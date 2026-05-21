#!/usr/bin/env bash
# Exercises the `go install ...@latest` happy path: a Reddit reader
# follows the TL;DR, expects glm v1.2.0 (or newer) and a working install.

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

# 1. go install @latest - the line copied from the README TL;DR.
echo "=== go install ...@latest ==="
go install github.com/veschin/GoLeM/cmd/glm@latest > /tmp/goinstall.log 2>&1
rc=$?
if [ "$rc" -eq 0 ]; then
    PASS=$((PASS + 1))
    echo "OK  go install completed"
else
    FAIL=$((FAIL + 1))
    FAILED+=("go install failed (rc=$rc)")
    cat /tmp/goinstall.log
fi

# 2. Sanity: glm is on PATH and reports the new version.
step "glm on PATH"                   command -v glm
step "glm version is 1.2.0"          bash -c 'glm version | grep -q "glm 1.2.0"'

# 3. Long-form flags introduced in v1.2.0 must work (this catches the case
#    where a stale @latest from the module proxy still resolved to v1.x).
step "glm run --help on stdout"      bash -c 'glm run --help | grep -q "Commands:"'
step "--dir long form recognised"    bash -c 'glm run --dir /nonexistent --timeout 5 "x" 2>&1 | grep -q "Directory not found"'

# 4. Interactive _install through non-interactive stdin.
{
    printf '%s\n' "N"
    printf '%s\n' "bypassPermissions"
} | glm _install > /tmp/install.out 2>&1
rc=$?
if [ "$rc" -eq 0 ]; then
    PASS=$((PASS + 1))
    echo "OK  _install completed"
else
    FAIL=$((FAIL + 1))
    FAILED+=("_install failed (rc=$rc)")
    cat /tmp/install.out
fi

# 5. config.json must mark the install_mode as go-install so glm update
#    later takes the `go install ...@latest` branch instead of git pull.
step "config.json marks go-install" \
    bash -c 'grep -q "\"install_mode\": \"go-install\"" "$HOME/.config/GoLeM/config.json"'

step "doctor 7 OKs"  bash -c 'test "$(glm doctor | grep -c -E "^[a-z_]+ +(OK|WARN)")" -eq 7'

# 6. Real glm run through Z.AI.
mkdir -p /tmp/work && echo "the answer is 42" > /tmp/work/file.txt
step "real glm run via Z.AI" \
    bash -c 'set -o pipefail; glm run --dir /tmp/work --timeout 90 "What is the answer in file.txt? Respond with just the number." > /tmp/run.out 2>&1; rc=$?; tail -10 /tmp/run.out; exit $rc'

# 7. glm update: in go-install mode this re-runs `go install ...@latest`.
#    Since @latest is already v1.2.0, the call should be idempotent.
step "glm update (go-install, idempotent)" \
    bash -c 'glm update > /tmp/update.out 2>&1; rc=$?; tail -10 /tmp/update.out; exit $rc'
step "glm version still 1.2.0 after update" \
    bash -c 'glm version | grep -q "glm 1.2.0"'
step "CLAUDE.md re-injected after update" \
    bash -c 'grep -q "GLM-SUBAGENT-START" "$HOME/.claude/CLAUDE.md"'

echo
echo "=================="
echo "go install PASS=$PASS  FAIL=$FAIL"
if [ "$FAIL" -gt 0 ]; then
    for s in "${FAILED[@]}"; do echo "  - $s"; done
    exit 1
fi
exit 0
