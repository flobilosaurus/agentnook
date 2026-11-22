ARG BASE_IMAGE=node:24-bookworm
FROM ${BASE_IMAGE}

ARG AGENT_VERSION=1.4.0

RUN apt-get update \
    && apt-get install -y --no-install-recommends git ripgrep fd-find ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && npm install --global @earendil-works/pi-coding-agent@${AGENT_VERSION} \
    && npm cache clean --force \
    && test "$(pi --version | cut -d ' ' -f 1)" = "${AGENT_VERSION}"

# agentnook mounts the project and an isolated profile HOME, then invokes pi.
# Never bake credentials or agent state into an image.
WORKDIR /workspace
