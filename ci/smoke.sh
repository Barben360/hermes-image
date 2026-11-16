#!/bin/sh
# Smoke test of a Hermes image: ci/smoke.sh <image>
# Fails (exit 1) when the gateway does not answer /health within 120 s, a
# baked tool is missing, or the GitHub credentials contract breaks:
#   - with /run/secrets/github_token + github_login: gh's hosts.yml exists,
#     owned by hermes, mode 600, and git uses gh as credential helper;
#   - restarted without the token: hosts.yml is gone (no stale token).
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
check "git installed" x git --version
check "Matrix E2EE module (olm)" x /opt/hermes/.venv/bin/python -c 'import olm, mautrix'
HOSTS=/opt/data/home/.config/gh/hosts.yml
check "GH_CONFIG_DIR points at the persistent gh config" \
    x sh -c 'test "$GH_CONFIG_DIR" = /opt/data/home/.config/gh'
check "hosts.yml owned by hermes, mode 600" \
    x sh -c "test \"\$(stat -c '%U %a' $HOSTS)\" = 'hermes 600'"
check "hosts.yml holds the bot login and token" \
    x sh -c "grep -q 'user: smoke-bot' $HOSTS && grep -q 'oauth_token: ghp_smoketest' $HOSTS"
check "gh resolves the bot as the github.com user" \
    x sh -c 'test "$(gh config get -h github.com user)" = smoke-bot'
check "git uses gh as credential helper" \
    x sh -c 'git config --global --get-all credential.https://github.com.helper | grep -qx "!gh auth git-credential"'

start
check "hosts.yml removed without a token" x sh -c "! test -e $HOSTS"

exit $fail
