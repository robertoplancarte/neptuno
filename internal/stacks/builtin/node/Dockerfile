# Development image for {{.Name}}.
FROM node:22-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git ripgrep \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /app
