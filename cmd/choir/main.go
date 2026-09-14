// filename: cmd/choir/main.go
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/meistro57/channelchoir/internal/conductor"
	"github.com/meistro57/channelchoir/internal/config"
	"github.com/meistro57/channelchoir/internal/discordio"
	"github.com/meistro57/channelchoir/internal/dotenv"
	"github.com/meistro57/channelchoir/internal/embed"
	"github.com/meistro57/channelchoir/internal/llm"
	"github.com/meistro57/channelchoir/internal/memory"
	"github.com/meistro57/channelchoir/internal/persona"
)

func main() {
	cfgPath := flag.String("config", "choir.yaml", "path to config file")
	envPath := flag.String("env", ".env", "path to env file (optional)")
	dryRun := flag.Bool("dry-run", false, "log what each voice would say instead of posting to Discord")
	flag.Parse()

	log.SetFlags(log.LstdFlags)

	// Load .env before anything reads the environment. Existing environment
	// variables take priority, so this never fights systemd or your shell.
	if err := dotenv.Load(*envPath); err != nil {
		log.Fatalf("env: %v", err)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	voices, err := persona.LoadDir(cfg.Conductor.PersonaDir)
	if err != nil {
		log.Fatalf("personas: %v", err)
	}
	log.Printf("loaded %d voices: %s", len(voices), persona.Roster(voices))

	dc := discordio.New(cfg.Discord.BotToken)
	model := buildSpeaker(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mem, err := buildMemory(ctx, cfg)
	if err != nil {
		log.Fatalf("memory: %v", err)
	}

	c := conductor.New(cfg, voices, dc, model, mem, *dryRun)

	if err := c.Run(ctx); err != nil {
		log.Fatalf("choir stopped: %v", err)
	}
	log.Println("choir stopped cleanly")
}

// buildSpeaker wires up whichever chat backend the config asked for. The
// provider string is already validated by config.Load.
func buildSpeaker(cfg *config.Config) llm.Speaker {
	switch cfg.LLM.Provider {
	case "openrouter":
		return llm.NewOpenRouter(
			cfg.OpenRouter.APIKey,
			cfg.OpenRouter.Model,
			cfg.OpenRouter.Temperature,
			cfg.OpenRouter.MaxTokens,
			cfg.OpenRouter.Referer,
			cfg.OpenRouter.Title,
			&http.Client{Timeout: cfg.OpenRouter.Timeout()},
		)
	default:
		return llm.NewOllama(
			cfg.Ollama.BaseURL,
			cfg.Ollama.Model,
			cfg.Ollama.Temperature,
			cfg.Ollama.NumPredict,
			cfg.Ollama.SuppressThinking(),
			&http.Client{Timeout: cfg.Ollama.Timeout()},
		)
	}
}

// buildMemory returns nil when memory is switched off, which the conductor
// treats as "no long-term recall" rather than an error.
func buildMemory(ctx context.Context, cfg *config.Config) (*memory.Store, error) {
	if !cfg.Memory.Enabled {
		return nil, nil
	}

	hc := &http.Client{Timeout: cfg.Memory.Timeout()}

	var embedder embed.Embedder
	switch cfg.Memory.Embed.Provider {
	case "ollama":
		embedder = embed.NewOllama(
			cfg.Memory.Embed.BaseURL,
			cfg.Memory.Embed.Model,
			cfg.Memory.Embed.VectorSize,
			hc,
		)
	default:
		embedder = embed.NewOpenRouter(
			cfg.OpenRouter.APIKey,
			cfg.Memory.Embed.Model,
			cfg.Memory.Embed.VectorSize,
			hc,
		)
	}

	store := memory.New(cfg.Memory.QdrantURL, cfg.Memory.Collection, embedder, hc)
	if err := store.Ensure(ctx); err != nil {
		return nil, err
	}
	return store, nil
}
