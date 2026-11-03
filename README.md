# Neptuno

Neptuno is an "environment as code" framework: one CLI and TUI that runs a
multi-repo development environment by driving tools you already use, docker
compose for containers, overmind for processes and tmux for terminals. It aims
to be opinionated enough to get you going fast, and customizable enough to run
just the way you work.

> Status: early. `init`, `add`, `up`, `down`, `claude`, `mcp`, `env use/show` and the dashboard work; `sync` and `doctor` are on the way.

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

In a terminal, `neptuno init` first shows a checklist of the default shared
services, all checked: uncheck the ones the project doesn't need. Services a
chosen one needs come along. Scripts pass
`--services` to skip the checklist:

```sh
neptuno init acme --services postgres,valkey
neptuno init acme --services=        # only the Caddy proxy
```

Run `neptuno init` again inside the project (or `neptuno init acme` next to
it) to add default services you left out. It lists only the ones the project
doesn't have, appends the picked ones to `compose.yml`, the `Caddyfile` and
`environments/_shared/base.env`, and leaves existing services and your edits
alone. `neptuno init --services s3` does the same without the checklist.

`neptuno init <name>` creates:

```
acme/
├── compose.yml      # yours to edit: proxy, postgres, valkey, s3
├── Caddyfile        # yours to edit: one http://<repo>.localhost block per repo
├── neptuno.yml      # optional project config
├── AGENTS.md        # tells coding agents in this folder how the project works
├── CLAUDE.md        # @AGENTS.md, so Claude Code reads the same
├── .claude/skills/  # step-by-step guides agents load when a task needs them
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
optional `generate` command, extra `volumes`, optional `extends`), a `Dockerfile`, an optional
`entrypoint.sh`, a `Procfile`, a `base.env` and an optional `app/` of starter files, all Go templates with
`{{.Name}}`, `{{.Project}}` and `{{.Port}}`. Put your own in `stacks/<name>/` in
the project to add stacks or override the built-in ones.

### Subdomains per tenant

```sh
neptuno add shop --stack rails --wildcard   # new repo
neptuno add shop --wildcard                 # turn it on for a repo you already added
```

`--wildcard` makes the repo's Caddy site `http://shop.localhost, http://*.shop.localhost`,
so `acme.shop.localhost` and any other subdomain reach the same container with
the `Host` header intact. On an existing repo it only widens the site, and if
the proxy is running Caddy reloads it for you (otherwise run
`docker compose restart proxy`). Rails needs no change: development already
allows `.localhost` hosts, and `request.subdomain` returns `acme`. Chrome and
Firefox resolve every `*.localhost` name to 127.0.0.1; Safari and `curl` on
some systems don't, so add `127.0.0.1 acme.shop.localhost` to `/etc/hosts`
for those.

### Installed packages live in volumes

The `rails` stack keeps installed gems in a `<repo>_bundle` named volume at
`/usr/local/bundle`, and the `node` stack keeps `node_modules` in a
`<repo>_node_modules` volume; `neptuno add` mounts them and declares them in
`compose.yml`. They survive rebuilt and recreated containers, so the Rails
entrypoint's `bundle check || bundle install` only installs when
`Gemfile.lock` changes. Named volumes live inside Docker's VM, so they run at
native speed on a Mac. To start one over, say for a clean `bundle install`,
select it in the dashboard's Volumes list and press `enter`, or run `neptuno volumes reset <repo>_bundle` (see
[Volumes](#volumes)).

### Stack options

Some stacks offer options, which `neptuno add` shows as a checklist (or set
them with `--with` and `--without`). The `rails` stack has:

| Option | Default | What it does |
| --- | --- | --- |
| `tailwind` | on | `rails new --css=tailwind` and a `css: bin/rails tailwindcss:watch` process |
| `action_text` | off | runs `bin/rails action_text:install` |
| `storage_s3` | off | stores Active Storage uploads in the project's SeaweedFS: adds `aws-sdk-s3`, a `seaweedfs` service in `config/storage.yml`, the S3 env vars, and creates the bucket at container start |

```sh
neptuno add shop --stack rails --with action_text,storage_s3 --without tailwind
```

Options live in `stack.yml`. Each can add generator flags, commands run right
after the generator, Procfile lines, `base.env` lines and shared services, and
templates can test `{{if .Options.tailwind}}`. A project stack that `extends:`
another can add options or replace one by name:

```yaml
# stacks/rails/stack.yml
extends: rails
options:
  sidekiq:
    description: Sidekiq on Valkey
    after_generate: bundle add sidekiq
    procfile: ["jobs: bundle exec sidekiq"]
    env: ["REDIS_URL=redis://valkey:6379/0"]
    services: [valkey]
```

### Customizing a stack

A stack with `extends:` inherits every setting and file it doesn't define, so
an override only holds what changes. For example, Rails without Minitest, for
teams on RSpec:

```yaml
# stacks/rails/stack.yml  (extends the built-in rails it overrides)
extends: rails
generate: >-
  gem install rails -v '~> 8.1' --no-document &&
  rails new . --name={{.Name}} --database=postgresql
  --skip-git --skip-docker --skip-kamal --skip-thruster --skip-test
```

Its options, Dockerfile, entrypoint.sh, Procfile and base.env still come from
the built-in stack. To start from a full copy instead, run `neptuno stacks copy
rails` (or `neptuno stacks copy rails rails-rspec` for a new stack next to the
original).

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
tmux session, with every Procfile line run in the repo's container with
`docker compose exec <repo>` (through `neptuno container-exec`, see below),
and Neptuno attaches you to that session. `neptuno down [service...]` stops
them again. Run with no arguments inside a repo's folder under `environments/`,
`dockerfiles/`, `procfiles/` or `repos/`, `up`, `down` and `env show` act on
that repo. You need docker, tmux and
[overmind](https://github.com/DarthSim/overmind) installed.

Overmind runs in the background with `--any-can-die`, so a process that
crashes or exits leaves the rest running, and it gives every process its own
tmux window. A repo's window shows those per-process windows; overmind's tmux
is nested inside Neptuno's, so press the prefix twice to reach it (`Ctrl-b
Ctrl-b n` for the next process, `Ctrl-b Ctrl-b d` to detach). Detaching or
closing the repo's window leaves its processes running, and the next
`neptuno up` reconnects to them. When a process exits or you stop it with
Ctrl-C, its window turns into a shell on the host, in the project folder,
with a note on how to run it again: `neptuno restart <repo>:<process>`
(`neptuno restart <repo>` restarts all of a repo's processes). `exit` or
Ctrl-D in that shell closes the window, and `neptuno restart` reopens it. Your own
`~/.tmux.conf` still applies inside overmind's session.

### Processes left running in a container

`docker compose exec` doesn't stop what it started when its client goes away,
so a Procfile line that overmind stopped or lost could keep running in the
container with no owner, holding its port. Neptuno prevents that in two ways:

- Each line runs through `neptuno container-exec`, which starts the command
  with `docker compose exec`, TTY and all, plus a quiet second exec that
  watches it. Stopping or restarting the line forwards the signal to the
  command's process group in the container, and if the line goes away any
  other way (overmind or its tmux killed, the wrapper itself killed), the
  watcher stops the group: TERM, then KILL five seconds later.
- Every command records itself under `/dev/shm/neptuno-procs/<run>/`, where
  the run is a fresh ID for each overmind Neptuno starts. Before `neptuno up`
  starts or reconnects to a repo's processes, and before `neptuno restart`,
  Neptuno stops whatever is recorded under any other run and says so.

Both only signal process groups they recorded, and check that the recorded
process is still the same one, so PID 1, the entrypoint and anything you start
yourself with `docker compose exec` are never touched.

What Neptuno can't clean up safely, it explains. When a process exits because
its port was taken (`Address already in use`) or Rails says `A server is
already running`, `neptuno status` and the dashboard say which process holds it
and whether an earlier run, this run or something started by hand owns it, or
that the pid file is stale. They also warn about overmind daemons on your
machine that shouldn't be there: one for a repo that is no longer in the
project, or two for the same repo. `neptuno down <repo>` stops those.

### Setup at container start

Setup that has to finish before any process starts, such as `bundle install`
and `bin/rails db:prepare` in the `rails` stack, goes in
`dockerfiles/<repo>/entrypoint.sh`, which the Dockerfile sets as its
`ENTRYPOINT`. It runs each time the container starts. When a repo has an
`entrypoint.sh`, its processes wait until the script touches
`/dev/shm/neptuno-ready`, or stop with an error if it touches
`/dev/shm/neptuno-failed`; its output is in `docker compose logs <repo>`. If
setup fails, each process window shows the end of its output and how to retry.
The script ends with `exec "$@"` so the container goes on to run its command.

The `rails` entrypoint runs `bin/rails db:prepare`, so the database keeps its
data between starts. To start from a fresh one every time, put
`NEPTUNO_RESET_DB=1` in a git-ignored `environments/<repo>/*.local.env` (for
example `environments/shop/me.local.env`), and every container start drops the
database, migrates and seeds it. For a one-off reset, run
`neptuno up shop --set NEPTUNO_RESET_DB=1`. When `neptuno add` generates a
new app over a database an earlier app of the same name left, the first start
renames the old one to `<name>_development_old_<time>` instead of failing on
tables that already exist.
Rebuild the image (`docker compose build <repo>`) after changing it.

## Dashboard

Run `neptuno` with no arguments inside a project to open the dashboard. It
runs in the first window of the project's `neptuno-<project>` tmux session,
named `neptuno`: Neptuno creates the session if there is none, puts the
dashboard window back if it was closed, selects it and attaches. Inside tmux
it switches your client to the session instead of nesting one. Without tmux,
or with `neptuno dashboard`, the dashboard runs in the current terminal. It
lists every repo with its state, how many of its overmind processes are
running, its branch, the commit it is at (`*` when the work tree has
uncommitted changes), that commit's date and its envs, with the active one in
brackets (`[local] staging production`), and every other compose service
with its container state and published ports. The row under the cursor shows
more: the commit's subject and author, each process and its state, the
container's health and what it depends on. It refreshes every two seconds,
and loads the project again when `neptuno.yml`, `compose.yml` or `repos/`
change, so a repo added from the dashboard or the shell shows up without
restarting it.

The screen is split into panels: the project, the repos and the services
stacked on the left with the volumes under them, the focused list's border highlighted, and on the
right a panel for the row under the cursor with four tabs. **Info** has the
details above plus the URLs the row answers on through the proxy, read from
the Caddyfile's sites; terminals with OSC 8 hyperlinks make them clickable
(in tmux, set `terminal-features` to include `hyperlinks`). **Env** has the repo's resolved variables and the file each
came from (as `neptuno env show` prints them), and **Logs** follows the
container's output (`docker compose logs`); a `run: host` repo's output is in
its window. **Actions** lists everything you can do to the row under the
cursor, each with its key, and says why one can't run right now (a stopped
repo has no window to open, say); press `enter` there to pick one with the
arrows, or press its key from anywhere as before. Volumes get the Actions tab
too. The keys run along the bottom. In a terminal under 70×14 it falls back
to a plain list.

| Key | Does |
| --- | --- |
| `↑` `↓` / `j` `k` | move |
| `←` `→` / `h` `l` / `tab` | jump between the repos, services and volumes lists |
| `[` `]` | switch the right panel's tab |
| `PgUp` `PgDn` / `ctrl+u` `ctrl+d` | scroll the right panel |
| `space` | select rows; `a` selects all |
| `enter` | open the repo's window |
| `u` | start the selected rows, or the one under the cursor (`neptuno up -d`) |
| `s` | stop them (`neptuno down`) |
| `r` | restart a repo's processes |
| `e` | switch a repo's env, then restart it if it is running |
| `c` | open Claude Code in a repo's container |
| `m` | copy a repo's MCP URLs, or add its servers to Claude Code |
| `n` | add a repo (see below) |
| `q` | quit |

A repo's envs are the `<name>.env` files in `environments/_shared` and
`environments/<repo>` (not `base.env` or `*.local.env`), plus the default.
Picking one with `e` runs `neptuno env use <repo>=<env>`; if the repo is
running, `neptuno up` then recreates its container with the new env and
restarts its processes (a `run: host` repo gets `neptuno restart`).

`u`, `s`, `r` and `e` run in the background, so the dashboard stays up. Each
command's output goes to the Logs tab of every service it acts on, which the
right panel switches to when that includes the row under the cursor: below
the container's output while it runs or after it fails, above it once it
succeeds. The bottom line says when it is done or failed, and a service
takes one command at a time. Starting or restarting a service (`u`, `r`, or `e` on a
running repo) clears its Logs tab, which then shows only that run's output;
stopping it keeps the last run's logs until it starts again.

### Adding a repo

`n` opens a form in the right panel that does what `neptuno add` does: type
the repo's name, optionally a git URL to clone (leave it blank to start a new
app), pick a stack with `←` `→`, and tick the stack's options with `space`;
they start at the stack's defaults, and the form shows the
`neptuno add <name> [git-url] --stack ... --with ... --without ...` command it
will run. `wildcard` adds `--wildcard`. `↑` `↓` or `tab` move between fields,
`enter` on **Add repo** (or `ctrl+s` anywhere) runs it in the background, and
`esc` cancels. The form shows the command's output while it runs; once it
succeeds the cursor moves to the new repo with its Logs tab open, and if it
fails the output stays up until `esc`. Cloning over SSH needs a key that
doesn't prompt for a passphrase (an agent works), since the command has no
terminal to ask in.

### Volumes

The **Volumes** list, under the services, has the named volumes in
`compose.yml` (as `docker compose config` resolves them), whether docker has
created each and the services that mount it; a service's Info tab names the
ones it mounts. With the cursor on a volume, the right panel says what it
holds (a database, installed gems, npm packages and so on), where each
service mounts it and whether that service is up, docker's driver, creation
time and host path, and how much space it takes and its top-level files and
folders. To see inside, Neptuno runs `du` and `ls` in a running container
that mounts it, or else in a throwaway `busybox` container that mounts it
read-only (docker pulls the image the first time). It looks once when you
select the volume; `f` looks again.

`enter` clears the volume after you confirm with `y`, since its data is gone
for good: Neptuno stops the services that mount it and removes their
containers, which hold on to it, deletes it, then starts again the ones that
were running, which recreates it empty. Clearing the postgres volume gives
postgres a new, empty database, and a Rails app's `<repo>_bundle` volume gets
its gems reinstalled by the entrypoint. It runs in the background like `u`,
and its output goes to the Logs tab of those services and to the volume's
panel. External volumes are left alone.

From the shell, `neptuno volumes` prints the list and
`neptuno volumes reset <volume>...` does the same clear, asking first unless
you pass `--yes`.

`neptuno up` makes the dashboard the first window of the session too, so
attaching lands on it. Quitting it closes that window, and the next `neptuno`
or `neptuno up` brings it back. `neptuno status` prints
the same table once, for scripts and terminals without a TUI.

## Claude Code in your containers

Every built-in stack installs [Claude Code](https://claude.com/claude-code) in
its dev image. Run it inside a repo's container, next to the code, tools and
services the app really uses:

```sh
neptuno claude api                 # opens a claude tab in api's window, next to its processes
neptuno claude api -- --continue   # arguments after -- go to claude
cd repos/api && neptuno claude     # the repo defaults to the folder you're in
```

It starts the repo if needed. Claude gets a `claude` tab among the repo's
process tabs (`Ctrl-b Ctrl-b n` cycles through them), and quitting it closes
the tab and leaves the app running. `neptuno down <repo>` closes it with the
processes. For a repo whose processes an older Neptuno started, it opens a
`claude-<repo>` window instead until you run `neptuno down` and `neptuno up`
for that repo. Sign-in and settings live in `.neptuno/claude`,
mounted at `/root/.claude` in every repo `neptuno add` creates, so you sign in
once per project and it survives rebuilds. For services added before this, add
`"./.neptuno/claude:/root/.claude"` to their `volumes` (or set
`CLAUDE_CODE_OAUTH_TOKEN` in `environments/_shared/*.local.env`), and rebuild
the image with `docker compose build <repo>` after adding Claude Code to its
Dockerfile.

## MCP servers your repos serve

When a repo runs an MCP server, declare it in `neptuno.yml` with the path it
answers on. Neptuno finds the host and port in the Caddyfile:

```yaml
repos:
  locke:
    mcp:
      hyperdrive: /_hyperdrive/mcp
```

The server is reached at a different address depending on where Claude runs,
and Neptuno hands each Claude the one that works there:

| Claude runs | It gets | Address |
| --- | --- | --- |
| In the project folder, in the terminal or the Claude desktop app's Code tab | `.mcp.json` in the project root | `http://locke.localhost/_hyperdrive/mcp`, through Caddy |
| In a repo's container (`neptuno claude <repo>`) | `--mcp-config /root/.claude/neptuno-mcp.json` | `http://locke:3000/_hyperdrive/mcp`, on the compose network |

Inside a container `locke.localhost` points back at that container, so the
Caddy address does not work there. `neptuno up` rewrites both files each time
it runs, and `neptuno mcp sync` does it without starting anything. Servers
you add to `.mcp.json` yourself are kept, and Claude asks you to approve
the project's servers the first time it starts in the folder.
`neptuno mcp` lists every declared server with both addresses. The
dashboard's **Info** tab shows them for the repo under the cursor, and
whether each one answers. Repos that serve one have `(mcp)` after their
names, and the project panel shows whether `.mcp.json` is current and
whether Claude desktop has the project. `m` opens a menu to copy a server's
URLs or its `claude mcp add` command, or to add it to Claude Code for every
folder (`--scope user`). A repo's own `.mcp.json` in
`repos/<repo>` is still read by Claude in its container, so keep
`*.localhost` addresses out of it.

A Claude session in the project folder sees every repo. It can run tools in
a container with `docker compose exec -T <repo> ...`, and hand a task to the
Claude inside a repo's container with
`docker compose exec -T <repo> claude -p "<task>" --mcp-config /root/.claude/neptuno-mcp.json`.

### Instructions for coding agents

`neptuno init` writes files that tell coding agents started in the project
folder how the project works, in formats more than one tool reads:

| File | Read by | Holds |
| --- | --- | --- |
| `AGENTS.md` | Cursor, Codex, Copilot and other tools that follow the AGENTS.md convention | the layout, the commands, working inside containers, MCP servers |
| `CLAUDE.md` | Claude Code | `@AGENTS.md`, which imports it, plus any notes only for Claude |
| `.claude/skills/<name>/SKILL.md` | Claude Code, and other tools that support Agent Skills | `neptuno-add-repo`, `neptuno-debug-service` and `neptuno-delegate-to-repo`: step-by-step guides loaded only when a task needs one |

They are yours to edit: add your team's conventions to `AGENTS.md` and your
own skills next to Neptuno's. `neptuno mcp sync` writes any that are
missing into older projects and never touches the ones you have. Each repo
keeps its own `AGENTS.md` or `CLAUDE.md` in `repos/<repo>` for its code.

### Claude desktop chat

Claude desktop's own connectors are reached from Anthropic's servers, so
they can't see `*.localhost`. What it can run is a local server, so Neptuno
provides one:

```sh
neptuno mcp desktop            # add this project to Claude desktop, then restart it
neptuno mcp desktop --remove   # take it out again
```

This adds `neptuno mcp serve --project <folder>` to
`claude_desktop_config.json` as `neptuno-<project>`, along with your `PATH`,
because apps started from the dock don't get your shell's `PATH` and so
couldn't find docker. The server talks MCP over stdio and has two kinds of
tools:

| Tool | Does |
| --- | --- |
| `neptuno_status` | what `neptuno status` prints |
| `neptuno_up`, `neptuno_down`, `neptuno_restart` | start, stop and restart repos and services |
| `neptuno_exec` | run a shell command in a repo's container (or in `repos/<repo>` for `run: host`) |
| `neptuno_logs` | the end of a container's output |
| `neptuno_ask_repo_claude` | hand a task to Claude Code in a repo's container and return its answer |
| `neptuno_mcp_servers` | the declared servers and whether they are reachable |
| `<repo>-<server>__<tool>` | every tool of every declared server, forwarded |

A server whose repo isn't running yet is tried again every 15 seconds and on
each call, and its tools show up once it answers. Only tools are forwarded,
not resources or prompts. Claude desktop asks before it runs a tool, and
`neptuno_exec` can run anything in your containers, so read what it is about
to run. Any other MCP client that starts local servers can run
`neptuno mcp serve` the same way.

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
