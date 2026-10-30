# Development image for {{.Name}}. Install the toolchain the repo needs here.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends bash ca-certificates curl git ripgrep \
 && rm -rf /var/lib/apt/lists/*

# Claude Code, started with `neptuno claude {{.Name}}`. Its sign-in and
# settings live in /root/.claude, which compose mounts from .neptuno/claude
# so they survive rebuilds and are shared by every repo.
RUN curl -fsSL https://claude.ai/install.sh -o /tmp/install-claude.sh \
 && bash /tmp/install-claude.sh \
 && rm /tmp/install-claude.sh \
 && /root/.local/bin/claude --version
ENV PATH="/root/.local/bin:$PATH" CLAUDE_CONFIG_DIR=/root/.claude
WORKDIR /app
