ARG BASE_IMAGE=node:24-bookworm
FROM ${BASE_IMAGE}

# Set an exact supported Claude Code npm version at build time.
ARG AGENT_VERSION

RUN apt-get update \
    && apt-get install -y --no-install-recommends git ripgrep fd-find ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && npm install --global @anthropic-ai/claude-code@${AGENT_VERSION} \
    && npm cache clean --force \
    && test "$(claude --version | cut -d ' ' -f 1)" = "${AGENT_VERSION}"

# agentnook sets DISABLE_AUTOUPDATER=1 and supplies a persistent HOME at runtime.
# Never bake credentials or agent state into an image.
WORKDIR /workspace
