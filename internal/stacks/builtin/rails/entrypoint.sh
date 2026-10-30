#!/bin/sh
# Runs each time the {{.Name}} container starts, before its command. Neptuno
# holds the Procfile's processes until this touches /dev/shm/neptuno-ready,
# or stops them if it touches /dev/shm/neptuno-failed. /dev/shm starts empty
# on every container start. Setup output: docker compose logs {{.Name}}
setup() {
  [ -f Gemfile ] || return 0 # no app yet while `neptuno add` runs rails new
  bundle install && bin/rails db:prepare && rm -f tmp/pids/server.pid
}
if setup; then
  touch /dev/shm/neptuno-ready
else
  echo "entrypoint.sh: setup failed" >&2
  touch /dev/shm/neptuno-failed
fi
exec "$@"
