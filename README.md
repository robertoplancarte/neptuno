# Neptuno

Neptuno is an "environment as code" framework: one CLI and TUI that runs a
multi-repo development environment by driving tools you already use, docker
compose for containers, overmind for processes and tmux for terminals. It aims
to be opinionated enough to get you going fast, and customizable enough to run
just the way you work.

> Status: early. `neptuno init` works; the other commands are on the way.

## Install

```sh
go install github.com/robertoplancarte/neptuno/cmd/neptuno@latest
```

## Quick start

```sh
neptuno init acme
cd acme
docker compose up -d   # Caddy, PostgreSQL 17, Valkey 8, SeaweedFS
```

`neptuno init <name>` creates:

```
acme/
├── compose.yml      # yours to edit: proxy, postgres, valkey, s3
├── Caddyfile        # yours to edit: one http://<repo>.localhost block per repo
├── neptuno.yml      # optional project config
├── environments/
│   └── _shared/     # base.env and local.env every repo gets
├── dockerfiles/     # dockerfiles/<repo>/Dockerfile
├── procfiles/       # procfiles/<repo>/Procfile
└── repos/           # repos/<repo>, as git submodules
```

| Service | Image | Reachable at |
| --- | --- | --- |
| Reverse proxy | `caddy:2` | `localhost:80`, `<repo>.localhost` |
| PostgreSQL | `postgres:17` | `localhost:5432` |
| Valkey | `valkey/valkey:8` | `localhost:6379` |
| Object storage | `chrislusf/seaweedfs` | `localhost:8333`, `s3.localhost` |

## Development

```sh
go test ./...
go run ./cmd/neptuno --help
```
