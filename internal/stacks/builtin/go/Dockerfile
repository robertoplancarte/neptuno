# Development image for {{.Name}}. The golang image already has curl and git.
FROM golang:1.24
RUN apt-get update \
 && apt-get install -y --no-install-recommends ripgrep \
 && rm -rf /var/lib/apt/lists/*

# Claude Code, started with `neptuno claude {{.Name}}`. Its sign-in and
# settings live in /root/.claude, which compose mounts from .neptuno/claude
# so they survive rebuilds and are shared by every repo.
RUN curl -fsSL https://claude.ai/install.sh -o /tmp/install-claude.sh \
 && bash /tmp/install-claude.sh \
 && rm /tmp/install-claude.sh \
 && /root/.local/bin/claude --version
ENV PATH="/root/.local/bin:$PATH" CLAUDE_CONFIG_DIR=/root/.claude
ENV GOFLAGS=-buildvcs=false
WORKDIR /app
