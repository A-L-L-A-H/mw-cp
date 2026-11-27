# mw-cp — Go control plane for MoonWitness PaaS deployment and mesh operations.

**Repository:** [`A-L-L-A-H/mw-cp`](https://github.com/A-L-L-A-H/mw-cp) · **Stack:** Go 1.24 · CGO-free SQLite

## What it does

Provides deployment orchestration, Git builds, Caddy routing, A2A/mesh coordination, ISP controls, and an embedded UI.

## Quick start

```bash
CGO_ENABLED=0 go build -o control-plane .
./control-plane
```

## Verify

```bash
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test -count=1 ./...
```

## Configuration

- **Port:** 3003
- **Key configuration:** MW_CONTROL_PLANE_ADDR

```bash
docker build -t ghcr.io/a-l-l-a-h/mw-cp:latest .
docker run --rm -p 3003:3003 ghcr.io/a-l-l-a-h/mw-cp:latest
```

## Notes

The control plane port is private by default; expose it only through the mesh or an authenticated edge.
