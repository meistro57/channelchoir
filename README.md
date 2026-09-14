# ChannelChoir

Ten voices in a Discord channel, and one conductor deciding who gets to sing.

## What It Is

A single Go binary that watches a Discord channel, picks which of your AI
personas should speak next, generates their line against a local Ollama model,
and posts it through that persona's own webhook — so each one shows up with its
own name and avatar.

## Why It Exists

Ten bots that each reply to everything is not a chat room, it's a denial of
service attack on your own GPU. One message spawns nine replies, those spawn
eighty-one, and the channel is unreadable inside a minute.

So the generation isn't the interesting part. The arbitration is. ChannelChoir
is a turn-taking engine that happens to produce dialogue.

## How It Works

```
Discord channel
      │  poll (bot token, read-only)
      ▼
  Conductor ──── scores every voice against the last message
      │           urge = chattiness + triggers + direct address ± jitter
      │           minus anyone on cooldown
      ▼
   one winner
      │
      ▼
   Ollama  ──── persona system prompt + rolling transcript
      │
      ▼
Discord webhook (that voice's name + avatar)
```

**Vocabulary used throughout the code:**

| Term | Meaning |
|---|---|
| **voice** | one persona, one YAML file |
| **conductor** | the arbitration layer that picks the next speaker |
| **verse** | a run of consecutive bot-only messages |
| **rest** | the enforced silence after a verse, broken when a human speaks |
| **cooldown** | turns a voice must sit out after speaking |

## The Damping (read this part)

Three settings keep the room from running away. They're the difference between
a fun server and a melted 4090.

- `max_verse` — hard cap on bot-only messages in a row. Hit it and the choir
  goes quiet until a human says something. Start at 6.
- `cooldown_turns` — a voice can't speak again for N turns. Prevents two
  personas locking into a private tennis match.
- `speak_threshold` — minimum score required to speak at all. `0.0` means
  someone always answers. Raise it toward `0.8` for a quieter, moodier room.

Also: a voice can never reply to itself, and webhook posts are locked to
`allowed_mentions: []`, so the choir physically cannot `@everyone`.

## Install

Requires Go 1.23+ and a running Ollama.

```bash
git clone git@github.com:meistro57/channelchoir.git
cd channelchoir
go mod tidy
make build
```

### Discord setup

1. Create a bot application, invite it to your server with **Read Messages** and
   **Read Message History**. This account never posts — it only listens.
2. In the channel: **Edit Channel → Integrations → Webhooks → New Webhook**.
   Make one per voice and copy each URL.
3. Enable Developer Mode in Discord, right-click the channel, **Copy Channel ID**.

### Configure

```bash
cp .env.example .env          # paste your token and webhook URLs
cp choir.example.yaml choir.yaml
set -a; source .env; set +a
```

Nothing secret goes in the YAML — `${VAR}` references are expanded from the
environment at load time, and `choir.yaml` is gitignored anyway.

## Usage

```bash
make dry     # log what each voice would say, post nothing
make run     # live
```

Always run `make dry` first. It exercises the full loop — polling, scoring,
generation — and prints the lines to your terminal instead of the channel.
Watch it for a few minutes and tune `max_verse` before you let it loose.

## Adding a Voice

Drop a new file in `personas/`, add its webhook to `.env`, restart.

```yaml
# filename: personas/yourname.yaml
name: Sparrow
webhook_url: ${WEBHOOK_SPARROW}
avatar_url: https://example.com/sparrow.png
chattiness: 0.5          # 0.2 lurker, 0.8 won't let silence sit
cooldown: 2              # 0 = use the global setting
triggers: [music, sound, listen]
system: |
  You are Sparrow. Write the character brief here like you're
  briefing an actor, not configuring software.
muted: false             # true parks a voice without deleting it
```

Eight are included to start. Names must be unique, and quote anything YAML
would eat — `NULL`, `YES`, `NO`, `ON`, `OFF`.

## Running It Persistently

```ini
# /etc/systemd/system/channelchoir.service
[Unit]
Description=ChannelChoir
After=network-online.target

[Service]
Type=simple
User=mark
WorkingDirectory=/home/mark/channelchoir
EnvironmentFile=/home/mark/channelchoir/.env
ExecStart=/home/mark/channelchoir/choir -config choir.yaml
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable --now channelchoir
journalctl -u channelchoir -f
```

## Known Limits

- **REST polling, not the gateway.** Costs you a few seconds of latency and
  buys you zero websocket dependencies. Fine for a room of chatterboxes; swap
  in a gateway client if you ever want instant reactions.
- **Sequential generation.** One Ollama call at a time, by design — it's a
  natural rate limiter and keeps one model resident in VRAM.
- **No long-term memory.** The voices remember `transcript_size` messages and
  nothing else. Wiring FrontPocket in per-voice is the obvious next move.
- **Text only.** No reactions, no threads, no presence — that's the tradeoff
  for webhooks instead of ten bot accounts.

## Roadmap

- [ ] Per-voice memory via FrontPocket
- [ ] Mood state that drifts over time
- [ ] `!choir` slash commands — mute, summon, set topic
- [ ] Multi-channel support with different casts per room
- [ ] Optional gateway client for instant response

## License

MIT
