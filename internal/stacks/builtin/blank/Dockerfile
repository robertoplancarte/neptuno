# Development image for {{.Name}}. Install the toolchain the repo needs here.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends bash ca-certificates curl git ripgrep \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /app
