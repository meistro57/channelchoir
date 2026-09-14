// filename: internal/config/config.go
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the whole machine setup sheet. Anything secret should be written
// in choir.yaml as ${ENV_VAR} — it gets expanded from the environment on load,
// so no tokens ever live in the repo.
type Config struct {
	Discord    DiscordConfig    `yaml:"discord"`
	LLM        LLMConfig        `yaml:"llm"`
	Ollama     OllamaConfig     `yaml:"ollama"`
	OpenRouter OpenRouterConfig `yaml:"openrouter"`
	Memory     MemoryConfig     `yaml:"memory"`
	Conductor  ConductorConfig  `yaml:"conductor"`
}

// MemoryConfig is the choir's own long-term recall. It uses a dedicated
// Qdrant collection — never point this at a corpus that holds anything
// personal, because the voices will quote it back in public.
type MemoryConfig struct {
	Enabled    bool   `yaml:"enabled"`
	QdrantURL  string `yaml:"qdrant_url"`
	Collection string `yaml:"collection"`

	// RecallLimit is how many memories a voice gets before speaking.
	RecallLimit int `yaml:"recall_limit"`

	// MinScore is the cosine similarity floor. Too low and they recall
	// noise; too high and they never remember anything.
	MinScore float32 `yaml:"min_score"`

	TimeoutSecs int         `yaml:"timeout_seconds"`
	Embed       EmbedConfig `yaml:"embed"`
}

type EmbedConfig struct {
	// Provider is "openrouter" or "ollama".
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`

	// VectorSize must match what the model actually returns. A mismatch
	// fails loudly rather than corrupting the collection.
	VectorSize int `yaml:"vector_size"`

	// BaseURL is only used by the ollama provider.
	BaseURL string `yaml:"base_url"`
}

type DiscordConfig struct {
	// BotToken is only used to READ the channel. The voices post through
	// webhooks, which is how they get their own names and avatars.
	BotToken    string `yaml:"bot_token"`
	ChannelID   string `yaml:"channel_id"`
	PollSeconds int    `yaml:"poll_seconds"`
}

// LLMConfig picks which backend does the talking.
type LLMConfig struct {
	// Provider is "ollama" or "openrouter".
	Provider string `yaml:"provider"`
}

type OllamaConfig struct {
	BaseURL     string  `yaml:"base_url"`
	Model       string  `yaml:"model"`
	Temperature float64 `yaml:"temperature"`
	NumPredict  int     `yaml:"num_predict"`
	TimeoutSecs int     `yaml:"timeout_seconds"`

	// NoThink appends the /no_think switch for reasoning models like Qwen3.
	// Defaults to true. Set false for models that don't understand it.
	NoThink *bool `yaml:"no_think"`
}

type OpenRouterConfig struct {
	APIKey      string  `yaml:"api_key"`
	Model       string  `yaml:"model"`
	Temperature float64 `yaml:"temperature"`
	MaxTokens   int     `yaml:"max_tokens"`
	TimeoutSecs int     `yaml:"timeout_seconds"`

	// Optional attribution headers OpenRouter uses for its leaderboards.
	Referer string `yaml:"referer"`
	Title   string `yaml:"title"`
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
	// people typing instead of a machine gun. Set both to 0 for no delay.
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

	if c.LLM.Provider == "" {
		c.LLM.Provider = "ollama"
	}
	c.LLM.Provider = strings.ToLower(strings.TrimSpace(c.LLM.Provider))

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
		c.Ollama.NumPredict = 400
	}
	if c.Ollama.TimeoutSecs == 0 {
		c.Ollama.TimeoutSecs = 180
	}
	if c.Ollama.NoThink == nil {
		on := true
		c.Ollama.NoThink = &on
	}

	if c.OpenRouter.Temperature == 0 {
		c.OpenRouter.Temperature = 0.9
	}
	if c.OpenRouter.MaxTokens == 0 {
		c.OpenRouter.MaxTokens = 300
	}
	if c.OpenRouter.TimeoutSecs == 0 {
		c.OpenRouter.TimeoutSecs = 60
	}
	if c.OpenRouter.Title == "" {
		c.OpenRouter.Title = "ChannelChoir"
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
	if c.Conductor.TriggerWeight == 0 {
		c.Conductor.TriggerWeight = 0.35
	}
	if c.Conductor.DirectAddressBoost == 0 {
		c.Conductor.DirectAddressBoost = 2.0
	}
	if c.Conductor.Jitter == 0 {
		c.Conductor.Jitter = 0.4
	}

	if c.Memory.QdrantURL == "" {
		c.Memory.QdrantURL = "http://localhost:6333"
	}
	if c.Memory.Collection == "" {
		c.Memory.Collection = "choir_memory"
	}
	if c.Memory.RecallLimit == 0 {
		c.Memory.RecallLimit = 4
	}
	if c.Memory.MinScore == 0 {
		c.Memory.MinScore = 0.45
	}
	if c.Memory.TimeoutSecs == 0 {
		c.Memory.TimeoutSecs = 30
	}
	if c.Memory.Embed.Provider == "" {
		c.Memory.Embed.Provider = "openrouter"
	}
	c.Memory.Embed.Provider = strings.ToLower(strings.TrimSpace(c.Memory.Embed.Provider))
	if c.Memory.Embed.BaseURL == "" {
		c.Memory.Embed.BaseURL = c.Ollama.BaseURL
	}
	// Delay deliberately has no default — 0/0 is a valid "post immediately".
}

func (c *Config) validate() error {
	if c.Discord.BotToken == "" {
		return fmt.Errorf("discord.bot_token is empty (set DISCORD_BOT_TOKEN in your environment)")
	}
	if c.Discord.ChannelID == "" {
		return fmt.Errorf("discord.channel_id is empty")
	}

	switch c.LLM.Provider {
	case "ollama":
		// nothing else required
	case "openrouter":
		if c.OpenRouter.APIKey == "" {
			return fmt.Errorf("openrouter.api_key is empty (set OPENROUTER_API_KEY in your environment)")
		}
		if c.OpenRouter.Model == "" {
			return fmt.Errorf("openrouter.model is empty")
		}
	default:
		return fmt.Errorf("llm.provider %q is not recognized (use \"ollama\" or \"openrouter\")", c.LLM.Provider)
	}

	if c.Conductor.MinDelaySeconds < 0 || c.Conductor.MaxDelaySeconds < 0 {
		return fmt.Errorf("conductor delay values must not be negative")
	}
	if c.Conductor.MaxDelaySeconds < c.Conductor.MinDelaySeconds {
		return fmt.Errorf("conductor.max_delay_seconds must be >= min_delay_seconds")
	}

	if c.Memory.Enabled {
		switch c.Memory.Embed.Provider {
		case "openrouter":
			if c.OpenRouter.APIKey == "" {
				return fmt.Errorf("memory.embed.provider is openrouter but openrouter.api_key is empty")
			}
		case "ollama":
			// nothing else required
		default:
			return fmt.Errorf("memory.embed.provider %q is not recognized (use \"openrouter\" or \"ollama\")", c.Memory.Embed.Provider)
		}
		if c.Memory.Embed.Model == "" {
			return fmt.Errorf("memory.embed.model is empty")
		}
		if c.Memory.Embed.VectorSize <= 0 {
			return fmt.Errorf("memory.embed.vector_size must be set to the model's actual output size")
		}
	}
	return nil
}

func (c *OllamaConfig) Timeout() time.Duration {
	return time.Duration(c.TimeoutSecs) * time.Second
}

// SuppressThinking reports whether to append the /no_think switch.
func (c *OllamaConfig) SuppressThinking() bool {
	return c.NoThink == nil || *c.NoThink
}

func (c *OpenRouterConfig) Timeout() time.Duration {
	return time.Duration(c.TimeoutSecs) * time.Second
}

func (c *MemoryConfig) Timeout() time.Duration {
	return time.Duration(c.TimeoutSecs) * time.Second
}
