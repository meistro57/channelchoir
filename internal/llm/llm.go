// filename: internal/llm/llm.go
package llm

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

// ErrTruncatedThinking means the model spent its whole token budget reasoning
// and never got to the actual message. Bump the token limit or disable
// thinking for that model.
var ErrTruncatedThinking = errors.New("model was still thinking when it ran out of tokens")

// Speaker is anything that can produce one line of dialogue. Ollama and
// OpenRouter both satisfy it, so the conductor doesn't care which is wired in.
type Speaker interface {
	Speak(ctx context.Context, system, transcript string) (string, error)
	Describe() string
}

// closedThink matches a complete reasoning block.
var closedThink = regexp.MustCompile(`(?s)<think>.*?</think>`)

// Clean strips reasoning blocks, surrounding quotes, and stray markup.
func Clean(s string) (string, error) {
	// Complete <think>...</think> blocks: drop them.
	s = closedThink.ReplaceAllString(s, "")

	// An opening tag with no close means the budget ran out mid-thought.
	if strings.Contains(s, "<think>") {
		return "", ErrTruncatedThinking
	}

	// Some builds emit bare reasoning with no tags at all. If the whole
	// response reads like a scratchpad, treat it as unusable.
	lower := strings.ToLower(strings.TrimSpace(s))
	for _, tell := range []string{
		"okay, the user wants",
		"okay, let me",
		"okay, let's",
		"let me break this down",
		"let me unpack",
		"first, i need to",
	} {
		if strings.HasPrefix(lower, tell) {
			return "", ErrTruncatedThinking
		}
	}

	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"")
	return strings.TrimSpace(s), nil
}

// StripSpeakerPrefix removes a leading "Name:" when the model narrated itself
// despite being told not to.
func StripSpeakerPrefix(s string, names []string) string {
	for _, n := range names {
		p := n + ":"
		if len(s) >= len(p) && strings.EqualFold(s[:len(p)], p) {
			return strings.TrimSpace(s[len(p):])
		}
	}
	return s
}

// Message is the provider-neutral chat message shape. Both backends happen to
// use the same one.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
