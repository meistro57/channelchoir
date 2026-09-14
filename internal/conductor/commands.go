// filename: internal/conductor/commands.go
package conductor

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/meistro57/channelchoir/internal/persona"
)

// parseCommand checks whether a human message is a command. It returns the
// parsed action and args, plus whether this message was actually a command.
func (c *Conductor) parseCommand(text string) (cmd string, args []string, ok bool) {
	prefix := strings.ToLower(strings.TrimSpace(c.cfg.Conductor.CommandPrefix))
	if prefix == "" {
		prefix = "!choir"
	}

	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, prefix) {
		return "", nil, false
	}

	rest := strings.TrimSpace(trimmed[len(prefix):])
	if rest == "" {
		return "help", nil, true
	}

	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "help", nil, true
	}
	return strings.ToLower(fields[0]), fields[1:], true
}

// handleCommand applies a human command and returns an acknowledgment string
// (empty means nothing worth posting back).
func (c *Conductor) handleCommand(ctx context.Context, cmd string, args []string) string {
	switch cmd {
	case "help":
		return c.helpText()
	case "mute":
		return c.muteCmd(args)
	case "unmute":
		return c.unmuteCmd(args)
	case "stop":
		c.verse = c.cfg.Conductor.MaxVerse
		c.restUntil = time.Now().Add(time.Duration(c.cfg.Conductor.RestSeconds) * time.Second)
		return fmt.Sprintf("room is resting %ds — a human speaking wakes it.", c.cfg.Conductor.RestSeconds)
	case "summon":
		return c.summonCmd(ctx, args)
	case "topic":
		return c.topicCmd(args)
	case "status":
		return c.statusCmd()
	case "voices":
		return c.voicesCmd()
	default:
		return fmt.Sprintf("unknown command %q — try %s help", cmd, c.cfg.Conductor.CommandPrefix)
	}
}

func (c *Conductor) findVoice(name string) *persona.Persona {
	target := strings.ToLower(strings.TrimSpace(name))
	for _, p := range c.voices {
		if strings.ToLower(strings.TrimSpace(p.Name)) == target {
			return p
		}
		if first, _, ok := strings.Cut(p.Name, " "); ok && strings.EqualFold(first, name) {
			return p
		}
	}
	return nil
}

func (c *Conductor) muteCmd(args []string) string {
	if len(args) == 0 {
		return "usage: mute <name>"
	}
	name := strings.Join(args, " ")
	if c.findVoice(name) == nil {
		return fmt.Sprintf("no voice named %q", name)
	}
	c.muted[strings.ToLower(strings.TrimSpace(name))] = true
	return fmt.Sprintf("muted %s", name)
}

func (c *Conductor) unmuteCmd(args []string) string {
	if len(args) == 0 {
		return "usage: unmute <name>"
	}
	name := strings.Join(args, " ")
	if c.findVoice(name) == nil {
		return fmt.Sprintf("no voice named %q", name)
	}
	delete(c.muted, strings.ToLower(strings.TrimSpace(name)))
	return fmt.Sprintf("unmuted %s", name)
}

func (c *Conductor) summonCmd(ctx context.Context, args []string) string {
	if len(args) == 0 {
		return "usage: summon <name>"
	}
	name := strings.Join(args, " ")
	p := c.findVoice(name)
	if p == nil {
		return fmt.Sprintf("no voice named %q", name)
	}
	if c.muted[strings.ToLower(strings.TrimSpace(p.Name))] {
		return fmt.Sprintf("%s is muted — unmute first to summon", p.Name)
	}
	if len(c.transcript) == 0 {
		return "nothing to react to yet"
	}
	last := c.transcript[len(c.transcript)-1]
	if err := c.perform(ctx, p, last); err != nil {
		log.Printf("summon %s failed: %v", p.Name, err)
		return fmt.Sprintf("couldn't summon %s: %v", p.Name, err)
	}
	return "" // the voice itself posted; nothing for the conductor to echo
}

func (c *Conductor) topicCmd(args []string) string {
	if len(args) == 0 {
		if c.topic == "" {
			return "no topic set"
		}
		return fmt.Sprintf("current topic: %s", c.topic)
	}
	c.topic = strings.Join(args, " ")
	return fmt.Sprintf("topic set: %s", c.topic)
}

func (c *Conductor) statusCmd() string {
	var mutedNames []string
	for _, p := range c.voices {
		if c.muted[strings.ToLower(strings.TrimSpace(p.Name))] {
			mutedNames = append(mutedNames, p.Name)
		}
	}
	sort.Strings(mutedNames)

	parts := []string{
		fmt.Sprintf("verse %d/%d", c.verse, c.cfg.Conductor.MaxVerse),
	}
	if len(mutedNames) > 0 {
		parts = append(parts, fmt.Sprintf("muted: %s", strings.Join(mutedNames, ", ")))
	} else {
		parts = append(parts, "none muted")
	}
	if c.topic != "" {
		parts = append(parts, fmt.Sprintf("topic: %s", c.topic))
	}
	return strings.Join(parts, " · ")
}

func (c *Conductor) voicesCmd() string {
	names := make([]string, 0, len(c.voices))
	for _, p := range c.voices {
		names = append(names, p.Name)
	}
	sort.Strings(names)
	return fmt.Sprintf("voices: %s", strings.Join(names, ", "))
}

func (c *Conductor) helpText() string {
	p := c.cfg.Conductor.CommandPrefix
	if p == "" {
		p = "!choir"
	}
	return fmt.Sprintf(
		"commands: %s help · mute <name> · unmute <name> · stop · summon <name> · topic [text] · status · voices",
		p)
}
