# Development image for {{.Name}}. The golang image already has curl and git.
FROM golang:1.24
RUN apt-get update \
 && apt-get install -y --no-install-recommends ripgrep \
 && rm -rf /var/lib/apt/lists/*

ENV GOFLAGS=-buildvcs=false
WORKDIR /app
