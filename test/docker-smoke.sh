#!/usr/bin/env bash
# End-to-end smoke test for a fresh Linux install of GoLeM.
# Used by test/Dockerfile.smoke. Expects glm on PATH, claude on PATH,
# and a Z.AI API key mounted at $HOME/.config/GoLeM/zai_api_key (read-only).
#
# Each step is announced and shells log clearly on failure.

set -uo pipefail

# The built-in Z.AI vision MCP is on by default; disable it for the smoke run so
# golems do not spawn the npx vision server (network + Node 22), keeping the
# suite deterministic. The vision config generation is covered by unit tests;
# the prompt-separation fix it depends on is exercised by the --mcp-config step.
export GLM_VISION_MCP=0

PASS=0
FAIL=0
FAILED_STEPS=()

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
        FAILED_STEPS+=("$name")
        echo "FAIL  $name"
    fi
}

expect_fail() {
    if "$@" >/dev/null 2>&1; then
        echo "expected non-zero exit, got 0"
        return 1
    fi
    return 0
}

# ---- Pre-conditions ----
step "binary present"               command -v glm >/dev/null
step "claude present"               command -v claude >/dev/null
step "version prints"               bash -c 'glm version | grep -q "glm "'
step "help prints to stdout"        bash -c 'glm help | grep -q "Commands:"'
step "help is not on stderr"        bash -c 'test "$(glm help 2>&1 1>/dev/null | wc -l)" -eq 0'
step "unknown command rejected"     expect_fail bash -c 'glm not-a-real-cmd'
step "--dir long flag works"        bash -c 'glm run --dir /nonexistent --timeout 5 "x" 2>&1 | grep -q "Directory not found"'
step "-d short flag works"          bash -c 'glm run -d /nonexistent -t 5 "x" 2>&1 | grep -q "Directory not found"'

# ---- Doctor before _install ----
step "doctor runs without install"  bash -c 'glm doctor >/dev/null; glm doctor | wc -l | grep -q "^7$"'

# ---- _install non-interactive ----
if [ ! -f "$HOME/.config/GoLeM/zai_api_key" ]; then
    echo "ERROR: \$HOME/.config/GoLeM/zai_api_key must be mounted"
    exit 2
fi

# Feed: N (don't overwrite existing key) + permission mode
{
    printf '%s\n' "N"
    printf '%s\n' "bypassPermissions"
} | glm _install >/tmp/install.out 2>/tmp/install.err
rc=$?
if [ "$rc" -eq 0 ]; then
    PASS=$((PASS + 1))
    echo "OK  _install completed"
else
    FAIL=$((FAIL + 1))
    FAILED_STEPS+=("_install failed (rc=$rc)")
    echo "FAIL  _install"
    cat /tmp/install.out /tmp/install.err
fi

# ---- Validate install side-effects ----
step "settings.json has golem entry" \
    bash -c 'grep -q "golem" "$HOME/.claude/settings.json"'
step "golem skill installed" \
    bash -c 'grep -q "name: golem" "$HOME/.claude/skills/golem/SKILL.md"'
step "subagents dir created" \
    bash -c 'test -d "$HOME/.claude/subagents"'
step "config.json written" \
    bash -c 'test -s "$HOME/.config/GoLeM/config.json"'
step "glm.toml written" \
    bash -c 'test -s "$HOME/.config/GoLeM/glm.toml"'

# ---- Doctor after install (proxy may be WARN if not started yet) ----
step "doctor 7 checks" \
    bash -c 'glm doctor | wc -l | grep -q "^7$"'
step "doctor api_key OK" \
    bash -c 'glm doctor | grep "^api_key" | grep -q OK'
step "doctor claude_cli OK" \
    bash -c 'glm doctor | grep "^claude_cli" | grep -q OK'
step "doctor zai_reachable OK" \
    bash -c 'glm doctor | grep "^zai_reachable" | grep -q OK'

# ---- Config commands ----
step "config show prints 16 lines" \
    bash -c 'test "$(glm config show | wc -l)" -eq 16'
step "config set valid key" \
    bash -c 'glm config set debug true && glm config show | grep "^debug" | grep -q true'
step "config set new (proxy_port)" \
    bash -c 'glm config set proxy_port 18080 && glm config show | grep "^proxy_port" | grep -q 18080'
step "config set invalid key fails" \
    expect_fail bash -c 'glm config set api_rps 5'

# ---- List/clean on empty state ----
step "list runs on empty state" \
    bash -c 'glm list >/dev/null 2>&1'
step "clean runs on empty state" \
    bash -c 'glm clean >/dev/null 2>&1'

# ---- MCP smoke (before real run so proxy is not yet busy) ----
step "mcp serves initialize" \
    bash -c '(printf "%s\n" "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{}}"; sleep 0.5) | timeout 5 glm mcp 2>&1 | grep -q jsonrpc'
step "mcp lists tools" \
    bash -c '(printf "%s\n%s\n" "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{}}" "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\",\"params\":{}}"; sleep 0.5) | timeout 5 glm mcp 2>&1 | grep -q glm_run'

# ---- Real Z.AI calls ----
mkdir -p /tmp/glm-test-workdir
cat > /tmp/glm-test-workdir/HELLO.md <<'INNER'
This is a test directory for GoLeM smoke testing.
The file contains a single sentence so a model can summarize it.
INNER

step "real glm run via Z.AI (90s timeout)" \
    bash -c 'set -o pipefail; glm run --dir /tmp/glm-test-workdir --timeout 90 "Read HELLO.md and answer in one sentence what it says." > /tmp/run.out 2>&1; rc=$?; tail -10 /tmp/run.out; exit $rc'

step "real glm chain via Z.AI (120s timeout)" \
    bash -c 'set -o pipefail; glm chain --dir /tmp/glm-test-workdir --timeout 120 "List files in this directory" "How many files are there?" > /tmp/chain.out 2>&1; rc=$?; tail -10 /tmp/chain.out; exit $rc'

# Regression: --mcp-config is variadic and must not swallow the positional
# prompt (the "--" separator). An empty mcpServers config needs no npx server,
# so this stays fast; the golem must still answer with the requested token.
echo '{"mcpServers":{}}' > /tmp/glm-test-workdir/empty-mcp.json
step "glm run --mcp-config keeps the prompt (Z.AI, 90s)" \
    bash -c 'set -o pipefail; glm run --dir /tmp/glm-test-workdir --mcp-config /tmp/glm-test-workdir/empty-mcp.json --timeout 90 "Reply with exactly the single token BANANAGUARD and nothing else." > /tmp/mcprun.out 2>&1; rc=$?; tail -10 /tmp/mcprun.out; grep -qi BANANAGUARD /tmp/mcprun.out; exit $((rc!=0 ? rc : $?))'

# Real DAG pipeline via Z.AI
cat > /tmp/glm-test-workdir/pipeline.json <<'PIPE'
{
  "steps": [
    {"id": "list", "prompt": "List all files in this directory by name only, one per line."},
    {"id": "count", "prompt": "Count how many lines the previous step produced. Reply with the number only.", "depends_on": ["list"]}
  ]
}
PIPE

step "real glm pipeline via Z.AI (180s timeout)" \
    bash -c 'set -o pipefail; cd /tmp/glm-test-workdir && glm pipeline /tmp/glm-test-workdir/pipeline.json > /tmp/pipeline.out 2>&1; rc=$?; tail -15 /tmp/pipeline.out; exit $rc'

# MCP tools/call - actually invoke glm_run through the MCP interface
step "mcp tools/call glm_run via Z.AI" \
    bash -c '(printf "%s\n%s\n%s\n" \
        "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{}}" \
        "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"glm_run\",\"arguments\":{\"dir\":\"/tmp/glm-test-workdir\",\"timeout\":90,\"prompt\":\"Reply with the single word OK and nothing else.\"}}}" \
        ""; sleep 1) | timeout 120 glm mcp 2>/tmp/mcp.err | tee /tmp/mcp.out | grep -q "\"id\":2"'

# ---- _uninstall (answer N for mounted key, y for jobs) ----
{
    printf '%s\n' "N"  # keep mounted API key
    printf '%s\n' "y"  # remove job artifacts
} | glm _uninstall >/tmp/uninstall.out 2>/tmp/uninstall.err
rc=$?
if [ "$rc" -eq 0 ]; then
    PASS=$((PASS + 1))
    echo "OK  _uninstall completed"
else
    FAIL=$((FAIL + 1))
    FAILED_STEPS+=("_uninstall failed (rc=$rc)")
    cat /tmp/uninstall.out /tmp/uninstall.err
fi

step "uninstall removed golem skill" \
    bash -c '! test -e "$HOME/.claude/skills/golem/SKILL.md"'
step "uninstall removed mcp entry" \
    bash -c '! grep -q "golem" "$HOME/.claude/settings.json" 2>/dev/null'
step "uninstall kept mounted key" \
    bash -c 'test -f "$HOME/.config/GoLeM/zai_api_key"'

# ---- Summary ----
echo
echo "=================="
echo "PASS=$PASS  FAIL=$FAIL"
if [ "$FAIL" -gt 0 ]; then
    echo "Failed steps:"
    for s in "${FAILED_STEPS[@]}"; do
        echo "  - $s"
    done
    exit 1
fi
echo "All smoke tests passed."
exit 0
