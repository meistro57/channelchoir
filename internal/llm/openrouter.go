// filename: internal/llm/openrouter.go
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

const openRouterURL = "https://openrouter.ai/api/v1/chat/completions"

// OpenRouter talks to OpenRouter's OpenAI-compatible endpoint.
type OpenRouter struct {
	apiKey      string
	model       string
	temperature float64
	maxTokens   int
	referer     string
	title       string
	http        *http.Client
}

func NewOpenRouter(apiKey, model string, temperature float64, maxTokens int, referer, title string, hc *http.Client) *OpenRouter {
	return &OpenRouter{
		apiKey:      apiKey,
		model:       model,
		temperature: temperature,
		maxTokens:   maxTokens,
		referer:     referer,
		title:       title,
		http:        hc,
	}
}

func (c *OpenRouter) Describe() string { return "openrouter/" + c.model }

type orReasoning struct {
	// Exclude drops reasoning tokens from the response for models that
	// emit them. Harmless on models that don't.
	Exclude bool `json:"exclude"`
}

type orRequest struct {
	Model       string      `json:"model"`
	Messages    []Message   `json:"messages"`
	Temperature float64     `json:"temperature"`
	MaxTokens   int         `json:"max_tokens,omitempty"`
	Reasoning   orReasoning `json:"reasoning"`
}

type orResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	} `json:"error"`
}

func (c *OpenRouter) Speak(ctx context.Context, system, transcript string) (string, error) {
	body, err := json.Marshal(orRequest{
		Model:       c.model,
		Temperature: c.temperature,
		MaxTokens:   c.maxTokens,
		Reasoning:   orReasoning{Exclude: true},
		Messages: []Message{
			{Role: "system", Content: system},
			{Role: "user", Content: transcript},
		},
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openRouterURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	// Optional attribution headers. OpenRouter uses these for its rankings.
	if c.referer != "" {
		req.Header.Set("HTTP-Referer", c.referer)
	}
	if c.title != "" {
		req.Header.Set("X-Title", c.title)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("openrouter request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read openrouter response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openrouter: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}

	var out orResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("decode openrouter response: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("openrouter: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("openrouter returned no choices")
	}

	return Clean(out.Choices[0].Message.Content)
}
