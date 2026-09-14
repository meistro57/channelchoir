// filename: internal/llm/ollama.go
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

type Client struct {
	baseURL     string
	model       string
	temperature float64
	numPredict  int
	http        *http.Client
}

// NewOllama takes a *http.Client so the caller owns the timeout.
func NewOllama(baseURL, model string, temperature float64, numPredict int, hc *http.Client) *Client {
	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		model:       model,
		temperature: temperature,
		numPredict:  numPredict,
		http:        hc,
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string            `json:"model"`
	Messages []chatMessage     `json:"messages"`
	Stream   bool              `json:"stream"`
	Options  map[string]any    `json:"options,omitempty"`
	Think    *bool             `json:"think,omitempty"`
	KeepAlive string           `json:"keep_alive,omitempty"`
}

type chatResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Error string `json:"error"`
}

// thinkTags strips <think>...</think> blocks that reasoning models emit.
var thinkTags = regexp.MustCompile(`(?s)<think>.*?</think>`)

// Speak generates one line of dialogue. system is the persona brief,
// transcript is the recent channel history already formatted as text.
func (c *Client) Speak(ctx context.Context, system, transcript string) (string, error) {
	no := false
	body, err := json.Marshal(chatRequest{
		Model:  c.model,
		Stream: false,
		// Reasoning models burn a lot of tokens thinking before they say
		// "lol". Turn it off; Ollama ignores this for models without it.
		Think:     &no,
		KeepAlive: "30m",
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: transcript},
		},
		Options: map[string]any{
			"temperature": c.temperature,
			"num_predict": c.numPredict,
		},
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("ollama: %s: %s", resp.Status, string(msg))
	}

	var out chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode ollama response: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("ollama: %s", out.Error)
	}

	return Clean(out.Message.Content), nil
}

// Clean strips reasoning blocks, surrounding quotes, and the "Name:" prefix
// models love to add even when you tell them not to.
func Clean(s string) string {
	s = thinkTags.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)

	// Drop a leading "Somebody:" if the model narrated itself.
	if idx := strings.Index(s, ":"); idx > 0 && idx < 32 && !strings.Contains(s[:idx], " ") {
		s = strings.TrimSpace(s[idx+1:])
	}

	s = strings.Trim(s, "\"")
	return strings.TrimSpace(s)
}
