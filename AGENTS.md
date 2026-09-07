# OdooClaw — Developer AGENTS.md

## What this is

**OdooClaw** is an AI assistant for Odoo ERP. Core engine in Go, with Python-based MCP servers, Odoo modules, and a browser extension.

## Repository layout

```
├── odooclaw/              # Go module (github.com/nicolasramos/odooclaw)
│   ├── cmd/odooclaw/main.go       # Entrypoint — cobra CLI
│   ├── cmd/odooclaw/internal/     # Subcommands: agent, gateway, cron, onboard, migrate, etc.
│   ├── pkg/                       # Core packages (agent, bus, channels, providers, mcp, memory, ...)
│   ├── workspace/                 # **Source of truth** for runtime AI personality files
│   │   ├── AGENTS.md, SOUL.md, IDENTITY.md, USER.md   # AI behavior prompts
│   │   └── skills/                # MCP servers (Python): odoo-mcp, whisper-stt, edge-tts
│   ├── browser_copilot/           # Python FastAPI backend for browser extension
│   ├── docker/                    # Docker compose for standalone deployment
│   └── Makefile                   # Build, test, lint, docker targets
├── odoo/custom/src/{16,17,18}.0/mail_bot_odooclaw/  # Odoo module per version
├── browser_extension/             # Chrome/Firefox extension source
├── Dockerfile                     # Docker build (multi-stage: Go build → Node 24 runtime)
├── docker-compose.yml             # Multi-instance Docker Compose (uses instances/)
├── manage.sh                      # Multi-instance lifecycle (build/create/start/stop/logs)
├── config.template.json           # Instance config template
└── instances/                     # Per-instance configs (created by manage.sh)
```

## Essential commands

All Go commands run from `odooclaw/`.

```bash
# Build (must run generate first — it embeds workspace/ into the binary)
make generate   # copies odooclaw/workspace/ → internal/onboard/workspace/ via go:generate
make build      # produces build/odooclaw-{os}-{arch}
make test       # go test ./...
make vet        # go vet ./...
make lint       # golangci-lint run  (not go fmt — project uses golangci-lint)
make fmt        # golangci-lint fmt  (not go fmt)
# Order: generate is a dependency of build, test, vet
```

## Workspace file propagation (critical)

**`odooclaw/workspace/`** is the source of truth for runtime AI prompt files. The onboard command embeds them via `go:generate cp -r ../../../../workspace .` which copies into `cmd/odooclaw/internal/onboard/workspace/`. **Always edit the files in `odooclaw/workspace/`**, then run `make generate` to sync the embedded copy — never edit the onboard/workspace copy directly.

## Go project notes

- `CGO_ENABLED=0` by default — pure Go build, cross-compiles easily
- Uses `golangci-lint` (not `go fmt`) — see Makefile
- Uses `cobra` CLI — add new subcommands in `cmd/odooclaw/internal/{name}/`
- Main packages in `pkg/`: `channels/` (telegram, discord, odoo, whatsapp, etc.), `providers/` (LLM), `mcp/` (MCP client), `memory/` (SQLite + vector), `bus/` (event bus)
- Tests use testify: `go test ./...` from `odooclaw/`

## Odoo module (`mail_bot_odooclaw`)

Three versions maintained: `odoo/custom/src/{16,17,18}.0/mail_bot_odooclaw/`.
- Odoo 18 renamed `mail.channel` → `discuss.channel`; member relationship structure also changed.
- Installed from Odoo Apps (requires `odooclaw.webhook_url` system parameter).
- Each version is independent — changes typically need backporting.

## MCP servers (Python)

Located at `odooclaw/workspace/skills/`:
- `odoo-mcp/` — main Odoo tool server (pip install requirements.txt)
- `whisper-stt/` — speech-to-text (single server.py)
- `edge-tts/` — text-to-speech (single server.py)

Installed inside Docker at `/opt/odoo-mcp` and `/usr/local/bin/*-mcp.py`. The `odoo-mcp` server has its own tests in `odooclaw/workspace/skills/odoo-mcp/tests/`.

## Browser copilot backend

Python FastAPI app in `odooclaw/browser_copilot/`. Requires its own virtual environment:
```bash
python3 -m venv .venv-browser-copilot && source .venv-browser-copilot/bin/activate && pip install -r requirements.txt
```
Start with `docker compose -f odooclaw/browser_copilot/docker-compose.browser-copilot.yml up --build`.
Smoke test: `./odooclaw/browser_copilot/scripts/smoke_test.sh`

## Docker

- Root `Dockerfile` builds the full image (Go → Node 24 runtime with Python MCP servers)
- `odooclaw/docker/docker-compose.{yml,full.yml}` for standalone deployment
- Root `docker-compose.yml` + `manage.sh` for multi-instance management
- `ODOOCLAW_HOME` defaults to `/home/odooclaw/.odooclaw/`
- Default mode: `odooclaw gateway` (webhook listener on port 18790)
- One-shot mode: `odooclaw agent -m "query"`

## Instance management (`manage.sh`)

```bash
./manage.sh build                    # Build Docker image (defaults linux/amd64 on macOS)
./manage.sh create <name>            # Create new instance dir + config
./manage.sh start <name>             # Start Docker container
./manage.sh stop <name>              # Stop container
./manage.sh logs <name>              # Follow logs
./manage.sh list                     # List all instances with status
```

Instances are created under `instances/<name>/` with `.env` and `config.json`.

## Config model

Layer: environment vars → `config.json`. Env vars take precedence when both exist.
Default LLM provider is Google Gemini (`google/gemini-2.5-pro`). See `config.template.json` for full structure.

## Key docs

- `odooclaw/docs/README.md` — Documentation index
- `odooclaw/workspace/SOUL.md` — AI response format rules (mandatory), Odoo 18 field reference, MCP auto-extension protocol
- `odooclaw/workspace/AGENTS.md` — AI behavioral directives (runtime)
