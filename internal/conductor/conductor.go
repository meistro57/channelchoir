// filename: internal/conductor/conductor.go
package conductor

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"time"

	"github.com/meistro57/channelchoir/internal/config"
	"github.com/meistro57/channelchoir/internal/discordio"
	"github.com/meistro57/channelchoir/internal/llm"
	"github.com/meistro57/channelchoir/internal/persona"
)

// Conductor is the whole point of this project. Ten voices generating text is
// easy; deciding who gets to talk, and when everybody shuts up, is the job.
type Conductor struct {
	cfg      *config.Config
	voices   []*persona.Persona
	discord  *discordio.Client
	model    *llm.Client
	rng      *rand.Rand
	dryRun   bool

	// transcript is the rolling memory of the room.
	transcript []line

	// turn increments every time anyone speaks, human or voice.
	turn int

	// lastSpoke[name] = turn number that voice last spoke on.
	lastSpoke map[string]int

	// verse counts consecutive voice messages with no human in between.
	verse int

	// restUntil is when the choir is allowed to sing again.
	restUntil time.Time

	lastMessageID string
}

type line struct {
	speaker string
	text    string
	human   bool
}

func New(cfg *config.Config, voices []*persona.Persona, dc *discordio.Client, model *llm.Client, dryRun bool) *Conductor {
	return &Conductor{
		cfg:       cfg,
		voices:    voices,
		discord:   dc,
		model:     model,
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
		dryRun:    dryRun,
		lastSpoke: map[string]int{},
	}
}

// Run polls the channel forever, conducting as it goes.
func (c *Conductor) Run(ctx context.Context) error {
	if err := c.seek(ctx); err != nil {
		return fmt.Errorf("find starting point: %w", err)
	}

	log.Printf("choir is live: %d voices, channel %s, model %s",
		len(c.voices), c.cfg.Discord.ChannelID, c.cfg.Ollama.Model)

	ticker := time.NewTicker(time.Duration(c.cfg.Discord.PollSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := c.tick(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				log.Printf("tick error: %v", err)
			}
		}
	}
}

// seek jumps to the present so the choir doesn't wake up and start replying to
// a month of backlog.
func (c *Conductor) seek(ctx context.Context) error {
	msgs, err := c.discord.FetchAfter(ctx, c.cfg.Discord.ChannelID, "", 1)
	if err != nil {
		return err
	}
	if len(msgs) > 0 {
		c.lastMessageID = msgs[len(msgs)-1].ID
	}
	return nil
}

func (c *Conductor) tick(ctx context.Context) error {
	msgs, err := c.discord.FetchAfter(ctx, c.cfg.Discord.ChannelID, c.lastMessageID, 50)
	if err != nil {
		return err
	}

	for _, m := range msgs {
		c.lastMessageID = m.ID
		if strings.TrimSpace(m.Content) == "" {
			continue
		}

		c.record(m.Speaker(), m.Content, m.IsHuman())

		if m.IsHuman() {
			// A human spoke. Wake the room up and clear the rest.
			c.verse = 0
			c.restUntil = time.Time{}
		}
	}

	if len(c.transcript) == 0 {
		return nil
	}
	if time.Now().Before(c.restUntil) {
		return nil
	}
	if c.verse >= c.cfg.Conductor.MaxVerse {
		c.restUntil = time.Now().Add(time.Duration(c.cfg.Conductor.RestSeconds) * time.Second)
		log.Printf("verse hit %d messages — resting %ds until a human speaks",
			c.verse, c.cfg.Conductor.RestSeconds)
		return nil
	}

	last := c.transcript[len(c.transcript)-1]

	// Nobody answers themselves.
	speaker := c.choose(last)
	if speaker == nil {
		return nil
	}

	return c.perform(ctx, speaker)
}

// choose scores every eligible voice against the last thing said and returns
// the winner, or nil if the room stays quiet.
func (c *Conductor) choose(last line) *persona.Persona {
	lowered := strings.ToLower(last.text)

	type scored struct {
		p     *persona.Persona
		score float64
	}
	var pool []scored

	for _, p := range c.voices {
		// Don't let a voice reply to itself.
		if strings.EqualFold(p.Name, last.speaker) {
			continue
		}

		cooldown := p.Cooldown
		if cooldown == 0 {
			cooldown = c.cfg.Conductor.CooldownTurns
		}
		if lastTurn, ok := c.lastSpoke[p.Name]; ok && c.turn-lastTurn <= cooldown {
			continue
		}

		score := p.Chattiness

		for _, t := range p.LowerTriggers() {
			if strings.Contains(lowered, t) {
				score += c.cfg.Conductor.TriggerWeight
			}
		}

		// Being named beats everything else.
		if strings.Contains(lowered, strings.ToLower(p.Name)) {
			score += c.cfg.Conductor.DirectAddressBoost
		}

		// A human question pulls harder than bot chatter.
		if last.human {
			score += 0.3
		}

		score += (c.rng.Float64() - 0.5) * 2 * c.cfg.Conductor.Jitter

		if score < c.cfg.Conductor.SpeakThreshold {
			continue
		}
		pool = append(pool, scored{p, score})
	}

	if len(pool) == 0 {
		return nil
	}

	best := pool[0]
	for _, s := range pool[1:] {
		if s.score > best.score {
			best = s
		}
	}
	return best.p
}

// perform waits a beat, generates the line, and posts it.
func (c *Conductor) perform(ctx context.Context, p *persona.Persona) error {
	delay := c.cfg.Conductor.MinDelaySeconds
	if spread := c.cfg.Conductor.MaxDelaySeconds - c.cfg.Conductor.MinDelaySeconds; spread > 0 {
		delay += c.rng.Intn(spread + 1)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Duration(delay) * time.Second):
	}

	text, err := c.model.Speak(ctx, c.systemPrompt(p), c.renderTranscript())
	if err != nil {
		return fmt.Errorf("%s failed to speak: %w", p.Name, err)
	}
	if strings.TrimSpace(text) == "" {
		log.Printf("%s had nothing to say", p.Name)
		return nil
	}

	if c.dryRun {
		log.Printf("[dry-run] %s: %s", p.Name, text)
	} else {
		if err := c.discord.PostWebhook(ctx, p.WebhookURL, p.Name, p.AvatarURL, text); err != nil {
			return fmt.Errorf("post as %s: %w", p.Name, err)
		}
	}

	// Record locally too. The poller will also see the webhook message come
	// back, but we dedupe by message ID, and having it now keeps the next
	// scoring pass honest.
	c.record(p.Name, text, false)
	return nil
}

func (c *Conductor) record(speaker, text string, human bool) {
	// Skip an exact repeat of the last line — this is what catches our own
	// webhook posts echoing back through the poller.
	if n := len(c.transcript); n > 0 {
		prev := c.transcript[n-1]
		if prev.speaker == speaker && prev.text == text {
			return
		}
	}

	c.transcript = append(c.transcript, line{speaker: speaker, text: text, human: human})
	if len(c.transcript) > c.cfg.Conductor.TranscriptSize {
		c.transcript = c.transcript[len(c.transcript)-c.cfg.Conductor.TranscriptSize:]
	}

	c.turn++
	if human {
		c.verse = 0
	} else {
		c.verse++
		c.lastSpoke[speaker] = c.turn
	}
}

func (c *Conductor) renderTranscript() string {
	var b strings.Builder
	b.WriteString("Recent messages in the channel:\n\n")
	for _, l := range c.transcript {
		fmt.Fprintf(&b, "%s: %s\n", l.speaker, l.text)
	}
	b.WriteString("\nWrite the next message.")
	return b.String()
}

func (c *Conductor) systemPrompt(p *persona.Persona) string {
	return fmt.Sprintf(`%s

You are %s, posting in a Discord channel.
Others in the channel: %s.

Rules:
- Write ONE message, exactly as you would actually type it in Discord.
- Keep it short. One to three sentences. Chat, not an essay.
- Do NOT prefix your message with your name.
- Do NOT narrate actions or use roleplay asterisks.
- Do NOT speak for anyone else.
- React to what was just said. Disagree, joke, or change the subject if that's in character.
- Never mention being an AI, a model, or a bot.`,
		p.System, p.Name, persona.Roster(c.voices))
}
