// filename: internal/llm/ollama.go
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Ollama talks to a local Ollama server.
type Ollama struct {
	baseURL     string
	model       string
	temperature float64
	numPredict  int
	noThink     bool
	http        *http.Client
}

// NewOllama takes a *http.Client so the caller owns the timeout.
func NewOllama(baseURL, model string, temperature float64, numPredict int, noThink bool, hc *http.Client) *Ollama {
	return &Ollama{
		baseURL:     strings.TrimRight(baseURL, "/"),
		model:       model,
		temperature: temperature,
		numPredict:  numPredict,
		noThink:     noThink,
		http:        hc,
	}
}

func (c *Ollama) Describe() string { return "ollama/" + c.model }

type ollamaRequest struct {
	Model     string         `json:"model"`
	Messages  []Message      `json:"messages"`
	Stream    bool           `json:"stream"`
	Options   map[string]any `json:"options,omitempty"`
	Think     *bool          `json:"think,omitempty"`
	KeepAlive string         `json:"keep_alive,omitempty"`
}

type ollamaResponse struct {
	Message struct {
		Content  string `json:"content"`
		Thinking string `json:"thinking"`
	} `json:"message"`
	Error string `json:"error"`
}

func (c *Ollama) Speak(ctx context.Context, system, transcript string) (string, error) {
	// Qwen3 and friends respect /no_think as a soft switch. Belt and
	// braces alongside the API field, which older Ollama builds ignore.
	if c.noThink {
		system += "\n\n/no_think"
	}

	no := false
	body, err := json.Marshal(ollamaRequest{
		Model:     c.model,
		Stream:    false,
		Think:     &no,
		KeepAlive: "30m",
		Messages: []Message{
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

	var out ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode ollama response: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("ollama: %s", out.Error)
	}

	return Clean(out.Message.Content)
}
