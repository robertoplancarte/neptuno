# Development image for {{.Name}}. The full ruby image already has curl, git,
# and the compilers and libpq headers native gems need.
FROM ruby:3.3
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
WORKDIR /app

# bundle install and db:prepare run when the container starts; Neptuno waits
# for them before starting the Procfile's processes.
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
ENTRYPOINT ["sh", "/usr/local/bin/entrypoint.sh"]
