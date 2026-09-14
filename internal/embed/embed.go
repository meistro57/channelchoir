// filename: internal/embed/embed.go
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Embedder turns text into a vector. Both backends satisfy it so the memory
// store doesn't care which one is wired in.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	Dims() int
	Describe() string
}

// ---------- OpenRouter ----------

const openRouterEmbedURL = "https://openrouter.ai/api/v1/embeddings"

type OpenRouter struct {
	apiKey string
	model  string
	dims   int
	http   *http.Client
}

func NewOpenRouter(apiKey, model string, dims int, hc *http.Client) *OpenRouter {
	return &OpenRouter{apiKey: apiKey, model: model, dims: dims, http: hc}
}

func (e *OpenRouter) Dims() int        { return e.dims }
func (e *OpenRouter) Describe() string { return "openrouter/" + e.model }

type orEmbedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type orEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (e *OpenRouter) Embed(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(orEmbedRequest{Model: e.model, Input: text})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openRouterEmbedURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter embed request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<22))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openrouter embed: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}

	var out orEmbedResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode openrouter embed: %w", err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("openrouter embed: %s", out.Error.Message)
	}
	if len(out.Data) == 0 || len(out.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("openrouter embed returned no vector")
	}

	vec := out.Data[0].Embedding
	if e.dims > 0 && len(vec) != e.dims {
		// Dimension mismatches must fail loudly — a silent reindex against
		// the wrong size corrupts the whole collection.
		return nil, fmt.Errorf("embedding dimension mismatch: got %d, config says %d", len(vec), e.dims)
	}
	return vec, nil
}

// ---------- Ollama ----------

type Ollama struct {
	baseURL string
	model   string
	dims    int
	http    *http.Client
}

func NewOllama(baseURL, model string, dims int, hc *http.Client) *Ollama {
	return &Ollama{baseURL: strings.TrimRight(baseURL, "/"), model: model, dims: dims, http: hc}
}

func (e *Ollama) Dims() int        { return e.dims }
func (e *Ollama) Describe() string { return "ollama/" + e.model }

type ollamaEmbedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaEmbedResponse struct {
	Embedding []float32 `json:"embedding"`
	Error     string    `json:"error"`
}

func (e *Ollama) Embed(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(ollamaEmbedRequest{Model: e.model, Prompt: text})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<22))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama embed: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}

	var out ollamaEmbedResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode ollama embed: %w", err)
	}
	if out.Error != "" {
		return nil, fmt.Errorf("ollama embed: %s", out.Error)
	}
	if len(out.Embedding) == 0 {
		return nil, fmt.Errorf("ollama embed returned no vector")
	}
	if e.dims > 0 && len(out.Embedding) != e.dims {
		return nil, fmt.Errorf("embedding dimension mismatch: got %d, config says %d", len(out.Embedding), e.dims)
	}
	return out.Embedding, nil
}
