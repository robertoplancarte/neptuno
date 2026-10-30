# Neptuno

Neptuno is an "environment as code" framework: one CLI and TUI that runs a
multi-repo development environment by driving tools you already use, docker
compose for containers, overmind for processes and tmux for terminals. It aims
to be opinionated enough to get you going fast, and customizable enough to run
just the way you work.

> Status: early. `init`, `up`, `down` and `env use/show` work; `add`, `sync`, `doctor` and the dashboard are on the way.

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

## Starting services

```sh
neptuno up            # pick repos and services from a menu
neptuno up api web    # skip the menu
neptuno up --all -d   # everything, without attaching
cd dockerfiles/api && neptuno up   # same as `neptuno up api`
```

`neptuno up` starts the chosen services plus everything they `depends_on`.
Containers start with `docker compose up --detach`. Each repo with a
`procfiles/<repo>/Procfile` gets an overmind window in the `neptuno-<project>`
tmux session, with every Procfile line wrapped in `docker compose exec <repo>`,
and Neptuno attaches you to that session. `neptuno down [service...]` stops
them again. Run with no arguments inside a repo's folder under `environments/`,
`dockerfiles/`, `procfiles/` or `repos/`, `up`, `down` and `env show` act on
that repo. You need docker, tmux and
[overmind](https://github.com/DarthSim/overmind) installed.

## Environments

Each repo's env is merged from `environments/` in this order, later wins:
`_shared/base.env`, `_shared/<config>.env`, `<repo>/base.env`,
`<repo>/<config>.env`, `<repo>/*.local.env`, then `neptuno up --set KEY=value`.

```sh
neptuno env use staging                  # whole project
neptuno env use api=staging web=local    # per repo
neptuno env show api                     # merged vars and where each came from
```

## Development

```sh
go test ./...
go run ./cmd/neptuno --help
```
