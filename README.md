# mw-cp — MoonWitness Go PaaS Control Plane

Standalone Go control plane for the MoonWitness PaaS, extracted from `mw-xv/apps/control-plane`.

## Features

- Pure-Go (`CGO_ENABLED=0`) HTTP control plane with embedded SQLite (`modernc.org/sqlite`)
- PaaS deployment orchestration, Git builder, Caddy routing, Coolify-compatible templates, and self-update
- Mesh coordination, A2A protocol, stateless agent runtime, and ISP contract/control plane APIs
- Embedded web UI (`ui/index.html`)

## Quick Start

```bash
CGO_ENABLED=0 go build -o control-plane .
./control-plane
```

## Development & Verification

```bash
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test -count=1 ./...
```

## Docker

```bash
docker build -t ghcr.io/a-l-l-a-h/mw-cp:latest .
docker run --rm -p 3003:3003 ghcr.io/a-l-l-a-h/mw-cp:latest
```
