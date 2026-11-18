#!/bin/sh
# Smoke test of a Hermes image: ci/smoke.sh <image>
# Fails (exit 1) when the gateway does not answer /health within 120 s, a
# baked tool or the Bot Screen desktop (-desktop base) is missing, or the GitHub credentials contract breaks:
#   - with /run/secrets/github_token + github_login: gh's hosts.yml exists,
#     owned by hermes, mode 600, and git uses gh as credential helper;
#   - restarted without the token: hosts.yml is gone (no stale token);
#   - symlinks planted in the volume give Hermes no file of the image.
set -eu

IMAGE=${1:?usage: ci/smoke.sh <image>}
NAME=hermes-smoke-$$
WORK=$(mktemp -d)
VOL=$NAME-data
fail=0

cleanup() {
    docker rm -f "$NAME" >/dev/null 2>&1 || true
    docker volume rm -f "$VOL" >/dev/null 2>&1 || true
    rm -rf "$WORK"
}
trap cleanup EXIT

check() { # check <description> <command...>
    desc=$1; shift
    if "$@" >/dev/null 2>&1; then
        echo "ok   $desc"
    else
        echo "FAIL $desc"
        fail=1
    fi
}

start() { # start [with-token]
    docker rm -f "$NAME" >/dev/null 2>&1 || true
    set -- "$@"
    secrets=""
    if [ "${1:-}" = with-token ]; then
        secrets="-v $WORK/github_token:/run/secrets/github_token:ro -v $WORK/github_login:/run/secrets/github_login:ro"
    fi
    # shellcheck disable=SC2086
    docker run -d --name "$NAME" -v "$VOL:/opt/data" $secrets \
        -e API_SERVER_ENABLED=true -e API_SERVER_HOST=127.0.0.1 \
        "$IMAGE" gateway run >/dev/null
    i=0
    until docker exec "$NAME" curl -fsS -m 3 http://127.0.0.1:8642/health >/dev/null 2>&1; do
        i=$((i + 3))
        if [ "$i" -ge 120 ]; then
            echo "FAIL /health did not answer within 120 s"
            docker logs --tail 50 "$NAME" 2>&1
            exit 1
        fi
        sleep 3
    done
    echo "ok   /health answers"
}

printf 'ghp_smoketest0000000000000000000000000000\n' > "$WORK/github_token"
printf 'smoke-bot\n' > "$WORK/github_login"
chmod 0644 "$WORK/github_token" "$WORK/github_login"

start with-token
x() { docker exec -u hermes "$NAME" "$@"; }
check "gh installed" x gh --version
check "himalaya installed" x himalaya --version
check "mcp-grafana installed" x mcp-grafana --version
check "rsync installed" x rsync --version
check "git installed" x git --version
check "Bot Screen packages (Xvnc, xfwm4, xfce4-panel)" \
    x sh -c 'command -v Xvnc && command -v xfwm4 && command -v xfce4-panel'
check "Matrix E2EE module (olm)" x /opt/hermes/.venv/bin/python -c 'import olm, mautrix'
HOSTS=/opt/data/home/.config/gh/hosts.yml
check "GH_CONFIG_DIR points at the persistent gh config" \
    x sh -c 'test "$GH_CONFIG_DIR" = /opt/data/home/.config/gh'
check "hosts.yml owned by hermes, mode 600" \
    x sh -c "test \"\$(stat -c '%U %a' $HOSTS)\" = 'hermes 600'"
check "hosts.yml holds the login and token" \
    x sh -c "grep -q 'user: smoke-bot' $HOSTS && grep -q 'oauth_token: ghp_smoketest' $HOSTS"
check "gh resolves the login as the github.com user" \
    x sh -c 'test "$(gh config get -h github.com user)" = smoke-bot'
check "git identity is Hermes, not the token's owner" \
    x sh -c 'test "$(git config --global user.name)" = Hermes && test "$(git config --global user.email)" = hermes@bbarrois.tech'
check "signing key owned by hermes, mode 600" \
    x sh -c "test \"\$(stat -c '%U %a' /opt/data/home/.ssh/hermes_signing)\" = 'hermes 600'"
check "commits are SSH-signed and verify" \
    x sh -c 'd=$(mktemp -d) && cd "$d" && git init -q && git commit -q --allow-empty -m t && git verify-commit HEAD 2>&1 | grep -q "Good \"git\" signature for hermes@bbarrois.tech"'
for skill in homelab-pr self-merge-image workspace-journal; do
    check "skill $skill synced into Hermes's skills" \
        x sh -c "find /opt/data/skills -path '*/$skill/SKILL.md' | grep -q ."
done
check "git uses gh as credential helper" \
    x sh -c 'git config --global --get-all credential.https://github.com.helper | grep -qx "!gh auth git-credential"'

KEY1=$(docker exec "$NAME" cat /opt/data/home/.ssh/hermes_signing.pub)
start
check "hosts.yml removed without a token" x sh -c "! test -e $HOSTS"
check "signing key kept across restarts" \
    x sh -c "test -n '$KEY1' && test \"\$(cat /opt/data/home/.ssh/hermes_signing.pub)\" = '$KEY1'"

# Symlink plants (review 2026-11-17): root's start script used to write and
# chown through names in the volume, handing Hermes any file of the image.
# After a restart, no image file may have changed owner, mode or content,
# and the credentials are rebuilt in place of the links.
x ln -sf /opt/hermes/README.md /opt/data/home/.config/git/allowed_signers.tmp
x sh -c 'rm -rf /opt/data/home/.config/gh && ln -s /etc/cont-init.d /opt/data/home/.config/gh'
x sh -c 'rm -f /opt/data/home/.ssh/hermes_signing && ln -s /etc/cont-init.d/01-hermes-setup /opt/data/home/.ssh/hermes_signing'
image_files() {
    docker exec "$NAME" sh -c 'stat -c "%n %U %a %s" /opt/hermes/README.md /etc/cont-init.d /etc/cont-init.d/*; md5sum /etc/cont-init.d/*; find /etc/cont-init.d /opt/hermes -maxdepth 1 -user hermes'
}
BEFORE=$(image_files)
start with-token
check "planted symlinks change no image file" test "$BEFORE" = "$(image_files)"
check "gh credentials rebuilt in place of the symlinked dir" \
    x sh -c "test ! -L /opt/data/home/.config/gh && test \"\$(stat -c '%U %a' $HOSTS)\" = 'hermes 600'"
check "a fresh signing key replaces the symlinked one" \
    x sh -c 'test ! -L /opt/data/home/.ssh/hermes_signing && test "$(stat -c "%U %a" /opt/data/home/.ssh/hermes_signing)" = "hermes 600"'

exit $fail
