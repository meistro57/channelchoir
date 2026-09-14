// filename: internal/persona/persona.go
package persona

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Persona is one voice in the choir. One YAML file per voice, living in
// personas/. Add a file, restart, and there's a new regular in the bar.
type Persona struct {
	// Name is what shows up as the Discord username on every message.
	Name string `yaml:"name"`

	// AvatarURL overrides the webhook's default avatar. Optional.
	AvatarURL string `yaml:"avatar_url"`

	// WebhookURL is this voice's own Discord webhook. Use ${ENV_VAR} here —
	// it's expanded at load time so real URLs stay out of the repo.
	WebhookURL string `yaml:"webhook_url"`

	// System is the character. Write it like you're briefing an actor.
	System string `yaml:"system"`

	// Triggers are words that make this voice want to jump in.
	Triggers []string `yaml:"triggers"`

	// Chattiness is the baseline urge to speak, roughly 0.0 - 1.0.
	// 0.2 is a lurker. 0.8 is the one who won't let a silence sit.
	Chattiness float64 `yaml:"chattiness"`

	// Cooldown overrides the global cooldown_turns for this voice.
	// 0 means "use the global setting".
	Cooldown int `yaml:"cooldown"`

	// Muted keeps the file around without letting the voice speak.
	Muted bool `yaml:"muted"`

	// lowercase trigger cache
	lowerTriggers []string
}

func (p *Persona) LowerTriggers() []string { return p.lowerTriggers }

// LoadDir reads every .yaml/.yml file in dir as a persona.
func LoadDir(dir string) ([]*Persona, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read persona dir: %w", err)
	}

	var out []*Persona
	seen := map[string]string{}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}

		var p Persona
		if err := yaml.Unmarshal([]byte(os.ExpandEnv(string(raw))), &p); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}

		if p.Muted {
			continue
		}
		if p.Name == "" {
			return nil, fmt.Errorf("%s: name is required", path)
		}
		if p.WebhookURL == "" {
			return nil, fmt.Errorf("%s: webhook_url is empty (did you export the env var?)", path)
		}
		if p.System == "" {
			return nil, fmt.Errorf("%s: system is required", path)
		}
		if prev, dup := seen[strings.ToLower(p.Name)]; dup {
			return nil, fmt.Errorf("%s: duplicate persona name %q (already defined in %s)", path, p.Name, prev)
		}
		seen[strings.ToLower(p.Name)] = path

		if p.Chattiness == 0 {
			p.Chattiness = 0.5
		}
		for _, t := range p.Triggers {
			p.lowerTriggers = append(p.lowerTriggers, strings.ToLower(t))
		}

		copied := p
		out = append(out, &copied)
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("no personas found in %s", dir)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Roster is a short line listing everyone in the room, injected into each
// voice's prompt so they know who they're talking to.
func Roster(ps []*Persona) string {
	names := make([]string, 0, len(ps))
	for _, p := range ps {
		names = append(names, p.Name)
	}
	return strings.Join(names, ", ")
}
