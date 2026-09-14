// filename: internal/memory/memory.go
package memory

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/meistro57/channelchoir/internal/embed"
)

// Store is the choir's own long-term memory: one Qdrant collection, walled
// off from any other corpus on the box. Every message said in the channel
// lands here; before a voice speaks it searches this for what the moment
// reminds it of.
type Store struct {
	baseURL    string
	collection string
	embedder   embed.Embedder
	http       *http.Client
}

// Record is one remembered message.
type Record struct {
	// Voice is the persona that said it, empty for humans.
	Voice string `json:"voice"`
	// Speaker is the display name as it appeared in the channel.
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
	Human   bool   `json:"human"`
	// TS is unix seconds — Qdrant filters on numbers more happily than
	// on formatted timestamps.
	TS int64 `json:"ts"`
}

// When renders a rough human-readable age, for injecting into prompts.
func (r Record) When(now time.Time) string {
	d := now.Sub(time.Unix(r.TS, 0))
	switch {
	case d < 2*time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	default:
		return "a while back"
	}
}

func New(qdrantURL, collection string, embedder embed.Embedder, hc *http.Client) *Store {
	return &Store{
		baseURL:    strings.TrimRight(qdrantURL, "/"),
		collection: collection,
		embedder:   embedder,
		http:       hc,
	}
}

func (s *Store) Describe() string {
	return fmt.Sprintf("%s via %s", s.collection, s.embedder.Describe())
}

// Ensure creates the collection if it isn't there yet. Safe to call on every
// boot. It never touches an existing collection's config.
func (s *Store) Ensure(ctx context.Context) error {
	url := fmt.Sprintf("%s/collections/%s", s.baseURL, s.collection)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("reach qdrant at %s: %w", s.baseURL, err)
	}
	existed := resp.StatusCode == http.StatusOK
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if existed {
		return nil
	}

	body, err := json.Marshal(map[string]any{
		"vectors": map[string]any{
			"size":     s.embedder.Dims(),
			"distance": "Cosine",
		},
	})
	if err != nil {
		return err
	}

	createReq, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	createReq.Header.Set("Content-Type", "application/json")

	createResp, err := s.http.Do(createReq)
	if err != nil {
		return fmt.Errorf("create collection: %w", err)
	}
	defer createResp.Body.Close()

	if createResp.StatusCode < 200 || createResp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(createResp.Body, 1024))
		return fmt.Errorf("create collection %s: %s: %s", s.collection, createResp.Status, string(msg))
	}
	return nil
}

// Remember embeds one message and writes it to the collection.
func (s *Store) Remember(ctx context.Context, r Record) error {
	if strings.TrimSpace(r.Text) == "" {
		return nil
	}
	if r.TS == 0 {
		r.TS = time.Now().Unix()
	}

	vec, err := s.embedder.Embed(ctx, r.Text)
	if err != nil {
		return err
	}

	body, err := json.Marshal(map[string]any{
		"points": []map[string]any{{
			"id":     newID(),
			"vector": vec,
			"payload": map[string]any{
				"voice":   r.Voice,
				"speaker": r.Speaker,
				"text":    r.Text,
				"human":   r.Human,
				"ts":      r.TS,
			},
		}},
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/collections/%s/points?wait=false", s.baseURL, s.collection)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant upsert: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("qdrant upsert: %s: %s", resp.Status, string(msg))
	}
	return nil
}

type searchResponse struct {
	Result []struct {
		Score   float32 `json:"score"`
		Payload Record  `json:"payload"`
	} `json:"result"`
	Status any `json:"status"`
}

// Recall finds what this voice remembers about the current moment. It sees
// its OWN past lines plus anything a human said — never another voice's
// private recollections, which is what keeps the personalities separate.
func (s *Store) Recall(ctx context.Context, voice, query string, limit int, minScore float32) ([]Record, error) {
	if strings.TrimSpace(query) == "" || limit <= 0 {
		return nil, nil
	}

	vec, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, err
	}

	filter := map[string]any{
		"should": []map[string]any{
			{"key": "voice", "match": map[string]any{"value": voice}},
			{"key": "human", "match": map[string]any{"value": true}},
		},
	}

	body, err := json.Marshal(map[string]any{
		"vector":       vec,
		"limit":        limit,
		"with_payload": true,
		"filter":       filter,
		"score_threshold": minScore,
	})
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/collections/%s/points/search", s.baseURL, s.collection)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qdrant search: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<22))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("qdrant search: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}

	var out searchResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode qdrant search: %w", err)
	}

	recs := make([]Record, 0, len(out.Result))
	for _, hit := range out.Result {
		recs = append(recs, hit.Payload)
	}
	return recs, nil
}

// newID returns a random UUID. Qdrant point IDs must be a uint64 or a UUID,
// and random means no coordination needed between concurrent writes.
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is not a recoverable situation; fall back to
		// a time-derived id rather than panicking a chat bot.
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
