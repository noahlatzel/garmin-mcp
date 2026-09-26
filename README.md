# garmin-mcp — Garmin Connect MCP server for Claude & other AI assistants

> A small, self-hosted **[Model Context Protocol (MCP)](https://modelcontextprotocol.io)** server
> for **[Garmin Connect](https://connect.garmin.com)**, written in Go. Let Claude, Claude Code and
> any MCP-compatible AI assistant **build native strength and pool-swim workouts** for your watch
> and read your wellness data: sleep, HRV, stress, body battery. Run it as a single binary or a
> ~20 MB Docker image.

![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)
![Docker](https://img.shields.io/badge/Docker-ready-2496ED?logo=docker&logoColor=white)
![Model Context Protocol](https://img.shields.io/badge/MCP-compatible-6E56CF)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)
![License](https://img.shields.io/badge/license-MIT-blue)

Ask your AI assistant *"build me a 5×5 squat and bench session for tomorrow"* or *"add a 2 km
technique swim with drills and 100 m pace targets"* and it writes a **real, native** Garmin
workout (proper exercise names, reps, weights, strokes and drills) straight to Garmin Connect,
ready on your watch. No FIT files.

---

## Contents

- [What's different about this one](#whats-different-about-this-one)
- [Why Go](#why-go)
- [What you can do](#what-you-can-do-tools)
- [Run it with Docker](#run-it-with-docker)
- [Run it as a binary](#run-it-as-a-binary)
- [Run it on a server](#run-it-on-a-server)
- [Configuration reference](#configuration-reference)
- [Verify it works live](#verify-it-works-live)
- [Build from source & test](#build-from-source--test)
- [Sources](#sources)
- [FAQ](#faq)

---

## What's different about this one

Most Garmin MCP servers are written in Python and focus on reading your data. Some cover far more
of Garmin Connect than this one; [Taxuspt/garmin_mcp](https://github.com/Taxuspt/garmin_mcp), for
example, has over 100 tools. This server does fewer things and concentrates on writing workouts
that behave properly on the watch:

- **Strength workouts with real exercise names.** A catalog of 1,527 exercises, harvested from
  Garmin Connect's own workout builder, is built into the binary. Exercises are matched by
  display name ("Barbell Back Squat") or Garmin key. An unknown name comes back with up to five
  close matches, and `search_exercises` lets the assistant look one up.
- **Workouts that survive the trip to the watch.**
  - Each step holds exactly one exercise.
  - Sets are expanded into separate steps with rests between them. Garmin's repeat blocks
    collapse silently on the watch.
  - Every step carries its reps and weight, plus an optional coaching note.
- **Pool swims too.** Strokes, drills, equipment, pool length and pace targets.
- **A fortnight in one call.** `create_workouts` builds up to 14 strength or swim workouts at
  once. It checks every one before writing any, then reports each result separately so a failed
  item can be retried on its own.
- **Gated writes.** `delete_workout`, `unschedule_workout` and `update_workout_name` show what
  they would change and apply only with `confirm=true`. Every write supports `dry_run`. A rename
  re-fetches the workout to check the change took.
- **A sign-in that keeps working.** In March 2026 Garmin changed its mobile sign-in, and garth,
  the Python library most Garmin tools used for signing in, was deprecated. This server signs in
  through the web sign-in widget and exchanges it for DI OAuth2 tokens: the same token service
  the Garmin Connect app uses, whose refresh tokens renew as they're used. If that fails it falls
  back to the older OAuth1 tokens, which last about a year. Your password is used once and never
  stored.
- **A live self-test.** `garmin verify` round-trips the workout, calendar and wellness tools
  against your real account, using throwaway workouts dated five years ahead, then deletes them.
- **Tolerant of real clients.** Some MCP clients, including Claude's, send numbers, booleans and
  even arrays as strings. Every tool here accepts both forms.

## Why Go

The Python servers work well. For a small, always-on MCP server, Go has a few modest advantages:

- **Nothing to install at runtime.** One static binary; no Python interpreter, virtualenv or `uv`
  on the machine running your MCP client.
- **A small, locked-down container.** The Docker image is about 20 MB: distroless, non-root, no
  shell. It writes only to its token volume.
- **Fast start, low memory.** Desktop clients launch stdio servers on demand, so start-up time and
  idle memory are paid every session.
- **Built on the official SDK.** It uses the
  [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk), maintained by the MCP project with
  Google, with typed tool inputs checked at compile time.

## What you can do (tools)

**Write:**

| Tool | What it does |
|------|--------------|
| `create_strength_workout` | Build a native strength session (named lifts, sets, reps, weight, notes) |
| `create_swim_workout` | Build a native pool-swim session (strokes, drills, equipment, pace) |
| `create_workouts` | Build up to 14 strength or swim workouts in one call, each optionally scheduled |
| `schedule_workout` | Put a saved workout on a calendar date |
| `unschedule_workout` | Remove one calendar entry, keeping the workout (confirm-gated) |
| `update_workout_name` | Rename a saved workout (confirm-gated) |
| `delete_workout` | Delete a single workout (confirm-gated) |

**Read:** `search_exercises`, `list_workouts`, `get_workout`, `get_calendar`,
`get_daily_summary`, `get_sleep`, `get_hrv`, `get_stress`, `get_body_battery`.

Creates return a short summary (`workout_id`, name, sport, schedule date) rather than the full
Garmin JSON; pass `verbose=true` to the single-workout builders to get the full response.

## Run it with Docker

Build the image once:

```sh
git clone https://github.com/goosegit97/garmin-mcp.git
cd garmin-mcp
docker build -t garmin-mcp .
```

### Link your Garmin account (one time)

Garmin has no API keys, so you sign in **once** to get tokens. Your password is used for that one
sign-in and **never stored**; only the tokens are saved, in a Docker volume called `garmin-mcp`:

```sh
docker run -it --rm -v garmin-mcp:/data \
  -e GARMIN_EMAIL="you@example.com" -e GARMIN_PASSWORD="your-password" \
  garmin-mcp login
```

You'll be asked for your MFA code if your account uses one.

### Claude Desktop

Open your Claude Desktop config file:

- **macOS:** `~/Library/Application Support/Claude/claude_desktop_config.json`
- **Windows:** `%APPDATA%\Claude\claude_desktop_config.json`
- **Linux:** `~/.config/Claude/claude_desktop_config.json`

Add a `garmin` server. Claude Desktop starts the container over stdio, with no ports and no
network exposure:

```json
{
  "mcpServers": {
    "garmin": {
      "command": "docker",
      "args": ["run", "-i", "--rm", "-v", "garmin-mcp:/data", "garmin-mcp"]
    }
  }
}
```

Save, fully **restart Claude Desktop**, and the garmin tools appear. Ask *"list my upcoming Garmin
workouts"* to confirm.

### Claude Code

```sh
claude mcp add garmin -- docker run -i --rm -v garmin-mcp:/data garmin-mcp
```

## Run it as a binary

With [Go 1.26+](https://go.dev/dl/) installed:

```sh
go install github.com/goosegit97/garmin-mcp/cmd/garmin@latest
```

Link your account, choosing where the token file lives:

```sh
GARMIN_TOKEN_PATH=~/.garmin-mcp/tokens.json \
GARMIN_EMAIL="you@example.com" \
GARMIN_PASSWORD="your-password" \
garmin login
```

Keep the token file private (`chmod 600`): anyone with it can use your Garmin account. Then point
your client at the binary. For Claude Desktop, use its **full path** as the `command`, with
`"env": {"GARMIN_TOKEN_PATH": "/Users/you/.garmin-mcp/tokens.json"}`. For Claude Code:

```sh
claude mcp add garmin \
  --env GARMIN_TOKEN_PATH=$HOME/.garmin-mcp/tokens.json \
  -- garmin
```

## Run it on a server

To reach it from a remote MCP client, serve Streamable HTTP behind a bearer token. Link your
account into the `garmin-mcp` volume as above, put a long random `MCP_BEARER_TOKEN`
(`openssl rand -hex 32`) in `.env`, then:

```sh
docker compose up -d
```

[`compose.yaml`](compose.yaml) publishes the server on `127.0.0.1:8082` only, with a read-only
filesystem and all capabilities dropped. Put a TLS-terminating reverse proxy (Caddy, nginx,
Traefik) in front before exposing it. Every MCP request then needs
`Authorization: Bearer <token>`; `/healthz` stays open for health checks.

## Configuration reference

| Environment variable | Required | Meaning |
|----------------------|----------|---------|
| `GARMIN_TOKEN_PATH` | ✅ | File holding your Garmin tokens (keep at `0600`); `/data/tokens.json` in Docker |
| `GARMIN_LISTEN_ADDR` | — | Set (e.g. `127.0.0.1:8082`) to serve HTTP instead of stdio |
| `MCP_BEARER_TOKEN` | HTTP mode | Shared bearer token clients must present over HTTP |
| `GARMIN_EMAIL` / `GARMIN_PASSWORD` | login only | Read by `garmin login`; never stored |

## Verify it works live

`garmin verify` round-trips the tools against your account using throwaway far-future workouts,
which it then deletes:

```sh
docker run --rm -v garmin-mcp:/data garmin-mcp verify         # strength + reads
docker run --rm -v garmin-mcp:/data garmin-mcp verify swim    # pool-swim builder
docker run --rm -v garmin-mcp:/data garmin-mcp verify batch   # batch, unschedule, rename
```

Without Docker, run `garmin verify` with `GARMIN_TOKEN_PATH` set.

## Build from source & test

```sh
go build ./...
go vet ./...
go test ./...
```

CI also runs gofmt, [staticcheck](https://staticcheck.dev), `go test -race`,
[govulncheck](https://go.dev/doc/tutorial/govulncheck) and a Docker build on every push.

## Sources

- [python-garminconnect](https://github.com/cyberjunky/python-garminconnect): the Garmin Connect
  API paths, and the DI OAuth2 token endpoint and client ids (as of v0.3.16). Each path is proven
  by `garmin verify` rather than assumed.
- [garth](https://github.com/matin/garth) and its
  [deprecation notice](https://github.com/matin/garth/discussions/222): the original OAuth1 →
  OAuth2 exchange, and why the mobile sign-in stopped working.
- The exercise catalog and the strength and swim step formats were harvested from Garmin
  Connect's web workout builder.
- [MCP specification](https://modelcontextprotocol.io/specification) and the
  [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk).

## FAQ

**Is my Garmin password stored?** No. It's used for a single `garmin login` to get tokens; only
those tokens are saved, and the server only ever refreshes them.

**Are these real workouts or FIT files?** Real, native Garmin Connect workouts created through the
API. FIT files are deliberately not used.

**Is this an official Garmin API?** No. Garmin's official developer program is only open to
approved businesses, so this uses the same private API as the Garmin Connect app and website.
Garmin can change it without notice; `garmin verify` is the quick way to check it still works.

**Does it work with other AI assistants?** Any host that supports the Model Context Protocol can
use it. Setup mirrors the Claude examples above.

---

## License

Released under the [MIT License](LICENSE).

---

<sub>Keywords: Garmin Connect MCP server · Model Context Protocol · Claude · Anthropic · native
strength workouts · pool swim workouts · sleep · HRV · stress · body battery · self-hosted ·
Docker · Go.</sub>
