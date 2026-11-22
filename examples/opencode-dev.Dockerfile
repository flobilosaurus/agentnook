ARG BASE_IMAGE=node:24-bookworm
FROM ${BASE_IMAGE}

# OpenCode v2; opencode-ai is the older v1 package.
ARG AGENT_VERSION=2.3.2

RUN apt-get update \
    && apt-get install -y --no-install-recommends git ripgrep fd-find ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && npm install --global @opencode/cli@${AGENT_VERSION} \
    && npm cache clean --force \
    && test "$(opencode --version | cut -d ' ' -f 1)" = "${AGENT_VERSION}"

RUN mkdir -p /etc/opencode \
    && printf '%s\n' '{"update":"disable"}' > /etc/opencode/opencode.json

# agentnook supplies a persistent HOME and a temporary telemetry plugin.
# Never bake credentials or agent state into an image.
WORKDIR /workspace
