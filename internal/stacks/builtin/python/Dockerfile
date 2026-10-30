# Development image for {{.Name}}.
FROM python:3.13-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git ripgrep \
 && rm -rf /var/lib/apt/lists/*

# Claude Code, started with `neptuno claude {{.Name}}`. Its sign-in and
# settings live in /root/.claude, which compose mounts from .neptuno/claude
# so they survive rebuilds and are shared by every repo.
RUN curl -fsSL https://claude.ai/install.sh -o /tmp/install-claude.sh \
 && bash /tmp/install-claude.sh \
 && rm /tmp/install-claude.sh \
 && /root/.local/bin/claude --version
ENV PATH="/root/.local/bin:$PATH" CLAUDE_CONFIG_DIR=/root/.claude
ENV PIP_ROOT_USER_ACTION=ignore PIP_DISABLE_PIP_VERSION_CHECK=1 PYTHONUNBUFFERED=1
WORKDIR /app
