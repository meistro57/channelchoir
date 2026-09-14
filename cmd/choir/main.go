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
	"github.com/meistro57/channelchoir/internal/llm"
	"github.com/meistro57/channelchoir/internal/persona"
)

func main() {
	cfgPath := flag.String("config", "choir.yaml", "path to config file")
	dryRun := flag.Bool("dry-run", false, "log what each voice would say instead of posting to Discord")
	flag.Parse()

	log.SetFlags(log.LstdFlags)

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

	model := llm.NewOllama(
		cfg.Ollama.BaseURL,
		cfg.Ollama.Model,
		cfg.Ollama.Temperature,
		cfg.Ollama.NumPredict,
		&http.Client{Timeout: cfg.Ollama.Timeout()},
	)

	c := conductor.New(cfg, voices, dc, model, *dryRun)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := c.Run(ctx); err != nil {
		log.Fatalf("choir stopped: %v", err)
	}
	log.Println("choir stopped cleanly")
}
