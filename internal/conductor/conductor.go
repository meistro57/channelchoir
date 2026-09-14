// filename: internal/conductor/conductor.go
package conductor

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"time"
	"unicode"

	"github.com/meistro57/channelchoir/internal/config"
	"github.com/meistro57/channelchoir/internal/discordio"
	"github.com/meistro57/channelchoir/internal/llm"
	"github.com/meistro57/channelchoir/internal/memory"
	"github.com/meistro57/channelchoir/internal/persona"
)

// Conductor is the whole point of this project. Ten voices generating text is
// easy; deciding who gets to talk, and when everybody shuts up, is the job.
type Conductor struct {
	cfg     *config.Config
	voices  []*persona.Persona
	discord *discordio.Client
	model   llm.Speaker
	mem     *memory.Store // nil when memory is disabled
	rng     *rand.Rand
	dryRun  bool

	names []string

	// transcript is the rolling short-term memory of the room.
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

	// muted is the session-only set of parked voices (keyed by lowercased name).
	muted map[string]bool

	// topic is an optional session topic injected into prompts.
	topic string
}

type line struct {
	speaker string
	text    string
	human   bool
}

func New(cfg *config.Config, voices []*persona.Persona, dc *discordio.Client, model llm.Speaker, mem *memory.Store, dryRun bool) *Conductor {
	names := make([]string, 0, len(voices))
	for _, v := range voices {
		names = append(names, v.Name)
	}
	return &Conductor{
		cfg:       cfg,
		voices:    voices,
		discord:   dc,
		model:     model,
		mem:       mem,
		names:     names,
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
		dryRun:    dryRun,
		lastSpoke: map[string]int{},
		muted:     map[string]bool{},
	}
}

// Run polls the channel forever, conducting as it goes.
func (c *Conductor) Run(ctx context.Context) error {
	if err := c.seek(ctx); err != nil {
		return fmt.Errorf("find starting point: %w", err)
	}

	log.Printf("choir is live: %d voices, channel %s, model %s",
		len(c.voices), c.cfg.Discord.ChannelID, c.model.Describe())
	if c.mem != nil {
		log.Printf("memory: %s", c.mem.Describe())
	} else {
		log.Printf("memory: disabled")
	}

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

			// Commands are handled before any address/scoring logic so a
			// mute actually takes effect and a summon speaks regardless of
			// cooldown. We only treat the most recent message as a command,
			// so a batch of backlog won't trigger a pile of actions.
		}
	}

	// If the most recent thing a human said was a command, act on it and
	// stop — commands don't get scored, addressed, or turned into banter.
	if n := len(c.transcript); n > 0 {
		if last := c.transcript[n-1]; last.human {
			if cmd, args, ok := c.parseCommand(last.text); ok {
				if ack := c.handleCommand(ctx, cmd, args); ack != "" {
					c.acknowledge(ctx, ack)
				}
				return nil
			}
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

	// A human calling someone by name gets that voice, full stop — no
	// cooldown, no scoring contest. This is how you talk to one of them.
	if last.human {
		if p := c.addressed(last.text); p != nil {
			return c.perform(ctx, p, last)
		}
	}

	speaker := c.choose(last)
	if speaker == nil {
		return nil
	}

	return c.perform(ctx, speaker, last)
}

// addressed returns the voice a message calls out by name, or nil. When
// several are named it picks one at random and lets the rest join normally.
func (c *Conductor) addressed(text string) *persona.Persona {
	tokens := tokenize(text)

	var named []*persona.Persona
	for _, p := range c.voices {
		if addresses(tokens, p) {
			named = append(named, p)
		}
	}

	switch len(named) {
	case 0:
		return nil
	case 1:
		return named[0]
	default:
		return named[c.rng.Intn(len(named))]
	}
}

// tokenize splits a message into lowercase word tokens with punctuation
// stripped, so "Rivet," matches and "mother" does not summon Moth.
func tokenize(s string) map[string]bool {
	out := map[string]bool{}
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	for _, f := range fields {
		out[f] = true
	}
	return out
}

// addresses reports whether the tokens name this voice. A voice answers to
// its full name and to its first word, so "Barnaby" reaches Barnaby Quill.
func addresses(tokens map[string]bool, p *persona.Persona) bool {
	name := strings.ToLower(strings.TrimSpace(p.Name))
	if tokens[name] {
		return true
	}
	if first, _, ok := strings.Cut(name, " "); ok && first != "" && tokens[first] {
		return true
	}
	return false
}

// choose scores every eligible voice against the last thing said and returns
// the winner, or nil if the room stays quiet.
func (c *Conductor) choose(last line) *persona.Persona {
	lowered := strings.ToLower(last.text)
	tokens := tokenize(last.text)

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

		// A muted voice is parked and won't win a round (summon still
		// reaches it if explicitly asked, but it's checked there too).
		if c.muted[strings.ToLower(strings.TrimSpace(p.Name))] {
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
		if addresses(tokens, p) {
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

// perform waits a beat, recalls what it can, generates the line, and posts it.
func (c *Conductor) perform(ctx context.Context, p *persona.Persona, last line) error {
	delay := c.cfg.Conductor.MinDelaySeconds
	if spread := c.cfg.Conductor.MaxDelaySeconds - c.cfg.Conductor.MinDelaySeconds; spread > 0 {
		delay += c.rng.Intn(spread + 1)
	}
	if delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(delay) * time.Second):
		}
	}

	recalled := c.recall(ctx, p, last.text)

	text, err := c.model.Speak(ctx, c.systemPrompt(p), c.renderTranscript(p, recalled))
	if err != nil {
		if errors.Is(err, llm.ErrTruncatedThinking) {
			// Don't burn the turn on a scratchpad. Bench this voice and
			// let someone else try on the next tick.
			log.Printf("%s: %v (raise the token limit or check no_think)", p.Name, err)
			c.lastSpoke[p.Name] = c.turn
			return nil
		}
		return fmt.Errorf("%s failed to speak: %w", p.Name, err)
	}

	text = llm.StripSpeakerPrefix(text, c.names)
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

	c.record(p.Name, text, false)
	return nil
}

// recall pulls this voice's relevant memories. Failures are logged and
// swallowed — a memory outage makes the choir forgetful, not broken.
func (c *Conductor) recall(ctx context.Context, p *persona.Persona, query string) []memory.Record {
	if c.mem == nil {
		return nil
	}

	limit := c.cfg.Memory.RecallLimit
	rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	recs, err := c.mem.Recall(rctx, p.Name, query, limit, c.cfg.Memory.MinScore)
	if err != nil {
		log.Printf("recall for %s failed: %v", p.Name, err)
		return nil
	}

	// Drop anything already sitting in the visible transcript — no point
	// "remembering" a line that's three messages up the screen.
	recent := map[string]bool{}
	for _, l := range c.transcript {
		recent[l.text] = true
	}

	out := make([]memory.Record, 0, len(recs))
	for _, r := range recs {
		if recent[r.Text] {
			continue
		}
		out = append(out, r)
	}
	return out
}

// remember writes a line to long-term memory in the background so the
// conversation never waits on an embedding call.
func (c *Conductor) remember(r memory.Record) {
	if c.mem == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := c.mem.Remember(ctx, r); err != nil {
			log.Printf("remember failed (%s): %v", r.Speaker, err)
		}
	}()
}

// acknowledge posts the conductor's own reply to a command, using the
// conductor webhook so it appears distinctly in-channel. No webhook => log only.
func (c *Conductor) acknowledge(ctx context.Context, ack string) {
	if ack == "" {
		return
	}
	if c.cfg.Conductor.ConductorWebhookURL == "" {
		log.Printf("[command] %s", ack)
		return
	}
	name := c.cfg.Conductor.ConductorUsername
	if name == "" {
		name = "Conductor"
	}
	if c.dryRun {
		log.Printf("[dry-run] %s: %s", name, ack)
		return
	}
	if err := c.discord.PostWebhook(ctx, c.cfg.Conductor.ConductorWebhookURL, name, "", ack); err != nil {
		log.Printf("acknowledge failed: %v", err)
	}
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

	voice := speaker
	if human {
		voice = ""
	}
	c.remember(memory.Record{
		Voice:   voice,
		Speaker: speaker,
		Text:    text,
		Human:   human,
		TS:      time.Now().Unix(),
	})
}

func (c *Conductor) renderTranscript(p *persona.Persona, recalled []memory.Record) string {
	var b strings.Builder

	if len(recalled) > 0 {
		now := time.Now()
		b.WriteString("Things you remember from earlier conversations:\n")
		for _, r := range recalled {
			who := r.Speaker
			if r.Voice == p.Name {
				who = "you"
			}
			fmt.Fprintf(&b, "- (%s) %s said: %s\n", r.When(now), who, r.Text)
		}
		b.WriteString("\nOnly bring these up if they're actually relevant.\n\n")
	}

	b.WriteString("Recent messages in the channel:\n\n")
	for _, l := range c.transcript {
		fmt.Fprintf(&b, "%s: %s\n", l.speaker, l.text)
	}
	// Naming the speaker at the very end matters — without it the model
	// drifts into whoever it was reading about last.
	fmt.Fprintf(&b, "\nYou are %s. Write only your next message, nothing else.", p.Name)
	return b.String()
}

func (c *Conductor) systemPrompt(p *persona.Persona) string {
	topic := ""
	if c.topic != "" {
		topic = fmt.Sprintf("The group is currently discussing: %s. Lean into it when it fits.\n\n", c.topic)
	}
	return fmt.Sprintf(`%s

You are %s, posting in a Discord channel.
Others in the channel: %s.

%sRules:
- Output ONLY the message text. No reasoning, no explanation, no preamble.
- Write ONE message, exactly as you would actually type it in Discord.
- Keep it SHORT. Under 30 words. Usually one sentence, two at the most.
- Do not deliver a lecture, a list, or a paragraph. This is a chat window.
- Do NOT prefix your message with your name.
- Do NOT narrate actions or use roleplay asterisks.
- Do NOT speak for anyone else.
- React to what was just said. Disagree, joke, or change the subject if that's in character.
- If you remember something relevant from before, reference it naturally, the
  way a person would. Never announce that you are recalling something.
- Never mention being an AI, a model, or a bot.`,
		p.System, p.Name, persona.Roster(c.voices), topic)
}
