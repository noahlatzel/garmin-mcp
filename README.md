# garmin-mcp

A single-user, self-hostable [Model Context Protocol](https://modelcontextprotocol.io) server for
[Garmin Connect](https://connect.garmin.com). It writes **native** strength and pool-swim workouts
(named exercises, reps, weight, strokes, drills, pace targets — never FIT files) and serves the
Garmin wellness reads. One deployment serves one linked Garmin account.

It began as one service inside the `gaggle` monorepo and was extracted to stand alone: the
multi-tenant OAuth2 front door is replaced by a single shared bearer token. The optional David
Lloyd → Garmin weigh-in sync (`davidlloyd`) rides along as a separate binary + timer.

## Tools

**Writes** (workout builders — the crown jewels): `create_strength_workout`, `create_swim_workout`,
`delete_workout` (single-id, fetch-then-confirm).
**Reads**: `list_workouts`, `get_workout`, `get_calendar`, `get_daily_summary`, `get_sleep`,
`get_hrv`, `get_stress`, `get_body_battery`.

The builders enforce hard rules: one exercise per step (never merged), sets/repeats expanded into
discrete steps (never `Nx` blocks), named exercise + reps + optional weight from the harvested ID
catalog. The FIT-file route is deliberately rejected — everything goes via the Connect API.

## Build & test

```sh
go build ./...
go vet ./...
go test ./...
```

## Link the account (one-time, human-gated)

The one credentialed login is run by hand — it reads `GARMIN_EMAIL` / `GARMIN_PASSWORD`, prompts
for MFA on stdin, and persists only OAuth tokens to `GARMIN_TOKEN_PATH`:

```sh
GARMIN_TOKEN_PATH=./garmin-tokens.json GARMIN_EMAIL=... GARMIN_PASSWORD=... \
go run ./cmd/garmin login
```

## Run

```sh
# stdio (local MCP clients, no auth):
GARMIN_TOKEN_PATH=./garmin-tokens.json go run ./cmd/garmin

# Streamable HTTP behind a bearer token (loopback; put Caddy/TLS in front):
GARMIN_TOKEN_PATH=./garmin-tokens.json \
GARMIN_LISTEN_ADDR=127.0.0.1:8082 \
MCP_BEARER_TOKEN=$(openssl rand -hex 32) \
go run ./cmd/garmin
```

Every HTTP request must carry `Authorization: Bearer <token>`; `/healthz` is unauthenticated.

## Verify live (on the box)

`garmin verify` round-trips the workout/calendar/wellness tools against the linked account via a
throwaway far-future workout it then deletes (uses the token store directly — no MCP/JWT). This is
the check that the Connect API paths still answer live:

```sh
GARMIN_TOKEN_PATH=... go run ./cmd/garmin verify        # strength + reads
GARMIN_TOKEN_PATH=... go run ./cmd/garmin verify swim   # pool-swim builder
```

## Environment

| Var | Required | Meaning |
|-----|----------|---------|
| `GARMIN_TOKEN_PATH` | yes | file holding persisted OAuth tokens (0600) |
| `GARMIN_LISTEN_ADDR` | no | e.g. `127.0.0.1:8082`; unset ⇒ stdio |
| `MCP_BEARER_TOKEN` | HTTP mode | shared bearer token clients must present |
| `GARMIN_EMAIL` / `GARMIN_PASSWORD` | login only | read by `garmin login`; never persisted |

## David Lloyd weight sync (optional)

`cmd/davidlloyd` is a headless one-shot that reads your latest David Lloyd gym scale weight and
writes it to Garmin Connect (which then syncs onward). It shares the garmin token store. Log in
once by hand (`davidlloyd login`, reads `DL_USERNAME`/`DL_PASSWORD`, stores only a session token),
then run `davidlloyd sync`. In production it deploys behind the `davidlloyd_enabled` Ansible var as
a daily systemd timer.

## Deploy

A hardened Debian 12 VPS deploy lives in `ansible/` (base + DevSec hardening + firewall + Caddy +
the garmin service, plus the optional davidlloyd timer when `davidlloyd_enabled: true`). Stage the
linux binaries, then run the playbook:

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o ansible/roles/app/files/garmin ./cmd/garmin
# optional:
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o ansible/roles/app/files/davidlloyd ./cmd/davidlloyd
cd ansible
ansible-galaxy collection install -r requirements.yml -p collections
ansible-vault create group_vars/all/vault.yml   # see group_vars/vault.example.yml
ansible-playbook site.yml             # full provision
ansible-playbook site.yml --tags app  # app-only refresh
```

After the first deploy, link the account on the box (`garmin login`) — the token store is the only
place credentials-derived tokens ever live. Secrets never enter git: the bearer token lives in
ansible-vault, deployed as a 0600 systemd `EnvironmentFile`.
