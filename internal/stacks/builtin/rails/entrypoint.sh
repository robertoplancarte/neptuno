#!/bin/sh
# Runs each time the {{.Name}} container starts, before its command. Neptuno
# holds the Procfile's processes until this touches /dev/shm/neptuno-ready,
# or stops them if it touches /dev/shm/neptuno-failed. /dev/shm starts empty
# on every container start. Setup output: docker compose logs {{.Name}}
setup() {
  [ -f Gemfile ] || return 0 # no app yet while `neptuno add` runs rails new
  # Gems live in the {{.Name}}_bundle volume, so this only installs what
  # Gemfile.lock added since the last start.
  { bundle check >/dev/null || bundle install; } &&
    bin/rails db:prepare && rm -f tmp/pids/server.pid || return 1
}
# The storage_s3 option keeps uploads in this bucket on SeaweedFS. It is
# created in the background, as SeaweedFS can take a while to start; creating
# one that exists is harmless.
if [ -n "$S3_BUCKET" ] && [ -n "$AWS_ENDPOINT" ]; then
  (
    for _ in $(seq 1 120); do
      curl -fsS -o /dev/null -X PUT "$AWS_ENDPOINT/$S3_BUCKET" 2>/dev/null && exit 0
      sleep 2
    done
    echo "entrypoint.sh: could not create bucket $S3_BUCKET at $AWS_ENDPOINT" >&2
  ) &
fi

if setup; then
  touch /dev/shm/neptuno-ready
else
  echo "entrypoint.sh: setup failed" >&2
  touch /dev/shm/neptuno-failed
fi
exec "$@"
