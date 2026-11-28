# Development image for {{.Name}}. The full ruby image already has curl, git,
# and the compilers and libpq headers native gems need. libvips backs the
# image_processing gem that new Rails apps include for Active Storage variants
# (the package is libvips42t64 on Debian trixie, which ruby:4.0 is built on).
FROM ruby:4.0
RUN apt-get update \
 && apt-get install -y --no-install-recommends libvips42t64 ripgrep \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# bundle install and db:prepare run when the container starts; Neptuno waits
# for them before starting the Procfile's processes.
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
ENTRYPOINT ["sh", "/usr/local/bin/entrypoint.sh"]
