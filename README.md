# hermes-image

The Hermes Agent image run on morpheus (`services/hermes` in
https://github.com/Barben360/my-homelab): upstream `nousresearch/hermes-agent`
(stable releases only, `-desktop` variant: TigerVNC + Xfce for Bot Screen, where the owner
takes over Hermes's browser to sign in) plus:

- `gh` (GitHub CLI), logged in with the owner's fine-grained token from
  `/run/secrets/github_token` + `/run/secrets/github_login` at every start
  (`rootfs/etc/cont-init.d/50-homelab-credentials`); git uses it as credential helper,
  and commits as `Hermes <hermes@bbarrois.tech>`, SSH-signed with a key kept in the volume
  (its public key is printed in the container log at start);
- `himalaya` (mail, through protonmail-bridge) and `mcp-grafana` (Grafana MCP server),
  pinned releases checked by sha256.

Our skills (`skills/`) are bundled under `/opt/hermes/skills/homelab/`: Hermes syncs them
into its own skills at every start (it keeps the ones it customized).

Every PR and push to `main` builds the image and runs `ci/smoke.sh`; `main` pushes
`ghcr.io/barben360/hermes-agent:latest` and `:sha-<12>`. morpheus polls `:latest`
(policy `poll`) and deploys a new digest within minutes; a failed health check keeps
it and raises `deploy:hermes` (`homelab rollback hermes` goes back).

`main` is protected: PR + green `image` check, no bypass. Renovate bumps the base image
(stable `vX.Y.Z-desktop`, digest pinned) and Actions, and automerges once green. Tool
versions (`HIMALAYA_*`, `MCP_GRAFANA_*`) are bumped by PR with the asset's sha256.

Run the smoke test locally: `docker build -t hermes-image:test . && ci/smoke.sh hermes-image:test`.

Hermes itself may change this repo: see `AGENTS.md`.
