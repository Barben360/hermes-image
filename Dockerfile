# Hermes Agent for morpheus: the upstream image plus the tools it uses there.
# Base: the -desktop variant (TigerVNC + Xfce + distro Chromium, ~+235 MB),
# for Bot Screen: the owner watches Hermes's headed browser from the Webapp
# and takes over for a login, 2FA or CAPTCHA. Nothing starts at boot: the
# screen costs memory only while it runs.
# Stable upstream releases only (Renovate, renovate.json); every build passes
# ci/smoke.sh before it is pushed. Add a tool: a layer below + a check in
# ci/smoke.sh, through a PR (AGENTS.md).
ARG HERMES_VERSION=v0.23.1-desktop
FROM nousresearch/hermes-agent:${HERMES_VERSION}@sha256:385725287c207b1517717167d8ac8631e5e7cd2d47a9954df18b6e95b6e5e78b

USER root

# GitHub CLI from GitHub's apt repository.
RUN set -eux; \
    install -d -m 0755 /etc/apt/keyrings; \
    curl -fsSL -o /etc/apt/keyrings/githubcli-archive-keyring.gpg https://cli.github.com/packages/githubcli-archive-keyring.gpg; \
    chmod 0644 /etc/apt/keyrings/githubcli-archive-keyring.gpg; \
    echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends gh; \
    rm -rf /var/lib/apt/lists/*

# rsync (used by the workspace-journal skill to mirror skills/ into the repo).
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends rsync; \
    rm -rf /var/lib/apt/lists/*

# himalaya (mail CLI, used by Hermes's bundled himalaya skill).
# Bumped by PR (version + sha256 of the release asset): Renovate cannot
# recompute an asset checksum.
ARG HIMALAYA_VERSION=v2.2.1
ARG HIMALAYA_SHA256=5c5ba2724c162f82d0a0c71b6c03224ed44f8bef7b14ace5da0635d3af2665a0
RUN set -eux; \
    curl -fsSL -o /tmp/himalaya.tgz "https://github.com/pimalaya/himalaya/releases/download/${HIMALAYA_VERSION}/himalaya.x86_64-linux.tgz"; \
    echo "${HIMALAYA_SHA256}  /tmp/himalaya.tgz" | sha256sum -c -; \
    tar -xzf /tmp/himalaya.tgz -C /usr/local/bin himalaya; \
    rm /tmp/himalaya.tgz

# mcp-grafana (Grafana MCP server, stdio).
ARG MCP_GRAFANA_VERSION=v2.3.0
ARG MCP_GRAFANA_SHA256=f808ea13d236e549a9fffa14b28398d30b5393623141307c916e26f5e7746671
RUN set -eux; \
    curl -fsSL -o /tmp/mcp-grafana.tgz "https://github.com/grafana/mcp-grafana/releases/download/${MCP_GRAFANA_VERSION}/mcp-grafana_Linux_x86_64.tar.gz"; \
    echo "${MCP_GRAFANA_SHA256}  /tmp/mcp-grafana.tgz" | sha256sum -c -; \
    tar -xzf /tmp/mcp-grafana.tgz -C /usr/local/bin mcp-grafana; \
    rm /tmp/mcp-grafana.tgz

# gh and git configuration live in the data volume (persistent, owned by
# hermes); the token comes from /run/secrets at every start.
ENV GH_CONFIG_DIR=/opt/data/home/.config/gh \
    GIT_CONFIG_GLOBAL=/opt/data/home/.gitconfig \
    GH_NO_UPDATE_NOTIFIER=1 \
    GH_PROMPT_DISABLED=1

COPY rootfs/ /

# Our skills, bundled like upstream's: Hermes syncs them into its skills at
# every start (new ones copied; unmodified ones updated; ones it customized
# left alone).
COPY skills/ /opt/hermes/skills/homelab/

# The git commit this image was built from: Hermes compares it with the PR
# it merged to confirm its own update went live (skills/self-merge-image).
ARG IMAGE_REVISION=dev
ENV HERMES_IMAGE_REVISION=${IMAGE_REVISION}
LABEL org.opencontainers.image.revision=${IMAGE_REVISION} \
      org.opencontainers.image.source=https://github.com/Barben360/hermes-image
