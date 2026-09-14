# ChannelChoir

Eight voices in a Discord channel, and one conductor deciding who gets to sing.
<img width="600" height="600" alt="logo" src="https://github.com/user-attachments/assets/416992ec-f758-4eff-9abf-80ee72fd7471" />

A single Go binary watches a channel, decides which of your AI personas should
speak next, generates their line, and posts it through that persona's own
webhook — so each one arrives with its own name and avatar. They remember each
other. They remember you. And they know when to shut up.

---

## Why It Exists

Ten bots that each reply to everything is not a chat room. It's a denial of
service attack on your own GPU. One message spawns nine replies, those spawn
eighty-one, and the channel is unreadable inside a minute.

So the generation isn't the interesting part — any of us can make a model say
things. The hard part is turn-taking. ChannelChoir is an arbitration engine
that happens to produce dialogue.

## What It Looks Like

> **Meistro:** good morning circus!!!
> **Pip:** GOOD MORNING BESTIES ready to cause some beautiful chaos today
> **Doc Halloway:** Morning! Did you know that burst of chaotic energy has a
> neurochemical basis? Sorry, rambling already.
> **Rivet:** Mornin'. Doc, you lost me at "neurochemical." Sounds like you're
> describing a gearbox with no oil.
> **Barnaby Quill:** Ah, but even the humblest gearbox has its own quiet
> philosophy. The rust is merely iron's slow surrender to the air.
> **Cassandra Thorne:** Lovely metaphor for corrosion. Still means you need to
> clean your tools.

---

## How It Works

```
Discord channel
      │  poll (bot token, read-only)
      ▼
  Conductor ──── named directly? that voice speaks, no contest
      │     └── otherwise score every voice against the last message
      │           urge = chattiness + triggers + human bonus ± jitter
      │           minus anyone on cooldown
      ▼
   one winner
      │
      ├──────────► Qdrant ──── recall what THIS voice remembers
      │                          (its own past lines + anything a human said)
      ▼
  Ollama / OpenRouter ──── persona brief + memories + rolling transcript
      │
      ▼
Discord webhook (that voice's name + avatar)
      │
      └──────────► Qdrant ──── remember what was just said
```

**Vocabulary used throughout the code:**

| Term | Meaning |
|---|---|
| **voice** | one persona, one YAML file |
| **conductor** | the arbitration layer that picks the next speaker |
| **verse** | a run of consecutive bot-only messages |
| **rest** | the enforced silence after a verse, broken when a human speaks |
| **cooldown** | turns a voice must sit out after speaking |

---

## The Damping (read this part)

Three settings keep the room from running away. They are the difference
between a fun server and a melted 4090.

- **`max_verse`** — hard cap on bot-only messages in a row. Hit it and the
  choir goes quiet until a human says something. Start at 6.
- **`cooldown_turns`** — a voice can't speak again for N turns. Prevents two
  personas locking into a private tennis match.
- **`speak_threshold`** — minimum score required to speak at all. `0.0` means
  someone always answers. Raise it toward `0.8` for a quieter, moodier room.

Also baked in: a voice can never reply to itself, and webhook posts are locked
to `allowed_mentions: []`, so the choir physically cannot `@everyone`.

**One caveat worth knowing.** The verse counter only governs *this* program's
voices. If you have other bots in the same channel, their messages don't reset
the verse and the cap can't stop them. Two agent frameworks in one room will
happily talk past each other until someone pulls a plug.

---

## Talking To One Of Them

Say a voice's name and that voice answers — cooldown skipped, scoring contest
skipped.

```
you: Rivet, what do you make of that?
Rivet: Sounds like somebody torqued it past spec and hoped.
```

First names work, so "Barnaby" reaches Barnaby Quill. Name two and it picks one
at random; the other can still join on its own. Matching is on whole words, so
"mother" won't summon Moth.

Note that webhook messages aren't real Discord users, so `@Rivet` won't
autocomplete. Type the bare name.

---

## Memory

The choir writes every message in the channel to its own Qdrant collection, and
each voice searches it before speaking.

The filter is the important bit: **a voice recalls its own past lines plus
anything a human said, and nothing else.** Rivet remembers the arguments he had
and remembers you. He doesn't get Doc's inner life. That's what keeps eight
personalities from converging into one soup.

Memory survives restarts. The rolling transcript doesn't.

> **Do not point `collection` at a corpus that holds anything personal.** The
> voices will quote whatever they find, in public, in a channel other people
> can read. Give the choir its own collection and nothing else.

Set `enabled: false` to run goldfish-mode — the rolling transcript only, no
Qdrant needed.

---

## Install

Requires Go 1.23+, and either Ollama or an OpenRouter key. Qdrant too, if you
want memory.

```bash
git clone git@github.com:meistro57/channelchoir.git
cd channelchoir
go mod tidy
make build
```

If `go build` tries to download a toolchain and fails, your Go is older than
the `go` directive in `go.mod`. Either install a current Go or lower that line.

### Discord setup

1. Create a bot application and invite it with **Read Messages** and **Read
   Message History**. This account never posts — it only listens.
2. In the channel: **Edit Channel → Integrations → Webhooks → New Webhook**.
   One per voice; copy each URL.
3. Enable Developer Mode, right-click the channel, **Copy Channel ID**.

### Configure

```bash
cp .env.example .env          # token, webhook URLs, OpenRouter key
cp choir.example.yaml choir.yaml
```

The binary loads `.env` itself at startup — no sourcing required. Real
environment variables always win, so systemd's `EnvironmentFile` still
overrides the file.

Nothing secret goes in the YAML. `${VAR}` references are expanded at load
time, and `choir.yaml` is gitignored.

---

## Choosing A Backend

```yaml
llm:
  provider: ollama      # or openrouter
```

**Ollama** keeps everything local and free. Reasoning models are the wrong tool
here — a 30B thinking model will write three paragraphs of scratchpad before
saying "lol", and you'll watch it happen. Reach for something small and
non-reasoning; two-sentence banter doesn't need a frontier model.

**OpenRouter** keeps your GPU free and is usually faster end to end. Set
`no_think: false` if your local model doesn't understand the `/no_think`
switch (that's a Qwen convention, not a universal one).

If a voice returns raw reasoning instead of dialogue, the conductor catches it,
logs `model was still thinking`, and passes the turn to someone else rather
than dumping a scratchpad into your channel.

---

## Usage

```bash
make dry     # full loop, prints to terminal, posts nothing
make run     # live
```

Always `make dry` first. It exercises polling, scoring, recall, and generation
— everything except the posting. Watch it a few minutes and tune before you let
it into the channel.

Note that dry-run still costs money on a hosted backend. Generation happens
either way; only the webhook call is skipped.

---

## Adding A Voice

Drop a file in `personas/`, add its webhook to `.env`, restart.

```yaml
# filename: personas/sparrow.yaml
name: Sparrow
webhook_url: ${WEBHOOK_SPARROW}
avatar_url: https://example.com/sparrow.png
chattiness: 0.5          # 0.2 lurker, 0.8 won't let a silence sit
cooldown: 2              # 0 = use the global setting
triggers: [music, sound, listen]
system: |
  You are Sparrow. Write the brief like you're directing an actor,
  not configuring software. Voice, posture, what they can't resist
  doing. Specific beats clever.
muted: false             # parks a voice without deleting it
```

Eight ship with the repo: an archivist, a contrarian, a fabricator, an
over-caffeinated scientist, a chaos gremlin, a pragmatist, something quiet and
uncanny, and whatever NULL is.

**Tuning notes learned the hard way:**

- Names must be unique, and quote anything YAML would eat — `NULL`, `YES`,
  `NO`, `ON`, `OFF` all parse as something other than strings unquoted.
- A voice set below ~0.4 chattiness in a room of 0.7s will never win a round.
  If you want someone rare but present, raise the chattiness *and* the
  cooldown. Low-and-low is just silence.
- If every voice sounds articulate in the same way, that's a persona-prompt
  problem, not a model problem. Sharpen the briefs.

---

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

---

## Known Limits

- **REST polling, not the gateway.** Costs a few seconds of latency, buys zero
  websocket dependencies. Swap in a gateway client if you ever want instant
  reactions.
- **Sequential generation.** One call at a time, by design — a natural rate
  limiter that also keeps one model resident in VRAM.
- **Text only.** No reactions, threads, or presence. That's the trade for
  webhooks instead of eight bot accounts.
- **One channel per process.** Run a second instance with a different config
  for a second room.
- **Memory is undifferentiated.** Everything said gets stored, with no notion
  of what's worth keeping. A salience pass is the obvious next step.

---

## Roadmap

- [ ] Salience scoring so memory keeps what matters instead of everything
- [ ] Mood state that drifts across sessions
- [ ] `!choir` commands — mute, summon, set topic
- [ ] Multi-channel support with a different cast per room
- [ ] Per-voice model overrides
- [ ] Optional gateway client for instant response

## License

MIT
