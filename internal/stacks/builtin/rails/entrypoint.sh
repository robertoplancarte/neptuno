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
    set_aside_old_database &&
    prepare_database && rm -f tmp/pids/server.pid || return 1
}
# NEPTUNO_RESET_DB=1 starts every container start from a fresh database:
# drop it, migrate, and seed. Set it for yourself in a git-ignored
# environments/{{.Name}}/*.local.env, or once with
# `neptuno up {{.Name}} --set NEPTUNO_RESET_DB=1`. Otherwise the database
# keeps its data and only gets pending migrations.
prepare_database() {
  case "${NEPTUNO_RESET_DB:-}" in
  1 | true | yes)
    echo "entrypoint.sh: NEPTUNO_RESET_DB is set; dropping, migrating and seeding the database"
    bin/rails db:drop db:create db:migrate db:seed
    ;;
  *) bin/rails db:prepare ;;
  esac
}
# `neptuno add` leaves tmp/neptuno-new-app in an app it generates. A database
# an earlier app of the same name left behind would make db:prepare fail on
# tables that already exist, so the new app's first start renames it.
set_aside_old_database() {
  [ -f tmp/neptuno-new-app ] || return 0
  bundle exec ruby -rpg -ruri -e '
    url = URI(ENV.fetch("DATABASE_URL"))
    name = url.path.delete_prefix("/")
    url.path = "/postgres"
    pg = PG.connect(url.to_s)
    if pg.exec_params("SELECT 1 FROM pg_database WHERE datname = $1", [name]).ntuples > 0
      old = "#{name}_old_#{Time.now.strftime("%Y%m%d%H%M%S")}"
      pg.exec("ALTER DATABASE #{pg.quote_ident(name)} RENAME TO #{pg.quote_ident(old)}")
      puts "entrypoint.sh: #{name} is from an earlier {{.Name}} app; renamed it to #{old}"
    end
  ' && rm -f tmp/neptuno-new-app
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

# Setup's output also goes to /dev/shm/neptuno-setup.log, which the
# processes show if it fails.
{ setup 2>&1; echo $? >/dev/shm/neptuno-setup.status; } | tee /dev/shm/neptuno-setup.log
if [ "$(cat /dev/shm/neptuno-setup.status)" = 0 ]; then
  touch /dev/shm/neptuno-ready
else
  echo "entrypoint.sh: setup failed" | tee -a /dev/shm/neptuno-setup.log >&2
  touch /dev/shm/neptuno-failed
fi
exec "$@"
