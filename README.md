# Neptuno

Neptuno is an "environment as code" framework: one CLI and TUI that runs a
multi-repo development environment by driving tools you already use, docker
compose for containers, overmind for processes and tmux for terminals. It aims
to be opinionated enough to get you going fast, and customizable enough to run
just the way you work.

> Status: early. `init`, `add`, `up`, `down`, `claude` and `env use/show` work; `sync`, `doctor` and the dashboard are on the way.

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

## Adding repos

```sh
neptuno add api git@github.com:acme/api.git --stack rails   # clone an existing repo
neptuno add shop --stack rails                               # start a brand-new app
neptuno add worker                                           # blank repo, bring your own toolchain
neptuno stacks                                               # list stacks
```

`neptuno add` writes `dockerfiles/<repo>/Dockerfile`, `procfiles/<repo>/Procfile`
and `environments/<repo>/{base,local}.env`, appends a service to `compose.yml`
and an `http://<repo>.localhost` site to the `Caddyfile`. With a git URL the
repo is cloned into `repos/<repo>` as a submodule. Without one, a new git repo
is created from the stack: `node`, `go` and `python` copy in a small runnable
app, and `rails` runs `rails new` inside the new container, so you don't need
Ruby installed. Existing files are never overwritten, so `add` can be rerun.

Stacks are folders with a `stack.yml` (description, port, shared services,
optional `generate` command, optional `extends`), a `Dockerfile`, a `Procfile`,
a `base.env` and an optional `app/` of starter files, all Go templates with
`{{.Name}}`, `{{.Project}}` and `{{.Port}}`. Put your own in `stacks/<name>/` in
the project to add stacks or override the built-in ones.

### Customizing a stack

A stack with `extends:` inherits every setting and file it doesn't define, so
an override only holds what changes. For example, Rails with Tailwind:

```yaml
# stacks/rails/stack.yml  (extends the built-in rails it overrides)
extends: rails
description: Rails 8 with Tailwind
generate: >-
  gem install rails -v '~> 8.0' --no-document &&
  rails new . --name={{.Name}} --database=postgresql --css=tailwind
  --skip-git --skip-docker --skip-kamal --skip-thruster
```

```
# stacks/rails/Procfile
web: bundle install && bin/rails db:prepare && rm -f tmp/pids/server.pid && bin/rails server -b 0.0.0.0 -p {{.Port}}
css: until bundle check >/dev/null 2>&1; do sleep 2; done; bin/rails tailwindcss:watch
```

The Dockerfile and base.env still come from the built-in stack. To start from a
full copy instead, run `neptuno stacks copy rails` (or `neptuno stacks copy
rails rails-tailwind` for a new stack next to the original).

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

## Claude Code in your containers

Every built-in stack installs [Claude Code](https://claude.com/claude-code) in
its dev image. Run it inside a repo's container, next to the code, tools and
services the app really uses:

```sh
neptuno claude api                 # opens a claude-api window in the tmux session
neptuno claude api -- --continue   # arguments after -- go to claude
cd repos/api && neptuno claude     # the repo defaults to the folder you're in
```

It starts the container if needed and runs outside overmind, so quitting
Claude never stops the app. Sign-in and settings live in `.neptuno/claude`,
mounted at `/root/.claude` in every repo `neptuno add` creates, so you sign in
once per project and it survives rebuilds. For services added before this, add
`"./.neptuno/claude:/root/.claude"` to their `volumes` (or set
`CLAUDE_CODE_OAUTH_TOKEN` in `environments/_shared/*.local.env`), and rebuild
the image with `docker compose build <repo>` after adding Claude Code to its
Dockerfile.

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
