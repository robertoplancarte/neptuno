# Neptuno

Neptuno is an "environment as code" framework: one CLI and TUI that runs a
multi-repo development environment by driving tools you already use, docker
compose for containers, overmind for processes and tmux for terminals. It aims
to be opinionated enough to get you going fast, and customizable enough to run
just the way you work.

> Status: early. `init`, `add`, `up`, `down`, `exec`, `claude`, `mcp`, `env use/show` and the dashboard work; `sync` and `doctor` are on the way.

## Install

```sh
go install github.com/robertoplancarte/neptuno/cmd/neptuno@latest
```

## Quick start

```sh
neptuno init acme
cd acme
docker compose up -d   # Caddy, PostgreSQL 18, Valkey 8, SeaweedFS, Mailpit, Hook0
```

In a terminal, `neptuno init` first shows a checklist of the shared
services, all checked but the opt-in Playwright: uncheck the ones the project
doesn't need. Services a
chosen one needs come along (Hook0 brings PostgreSQL and Mailpit). Scripts pass
`--services` to skip the checklist:

```sh
neptuno init acme --services postgres,valkey
neptuno init acme --services=        # only the Caddy proxy
```

Run `neptuno init` again inside the project (or `neptuno init acme` next to
it) to add default services you left out. It lists only the ones the project
doesn't have, appends the picked ones to `compose.yml`, the `Caddyfile` and
`environments/_shared/base.env`, and leaves existing services and your edits
alone. `neptuno init --services hook0` does the same without the checklist.

`neptuno init <name>` creates:

```
acme/
├── compose.yml      # yours to edit: proxy, postgres, valkey, s3, mailpit, hook0
├── Caddyfile        # yours to edit: one http://<repo>.localhost block per repo
├── neptuno.yml      # optional project config
├── AGENTS.md        # tells coding agents in this folder how the project works
├── CLAUDE.md        # @AGENTS.md, so Claude Code reads the same
├── .claude/
│   ├── settings.json  # Claude Code may edit repos/ and run neptuno exec without asking
│   ├── agents/        # a subagent per repo, written by neptuno add
│   └── skills/        # step-by-step guides agents load when a task needs them
├── environments/
│   └── _shared/     # base.env and local.env every repo gets
├── dockerfiles/     # dockerfiles/<repo>/Dockerfile
├── procfiles/       # procfiles/<repo>/Procfile
└── repos/           # repos/<repo>, as git submodules
```

| Service | Image | Reachable at |
| --- | --- | --- |
| Reverse proxy | `caddy:2` | `localhost:80`, `<repo>.localhost` |
| PostgreSQL | `postgres:18` | `localhost:5432` |
| Valkey | `valkey/valkey:8` | `localhost:6379` |
| Object storage | `chrislusf/seaweedfs` | `localhost:8333`, `s3.localhost` |
| Mail catcher | `axllent/mailpit:v1` | SMTP `localhost:1025`, inbox `localhost:8025`, `mailpit.localhost` |
| Webhooks | `hook0/hook0-api`, `hook0/hook0-output-worker`, `hook0/hook0-frontend` | API `localhost:8081`, dashboard `hook0.localhost` |
| Browsers (opt-in) | `mcr.microsoft.com/playwright` | tests `ws://playwright:3000/`, MCP `playwright.localhost/mcp` |

[Hook0](https://www.hook0.com) keeps its tables in their own schemas of the
shared `postgres` database, which is why that service is PostgreSQL 18: Hook0
needs 18 or later. A one-off `hook0-setup` container creates a `neptuno`
organization and application, and every repo gets `HOOK0_API_URL`
(`http://hook0:8081/api/v1`), `HOOK0_API_TOKEN` (the development master key set
in `compose.yml`), `HOOK0_ORGANIZATION_ID` and `HOOK0_APPLICATION_ID`, so it can
create event types and subscriptions and send events right away. The worker may
deliver to private addresses, so endpoints such as `http://api:3000/webhooks`
reach the repos' containers.

Mailpit keeps every mail sent to it and shows it at `http://mailpit.localhost`;
repos get `SMTP_HOST=mailpit` and `SMTP_PORT=1025`. Hook0 sends its mail
there too. To use the Hook0 dashboard, sign up at `http://hook0.localhost`,
open the confirmation mail in Mailpit, and log in: everyone who signs up joins
the `neptuno` organization, so the dashboard shows the repos' event types,
subscriptions, events and deliveries.

Projects made before PostgreSQL 18 keep `postgres:17` until you change it.
PostgreSQL 18 can't open a 17 data directory and mounts its volume at
`/var/lib/postgresql` instead of `/var/lib/postgresql/data`, so dump your
databases, move to the new image and mount, and restore them before adding
Hook0 to such a project.

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
| `mailpit` | on | Action Mailer in development delivers over SMTP to the project's Mailpit (`SMTP_HOST`, `SMTP_PORT`), so mail shows at `http://mailpit.localhost`; mail links point at `http://<name>.localhost` |
| `action_text` | off | runs `bin/rails action_text:install` |
| `storage_s3` | off | stores Active Storage uploads in the project's SeaweedFS: adds `aws-sdk-s3`, a `seaweedfs` service in `config/storage.yml`, the S3 env vars, and creates the bucket at container start |
| `playwright` | off | system tests drive the project's shared Playwright browsers: adds `capybara-playwright-driver` and a `test/application_system_test_case.rb` that connects to them, and adds the `playwright` service if the project lacks it (see [Browser testing with Playwright](#browser-testing-with-playwright)) |

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

### Browser testing with Playwright

```sh
neptuno add shop --stack rails --with playwright
neptuno exec shop -- bin/rails test:system
```

Browsers are big, so instead of putting them in every repo's image Neptuno
runs them once, in an opt-in `playwright` service on the
`mcr.microsoft.com/playwright` image. `neptuno add` with the `playwright`
option adds it to `compose.yml`, the `Caddyfile` and
`environments/_shared/base.env` when the project lacks it, as
`neptuno init --services playwright` does. The container runs two servers:

- `playwright run-server` at `ws://playwright:3000/`, which test suites
  connect to. Every repo gets `PLAYWRIGHT_SERVER_URL`. A client must match
  its Playwright version (1.65), so the Rails option pins
  `playwright-ruby-client` to `~> 1.65.0`; a Node repo would use
  `playwright@1.65` and `chromium.connect(process.env.PLAYWRIGHT_SERVER_URL)`.
- [Playwright MCP](https://github.com/microsoft/playwright-mcp) at
  `http://playwright.localhost/mcp`, a headless Chromium that Claude Code
  drives to click through a repo and check its own work. `neptuno up` and
  `neptuno mcp sync` add it to `.mcp.json` as `playwright` whenever
  `compose.yml` has the service, without anything in `neptuno.yml`.

Inside the container every `*.localhost` name reaches the proxy: browsers
send those names to the container's own loopback, and a small forwarder
there passes port 80 on to Caddy with the `Host` header intact. So the MCP
browser opens `http://shop.localhost` and `http://acme.shop.localhost` as
yours do. Use URLs on port 80 there, even if you publish the proxy on
another port.

The Rails option writes `test/application_system_test_case.rb` so system
tests use Chromium in that container. The test's Puma listens on every
interface of the repo's container, and the browser reaches it by the repo's
compose name, which the option puts in `SYSTEM_TESTS_HOST`. The repo
`depends_on` the service, so `neptuno up shop` and `neptuno exec shop`
start it too. Screenshots of
failures land in `repos/<repo>/tmp/screenshots` as usual. For a Rails repo
you already have, run `neptuno init --services playwright`, add the two
gems to its test group, and copy that file from a new app made with the
option.

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

While `neptuno up` gets containers going, from the dashboard or a shell, each
service it starts shows what is happening instead of `stopped`: `pulling`
(with layers done, such as `pulling 3/7`, when one image pulls alone),
`building` (with the Dockerfile step, such as `building 4/9`), `waiting` for
the other images, then `creating` and `starting`, next to a spinner. The row
under the cursor shows how long the phase has run, the build step's
instruction and the last line it printed, and says so when a step such as a
compile has printed nothing for a while. `up` reads this from docker
compose's output, so compose prints it plainly rather than redrawing it in
place.

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
| `c` | move to Claude Code, in the pane below the dashboard |
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

In a project with no repos yet, the dashboard opens on the empty Repos list,
whose one line is **+ Add a repo**: `enter` or `n` there opens the form, and
`↓` or `→` moves on to the services.

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

## Claude Code

Claude Code runs on your machine, in the project folder, in the pane below
the dashboard: the dashboard window opens it when [Claude
Code](https://claude.com/claude-code) is installed. One session sees every
repo. It edits the code in `repos/` directly and runs each repo's own tools
in that repo's container, so nothing about Claude lives in the images.

```sh
neptuno claude                 # select the Claude pane, opening it (and the dashboard) if needed
neptuno claude -- --continue   # arguments after -- go to claude when the pane starts
```

`c` in the dashboard does the same.

### Running a repo's tools: `neptuno exec`

```sh
neptuno exec api -- bin/rails test
neptuno exec api 'bin/rails db:migrate && bin/rails test'   # one argument runs with sh -c
neptuno exec postgres -- psql -U postgres                   # any compose service
cd repos/api/engines/billing && neptuno exec -- bin/rails test
```

`neptuno exec <repo> -- <command>` runs the command in the repo's container
with `docker compose exec`, so it gets the repo's Ruby, Node or Python, its
packages and its env, and reaches the shared services by name. It starts the
container first, as `neptuno up --detach` does, when it isn't running. Run
from a subfolder of `repos/<repo>`, the command runs in the same folder of
the container (found from the repo's mount in `compose.yml`), and inside a
repo's folder the repo can be left out. It gets a terminal when you have one
and plain output when piped, and exits with the command's status. A `run:
host` repo runs the command in `repos/<repo>` on your machine, with the
repo's env.

### A subagent per repo

Every repo gets a Claude Code [subagent](https://code.claude.com/docs/en/sub-agents)
in `.claude/agents/<repo>.md`, named after it. It changes files in its own
repo only, runs commands with `neptuno exec <repo>`, and ends with a report
of what it changed and checked. The main session hands it work inside one
repo, and splits work across repos into one task per repo that run in
parallel, then checks the results. The `neptuno-delegate-to-repo` skill
spells this out. `neptuno add`, `neptuno up` and `neptuno mcp sync` write a
missing one and never overwrite one you edited, so add a repo's own
conventions to it.

### Permissions

`neptuno init` writes `.claude/settings.json`, which lets Claude edit files
under `repos/` and run `neptuno exec`, `neptuno status`, `neptuno up -d`,
`neptuno restart`, `docker compose ps` and `logs`, and `git -C repos/...`
without asking, and never edit `.neptuno/`. Claude asks before anything
else, such as editing the project's own files or running other commands on
your machine. Claude now runs with your user's access rather than in a
container, so keep the rules narrow; put personal additions in the
git-ignored `.claude/settings.local.json`.

### Projects from before Claude ran on the host

Earlier versions installed Claude Code in every stack's image and ran it in
the repos' containers with `neptuno claude <repo>`. In such a project:

- Delete `AGENTS.md` and `.claude/skills/neptuno-delegate-to-repo` (or merge
  your edits by hand) and run `neptuno mcp sync`, which writes the new
  versions, `.claude/settings.json` and the repos' subagents.
- Optionally drop `"./.neptuno/claude:/root/.claude"` from the repos'
  `volumes` in `compose.yml` and the Claude Code lines from
  `dockerfiles/<repo>/Dockerfile`, then `docker compose build <repo>`.
  `.neptuno/claude` can go too.

## MCP servers your repos serve

When a repo runs an MCP server, declare it in `neptuno.yml` with the path it
answers on. Neptuno finds the host and port in the Caddyfile:

```yaml
repos:
  locke:
    mcp:
      hyperdrive: /_hyperdrive/mcp
```

Claude Code runs on your machine in the project folder, so it reaches the
server through Caddy, at `http://locke.localhost/_hyperdrive/mcp`, from
`.mcp.json` in the project root; the repos' subagents get the same servers.
The opt-in `playwright` service's browser is added the same way, with no
declaration (see [Browser testing with Playwright](#browser-testing-with-playwright)).
That covers Claude below the dashboard, in any terminal and in the Claude
desktop app's Code tab. A repo's app that calls another repo's server from
its container uses the compose address instead, `http://locke:3000/_hyperdrive/mcp`,
since inside a container `locke.localhost` points back at that container.
`neptuno up` rewrites `.mcp.json` each time it runs, and `neptuno mcp sync`
does it without starting anything. Servers
you add to `.mcp.json` yourself are kept, and Claude asks you to approve
the project's servers the first time it starts in the folder.
`neptuno mcp` lists every declared server with both addresses. The
dashboard's **Info** tab shows them for the repo under the cursor, and
whether each one answers. Repos that serve one have `(mcp)` after their
names, and the project panel shows whether `.mcp.json` is current and
whether Claude desktop has the project. `m` opens a menu to copy a server's
URLs or its `claude mcp add` command, or to add it to Claude Code for every
folder (`--scope user`).

### Instructions for coding agents

`neptuno init` writes files that tell coding agents started in the project
folder how the project works, in formats more than one tool reads:

| File | Read by | Holds |
| --- | --- | --- |
| `AGENTS.md` | Cursor, Codex, Copilot and other tools that follow the AGENTS.md convention | the layout, the commands, `neptuno exec`, the subagents, MCP servers |
| `CLAUDE.md` | Claude Code | `@AGENTS.md`, which imports it, plus any notes only for Claude |
| `.claude/settings.json` | Claude Code | the permission rules above |
| `.claude/agents/<repo>.md` | Claude Code | each repo's subagent |
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
| `neptuno_exec` | run a shell command in a repo's container with `neptuno exec` (or in `repos/<repo>` for `run: host`) |
| `neptuno_logs` | the end of a container's output |
| `neptuno_ask_repo_claude` | hand a task to the repo's subagent, through `claude -p` on your machine, and return its answer |
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
`<repo>/<config>.env`, `_shared/*.local.env`, `<repo>/*.local.env`, then
`neptuno up --set KEY=value`.

`*.local.env` files are personal and git-ignored. They come after every
committed file, so your value wins even when a repo commits its own. One in
`_shared/` reaches every repo, including repos added later: a `before_up`
hook can write `environments/_shared/hook0.local.env` once and every repo gets
what it holds. Neptuno resolves env files again after `before_up`, so values
the hook writes apply to the same `up`. A repo's own `*.local.env` still beats a shared one.

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
