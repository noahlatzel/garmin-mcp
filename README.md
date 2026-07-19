# garmin-mcp — Garmin Connect MCP server for Claude & other AI assistants

> A self-hosted **[Model Context Protocol (MCP)](https://modelcontextprotocol.io)** server for
> **[Garmin Connect](https://connect.garmin.com)**. Let Claude, Claude Code, and any
> MCP-compatible AI assistant **build native strength & pool-swim workouts** on your watch and read
> your wellness data — sleep, HRV, stress, body battery — all from your own device.

![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)
![Model Context Protocol](https://img.shields.io/badge/MCP-compatible-6E56CF)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)
![Status](https://img.shields.io/badge/status-stable-brightgreen)

Ask your AI assistant *"build me a 5×5 squat + bench strength session for tomorrow"* or *"add a
2km technique swim with drills and 100m pace targets"* and it writes a **real, native** Garmin
workout — proper exercise names, reps, weights, strokes and drills — straight to Garmin Connect,
ready on your watch. No FIT-file hacks.

---

## Contents

- [Why garmin-mcp](#why-garmin-mcp)
- [What you can do](#what-you-can-do-tools)
- [Requirements](#requirements)
- [Install](#install)
- [Link your Garmin account (one time)](#link-your-garmin-account-one-time)
- [Use it on your device (Claude Desktop / Claude Code)](#use-it-on-your-device)
- [Configuration reference](#configuration-reference)
- [Verify it works live](#verify-it-works-live)
- [Self-host over HTTPS (optional)](#self-host-over-https-optional)
- [Build from source & test](#build-from-source--test)
- [FAQ](#faq)

---

## Why garmin-mcp

- **Native workouts, not files.** Strength and pool-swim sessions are written through the Garmin
  Connect API with named exercises, reps, weights, strokes, drills and pace targets — they look and
  behave exactly like ones you built by hand.
- **Runs on your machine.** Your Garmin login is used once to mint OAuth tokens that stay on your
  device; the running server only ever refreshes them.
- **Read your body metrics too** — daily summary, sleep, HRV, stress and body battery.
- **Single static binary.** Written in Go; nothing to install at runtime.
- **Works with any MCP client** — Claude Desktop, Claude Code, and other Model Context Protocol
  hosts.

## What you can do (tools)

**Write (the workout builders):**

| Tool | What it does |
|------|--------------|
| `create_strength_workout` | Build a native strength session (named lifts, sets, reps, weight) |
| `create_swim_workout` | Build a native pool-swim session (strokes, drills, equipment, pace) |
| `delete_workout` | Delete a single workout (fetch-then-confirm) |

**Read:** `list_workouts`, `get_workout`, `get_calendar`, `get_daily_summary`, `get_sleep`,
`get_hrv`, `get_stress`, `get_body_battery`.

The builders enforce hard correctness rules: one exercise per step (never merged), sets/repeats
expanded into discrete steps, named exercise + reps + optional weight from a harvested catalog.

## Requirements

- A **Garmin Connect** account (email + password; MFA supported).
- Either **[Go 1.25+](https://go.dev/dl/)** to install with one command, **or** a prebuilt binary.
- An MCP client such as **[Claude Desktop](https://claude.ai/download)** or
  **[Claude Code](https://claude.com/claude-code)**.

## Install

### Option A — `go install` (recommended)

```sh
go install github.com/goosegit97/garmin-mcp/cmd/garmin@latest
```

This drops a `garmin` (or `garmin.exe` on Windows) binary in your `$(go env GOPATH)/bin`. Add that
directory to your `PATH` if it isn't already.

### Option B — build from source

```sh
git clone https://github.com/goosegit97/garmin-mcp.git
cd garmin-mcp
go build -o garmin ./cmd/garmin
```

## Link your Garmin account (one time)

Garmin has no API keys, so you log in **once** to mint OAuth tokens. Your password is used for that
single login and **never stored** — only the resulting tokens are saved to `GARMIN_TOKEN_PATH`.

```sh
GARMIN_TOKEN_PATH=~/.garmin-mcp/tokens.json \
GARMIN_EMAIL="you@example.com" \
GARMIN_PASSWORD="your-password" \
garmin login
```

You'll be prompted for your MFA code if your account has it enabled. On success, the token file is
written and you never need your password again on this device.

> **Tip:** keep the token file private (`chmod 600`). Anyone with it can access your Garmin account.

## Use it on your device

The server speaks MCP over **stdio** by default — exactly what desktop MCP clients expect. No ports,
no bearer tokens, no network exposure.

### Claude Desktop

Open your Claude Desktop config file:

- **macOS:** `~/Library/Application Support/Claude/claude_desktop_config.json`
- **Windows:** `%APPDATA%\Claude\claude_desktop_config.json`
- **Linux:** `~/.config/Claude/claude_desktop_config.json`

Add a `garmin` server (use the **full path** to the binary, and the token path from the login step):

```json
{
  "mcpServers": {
    "garmin": {
      "command": "/Users/you/go/bin/garmin",
      "env": {
        "GARMIN_TOKEN_PATH": "/Users/you/.garmin-mcp/tokens.json"
      }
    }
  }
}
```

Save, fully **restart Claude Desktop**, and the garmin tools appear. Ask *"list my upcoming Garmin
workouts"* to confirm.

### Claude Code

```sh
claude mcp add garmin \
  --env GARMIN_TOKEN_PATH=$HOME/.garmin-mcp/tokens.json \
  -- garmin
```

Then run `claude` and your assistant can build and read workouts immediately.

## Configuration reference

| Environment variable | Required | Meaning |
|----------------------|----------|---------|
| `GARMIN_TOKEN_PATH` | ✅ | File holding your persisted OAuth tokens (keep at `0600`) |
| `GARMIN_LISTEN_ADDR` | — | Set (e.g. `127.0.0.1:8082`) to serve HTTP instead of stdio |
| `MCP_BEARER_TOKEN` | HTTP mode | Shared bearer token clients must present over HTTP |
| `GARMIN_EMAIL` / `GARMIN_PASSWORD` | login only | Read by `garmin login`; never persisted |

## Verify it works live

`garmin verify` round-trips the workout, calendar and wellness tools against your account using a
throwaway far-future workout it then deletes — the quickest way to confirm everything is wired up:

```sh
GARMIN_TOKEN_PATH=~/.garmin-mcp/tokens.json garmin verify        # strength + reads
GARMIN_TOKEN_PATH=~/.garmin-mcp/tokens.json garmin verify swim   # pool-swim builder
```

## Self-host over HTTPS (optional)

Prefer to run it on a VPS and connect a remote MCP client? Set `GARMIN_LISTEN_ADDR` and
`MCP_BEARER_TOKEN` to serve Streamable HTTP behind a bearer token (bind loopback; terminate TLS
with Caddy or similar). A hardened Debian 12 Ansible deploy — base + DevSec hardening + firewall +
Caddy + the garmin service — lives in [`ansible/`](ansible/).

## Build from source & test

```sh
go build ./...
go vet ./...
go test ./...
```

## FAQ

**Is my Garmin password stored?** No. It's used for a single `garmin login` to mint OAuth tokens;
only those tokens are saved locally, and the server only refreshes them.

**Are these real workouts or FIT files?** Real, native Garmin Connect workouts built through the
API — the FIT-file route is deliberately not used.

**Does it work with other AI assistants?** Any host supporting the Model Context Protocol can use
it; setup mirrors the Claude examples above.

---

<sub>Keywords: Garmin Connect MCP server · Model Context Protocol · Claude · Anthropic · native
strength workouts · pool swim workouts · sleep · HRV · stress · body battery · self-hosted · Go.</sub>
