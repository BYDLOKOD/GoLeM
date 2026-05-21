#!/usr/bin/env bash
# Verifies the install.sh curl|bash flow against the live GitHub repo.
# Requires: Go, claude on PATH, git, bash, and a mounted Z.AI API key.

set -uo pipefail

PASS=0
FAIL=0
FAILED=()

step() {
    local name="$1"
    shift
    echo
    echo "=== $name ==="
    if "$@"; then
        PASS=$((PASS + 1))
        echo "OK  $name"
    else
        FAIL=$((FAIL + 1))
        FAILED+=("$name")
        echo "FAIL  $name"
    fi
}

if [ ! -f "$HOME/.config/GoLeM/zai_api_key" ]; then
    echo "ERROR: mount the Z.AI key at \$HOME/.config/GoLeM/zai_api_key"
    exit 2
fi

# install.sh prompts the user to overwrite the existing API key and to pick a
# permission mode. Feed both answers via FIFO pipe so they reach `glm _install`.
RAW=https://raw.githubusercontent.com/veschin/GoLeM/main/install.sh
echo "Fetching $RAW"
curl -fsSL "$RAW" -o /tmp/install.sh
chmod +x /tmp/install.sh

# Pipe the answers expected by glm _install (N to overwrite key, default mode).
{
    printf '%s\n' "N"
    printf '%s\n' "bypassPermissions"
} | bash /tmp/install.sh > /tmp/install.out 2>&1
rc=$?
if [ "$rc" -eq 0 ]; then
    PASS=$((PASS + 1))
    echo "OK  install.sh completed"
else
    FAIL=$((FAIL + 1))
    FAILED+=("install.sh failed (rc=$rc)")
    echo "FAIL  install.sh"
    cat /tmp/install.out
fi

# After install.sh: glm should be on PATH (via go install -> $GOPATH/bin)
step "glm on PATH"            command -v glm
step "glm version is 1.2.0"   bash -c 'glm version | grep -q "glm 1.2.0"'
step "doctor 7 OKs"           bash -c 'test "$(glm doctor | grep -c -E "^[a-z_]+ +(OK|WARN)")" -eq 7'
step "settings.json has golem entry" bash -c 'grep -q golem "$HOME/.claude/settings.json"'
step "CLAUDE.md has markers"  bash -c 'grep -q "GLM-SUBAGENT-START" "$HOME/.claude/CLAUDE.md"'
step "clone dir exists"       bash -c 'test -d "$HOME/.local/share/GoLeM/.git"'

# Real glm run against Z.AI
mkdir -p /tmp/glm-test-workdir
echo "test file" > /tmp/glm-test-workdir/X.txt
step "real glm run after install.sh" \
    bash -c 'set -o pipefail; glm run --dir /tmp/glm-test-workdir --timeout 90 "Print the contents of X.txt as a single sentence." > /tmp/run.out 2>&1; rc=$?; tail -10 /tmp/run.out; exit $rc'

echo
echo "=================="
echo "install.sh PASS=$PASS  FAIL=$FAIL"
if [ "$FAIL" -gt 0 ]; then
    for s in "${FAILED[@]}"; do echo "  - $s"; done
    exit 1
fi
exit 0
