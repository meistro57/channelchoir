// filename: internal/discordio/discordio.go
package discordio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const apiBase = "https://discord.com/api/v10"

// maxContent is Discord's hard limit on a message body.
const maxContent = 2000

// Message is the slice of a Discord message we actually care about.
type Message struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	WebhookID string `json:"webhook_id"`
	Author    struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Bot      bool   `json:"bot"`
	} `json:"author"`
}

// IsHuman reports whether this message came from an actual person. Used for
// memory attribution (a human's words are recallable by every voice).
func (m Message) IsHuman() bool {
	return m.WebhookID == "" && !m.Author.Bot
}

// IsOurEcho reports whether this message is a webhook post — i.e. one of the
// choir's own webhook messages echoing back through the poller. These should
// never reset the verse or wake the room, or the choir would answer itself
// forever.
func (m Message) IsOurEcho() bool {
	return m.WebhookID != ""
}

// Speaker is the display name to use in the transcript.
func (m Message) Speaker() string { return m.Author.Username }

type Client struct {
	token string
	http  *http.Client
}

func New(token string) *Client {
	return &Client{
		token: token,
		http:  &http.Client{Timeout: 30 * time.Second},
	}
}

// FetchAfter returns messages newer than afterID, oldest first.
// Pass an empty afterID to get the most recent message only (used to find
// our starting point without replaying channel history).
func (c *Client) FetchAfter(ctx context.Context, channelID, afterID string, limit int) ([]Message, error) {
	url := fmt.Sprintf("%s/channels/%s/messages?limit=%d", apiBase, channelID, limit)
	if afterID != "" {
		url += "&after=" + afterID
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bot "+c.token)
	req.Header.Set("User-Agent", "ChannelChoir (https://github.com/meistro57/channelchoir, 0.1)")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		wait := retryAfter(resp)
		return nil, fmt.Errorf("rate limited by discord, retry in %s", wait)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("discord fetch: %s: %s", resp.Status, string(body))
	}

	var msgs []Message
	if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
		return nil, fmt.Errorf("decode messages: %w", err)
	}

	// Discord returns newest first. Flip it so the transcript reads forward.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

type webhookPayload struct {
	Content   string `json:"content"`
	Username  string `json:"username,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
	// AllowedMentions locked down so the choir can't @everyone itself into
	// a support ticket.
	AllowedMentions struct {
		Parse []string `json:"parse"`
	} `json:"allowed_mentions"`
}

// PostWebhook sends one message as the given identity. It retries once on a
// 429 using Discord's own retry_after value.
func (c *Client) PostWebhook(ctx context.Context, webhookURL, username, avatarURL, content string) error {
	if len(content) > maxContent {
		content = content[:maxContent-1] + "…"
	}

	var p webhookPayload
	p.Content = content
	p.Username = username
	p.AvatarURL = avatarURL
	p.AllowedMentions.Parse = []string{} // no pings, ever

	body, err := json.Marshal(p)
	if err != nil {
		return err
	}

	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			wait := retryAfter(resp)
			resp.Body.Close()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return nil
		}

		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		return fmt.Errorf("webhook post: %s: %s", resp.Status, string(msg))
	}

	return fmt.Errorf("webhook post: still rate limited after retry")
}

func retryAfter(resp *http.Response) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.ParseFloat(v, 64); err == nil {
			return time.Duration(secs*1000) * time.Millisecond
		}
	}
	return 2 * time.Second
}
