// filename: internal/config/config.go
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the whole machine setup sheet. Anything secret should be written
// in choir.yaml as ${ENV_VAR} — it gets expanded from the environment on load,
// so no tokens ever live in the repo.
type Config struct {
	Discord   DiscordConfig   `yaml:"discord"`
	Ollama    OllamaConfig    `yaml:"ollama"`
	Conductor ConductorConfig `yaml:"conductor"`
}

type DiscordConfig struct {
	// BotToken is only used to READ the channel. The voices post through
	// webhooks, which is how they get their own names and avatars.
	BotToken    string `yaml:"bot_token"`
	ChannelID   string `yaml:"channel_id"`
	PollSeconds int    `yaml:"poll_seconds"`
}

type OllamaConfig struct {
	BaseURL     string  `yaml:"base_url"`
	Model       string  `yaml:"model"`
	Temperature float64 `yaml:"temperature"`
	NumPredict  int     `yaml:"num_predict"`
	TimeoutSecs int     `yaml:"timeout_seconds"`
}

type ConductorConfig struct {
	PersonaDir string `yaml:"persona_dir"`

	// TranscriptSize is how many recent messages the voices can see.
	TranscriptSize int `yaml:"transcript_size"`

	// MaxVerse is the hard cap on consecutive bot-only messages. When the
	// choir hits it, everyone shuts up until a human says something.
	MaxVerse int `yaml:"max_verse"`

	// RestSeconds is the enforced silence after a verse ends.
	RestSeconds int `yaml:"rest_seconds"`

	// CooldownTurns is how many turns a voice must sit out after speaking.
	CooldownTurns int `yaml:"cooldown_turns"`

	// Delay range before a chosen voice actually posts, so it reads like
	// people typing instead of a machine gun.
	MinDelaySeconds int `yaml:"min_delay_seconds"`
	MaxDelaySeconds int `yaml:"max_delay_seconds"`

	// Scoring weights.
	TriggerWeight      float64 `yaml:"trigger_weight"`
	DirectAddressBoost float64 `yaml:"direct_address_boost"`
	Jitter             float64 `yaml:"jitter"`

	// SpeakThreshold is the minimum score required to speak at all. Raise it
	// to make the room quieter.
	SpeakThreshold float64 `yaml:"speak_threshold"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	expanded := os.ExpandEnv(string(raw))

	var c Config
	if err := yaml.Unmarshal([]byte(expanded), &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	c.applyDefaults()

	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Discord.PollSeconds == 0 {
		c.Discord.PollSeconds = 4
	}
	if c.Ollama.BaseURL == "" {
		c.Ollama.BaseURL = "http://localhost:11434"
	}
	if c.Ollama.Model == "" {
		c.Ollama.Model = "qwen3:30b-a3b"
	}
	if c.Ollama.Temperature == 0 {
		c.Ollama.Temperature = 0.9
	}
	if c.Ollama.NumPredict == 0 {
		c.Ollama.NumPredict = 160
	}
	if c.Ollama.TimeoutSecs == 0 {
		c.Ollama.TimeoutSecs = 180
	}
	if c.Conductor.PersonaDir == "" {
		c.Conductor.PersonaDir = "personas"
	}
	if c.Conductor.TranscriptSize == 0 {
		c.Conductor.TranscriptSize = 20
	}
	if c.Conductor.MaxVerse == 0 {
		c.Conductor.MaxVerse = 6
	}
	if c.Conductor.RestSeconds == 0 {
		c.Conductor.RestSeconds = 90
	}
	if c.Conductor.CooldownTurns == 0 {
		c.Conductor.CooldownTurns = 2
	}
	if c.Conductor.MinDelaySeconds == 0 {
		c.Conductor.MinDelaySeconds = 3
	}
	if c.Conductor.MaxDelaySeconds == 0 {
		c.Conductor.MaxDelaySeconds = 12
	}
	if c.Conductor.TriggerWeight == 0 {
		c.Conductor.TriggerWeight = 0.35
	}
	if c.Conductor.DirectAddressBoost == 0 {
		c.Conductor.DirectAddressBoost = 2.0
	}
	if c.Conductor.Jitter == 0 {
		c.Conductor.Jitter = 0.4
	}
	// SpeakThreshold intentionally has no default — 0 means "someone always
	// answers", which is a reasonable starting posture.
}

func (c *Config) validate() error {
	if c.Discord.BotToken == "" {
		return fmt.Errorf("discord.bot_token is empty (set DISCORD_BOT_TOKEN in your environment)")
	}
	if c.Discord.ChannelID == "" {
		return fmt.Errorf("discord.channel_id is empty")
	}
	if c.Conductor.MaxDelaySeconds < c.Conductor.MinDelaySeconds {
		return fmt.Errorf("conductor.max_delay_seconds must be >= min_delay_seconds")
	}
	return nil
}

func (c *OllamaConfig) Timeout() time.Duration {
	return time.Duration(c.TimeoutSecs) * time.Second
}
