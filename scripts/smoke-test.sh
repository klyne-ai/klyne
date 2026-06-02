#!/bin/bash
# scripts/smoke-test.sh — exercise every klyne CLI subcommand and MCP
# tool/prompt end-to-end, fail fast on hang or error.
#
# Designed to run after a fresh `go build` so the binary at the path
# below reflects the current source. Each command gets a 30 s timeout;
# hangs and non-zero exits both count as failures. Output is structured
# so a downstream agent can parse pass/fail counts.
#
# Usage:
#   scripts/smoke-test.sh                # uses ~/.local/bin/klyne
#   KLYNE_BIN=/path/to/klyne scripts/smoke-test.sh
#   PROJECT_DIR=/path/to/cc-project scripts/smoke-test.sh

set -u

KLYNE_BIN="${KLYNE_BIN:-$HOME/.local/bin/klyne}"
PROJECT_DIR="${PROJECT_DIR:-$HOME/Desktop/Learning/consult-service}"
LOG_DIR="${LOG_DIR:-/tmp/klyne-smoke}"
TIMEOUT_SECS=30

if [[ ! -x "$KLYNE_BIN" ]]; then
    echo "FATAL: klyne binary not executable at $KLYNE_BIN"
    exit 2
fi
if [[ ! -d "$PROJECT_DIR" ]]; then
    echo "FATAL: project dir not found at $PROJECT_DIR"
    exit 2
fi

mkdir -p "$LOG_DIR"
rm -f "$LOG_DIR"/*.log "$LOG_DIR"/*.out

PASS=0
FAIL=0
FAILED_CMDS=()

# Poor man's timeout for systems without coreutils. Runs $@ in
# background, waits up to $TIMEOUT_SECS, kills if still alive.
run_with_timeout() {
    local logfile="$1"
    shift
    "$@" >"$logfile" 2>&1 &
    local pid=$!
    local waited=0
    while kill -0 "$pid" 2>/dev/null; do
        if (( waited >= TIMEOUT_SECS )); then
            kill -9 "$pid" 2>/dev/null
            wait "$pid" 2>/dev/null
            return 124  # timeout convention
        fi
        sleep 1
        waited=$((waited + 1))
    done
    wait "$pid"
    return $?
}

check() {
    local name="$1"
    local expected_substring="$2"
    shift 2
    local logfile="$LOG_DIR/${name//[^a-zA-Z0-9_-]/_}.out"

    printf "  %-35s " "$name"
    run_with_timeout "$logfile" "$@"
    local rc=$?

    if [[ $rc -eq 124 ]]; then
        echo "FAIL (timeout after ${TIMEOUT_SECS}s)"
        FAIL=$((FAIL + 1))
        FAILED_CMDS+=("$name (timeout)")
        return
    fi
    if [[ $rc -ne 0 ]]; then
        echo "FAIL (exit $rc)"
        FAIL=$((FAIL + 1))
        FAILED_CMDS+=("$name (exit $rc)")
        return
    fi
    if [[ -n "$expected_substring" ]]; then
        if ! grep -qF "$expected_substring" "$logfile"; then
            echo "FAIL (missing expected substring: $expected_substring)"
            FAIL=$((FAIL + 1))
            FAILED_CMDS+=("$name (no substr: $expected_substring)")
            return
        fi
    fi
    echo "PASS"
    PASS=$((PASS + 1))
}

# Test the MCP server end-to-end via JSON-RPC over stdio. Builds an
# input file with initialize + tool call, pipes it into klyne mcp,
# asserts the expected substring landed in the response. Uses a fixed
# total timeout via `&` + sleep + kill instead of run_with_timeout to
# keep stdin/stdout wiring deterministic.
check_mcp_tool() {
    local name="$1"
    local tool="$2"
    local args_json="$3"
    local expected_substring="$4"
    local logfile="$LOG_DIR/mcp_${tool}.out"
    local errfile="$LOG_DIR/mcp_${tool}.err"
    local infile="$LOG_DIR/mcp_${tool}.in"

    printf "  %-35s " "$name"
    {
        printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'
        printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized"}'
        printf '%s\n' "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"${tool}\",\"arguments\":${args_json}}}"
    } > "$infile"

    # Start server, feed input, give it 8s to answer, then SIGTERM.
    # The MCP server exits on stdin EOF, so we keep stdin open via tail -f
    # on the input file for the full window, then close.
    (
        cat "$infile"
        sleep 6
    ) | "$KLYNE_BIN" mcp >"$logfile" 2>"$errfile" &
    local pid=$!
    local waited=0
    while kill -0 "$pid" 2>/dev/null; do
        if (( waited >= 12 )); then
            kill -TERM "$pid" 2>/dev/null
            sleep 0.5
            kill -9 "$pid" 2>/dev/null
            break
        fi
        sleep 1
        waited=$((waited + 1))
    done
    wait "$pid" 2>/dev/null

    if ! grep -q '"id":2' "$logfile"; then
        echo "FAIL (no id:2 response in stream)"
        FAIL=$((FAIL + 1))
        FAILED_CMDS+=("$name (no response)")
        return
    fi
    if [[ -n "$expected_substring" ]]; then
        if ! grep -qF "$expected_substring" "$logfile"; then
            echo "FAIL (missing substring: $expected_substring)"
            FAIL=$((FAIL + 1))
            FAILED_CMDS+=("$name (no substr)")
            return
        fi
    fi
    echo "PASS"
    PASS=$((PASS + 1))
}

echo "=== klyne smoke test ==="
echo "binary:  $KLYNE_BIN"
echo "project: $PROJECT_DIR"
echo "logs:    $LOG_DIR"
echo

echo "[CLI: basic]"
check "klyne --version"             "klyne version"   "$KLYNE_BIN" --version
check "klyne --help"                "Available Commands" "$KLYNE_BIN" --help
check "klyne doctor"                ""                "$KLYNE_BIN" doctor

echo
echo "[CLI: read-only session views]"
# Token timeline against a real session. With multiple sessions in the
# cwd the CLI returns the ambiguous candidate list; with one it returns
# the timeline. Either is acceptable — just expect non-empty markdown.
(
    cd "$PROJECT_DIR" || exit 99
    check "klyne tokens (cwd=project)" "session"           "$KLYNE_BIN" tokens
    check "klyne statusline"           "klyne"             "$KLYNE_BIN" statusline
    check "klyne statusline --format=mini" ""              "$KLYNE_BIN" statusline --format=mini
    check "klyne statusline --format=plain" ""             "$KLYNE_BIN" statusline --format=plain
    check "klyne advise --explain"     ""                  "$KLYNE_BIN" advise --explain
)

echo
echo "[CLI: aggregate analytics]"
check "klyne top --limit=5"           ""  "$KLYNE_BIN" top --limit=5
check "klyne top --json --limit=3"    ""  "$KLYNE_BIN" top --json --limit=3
check "klyne patterns --limit=5"      ""  "$KLYNE_BIN" patterns --limit=5
check "klyne patterns --json"         ""  "$KLYNE_BIN" patterns --json --limit=3
check "klyne roast --max=3"           ""  "$KLYNE_BIN" roast --max=3
check "klyne roast --json"            ""  "$KLYNE_BIN" roast --json --max=3
check "klyne files --limit=5"         ""  "$KLYNE_BIN" files --limit=5
check "klyne files --json"            ""  "$KLYNE_BIN" files --json --limit=3
check "klyne subagents --limit=5"     ""  "$KLYNE_BIN" subagents --limit=5
check "klyne subagents --json"        ""  "$KLYNE_BIN" subagents --json --limit=3

echo
echo "[CLI: audit + eval]"
# audit-sessions intentionally exits 1 when it finds mismatches; that's
# a feature, not a smoke-test failure. Treat as informational.
audit_log="$LOG_DIR/klyne_audit-sessions_--limit_2.out"
printf "  %-35s " "klyne audit-sessions --limit=2"
run_with_timeout "$audit_log" "$KLYNE_BIN" audit-sessions --limit=2
audit_rc=$?
if [[ $audit_rc -eq 124 ]]; then
    echo "FAIL (timeout)"; FAIL=$((FAIL+1)); FAILED_CMDS+=("klyne audit-sessions (timeout)")
elif [[ $audit_rc -gt 1 ]]; then
    echo "FAIL (exit $audit_rc)"; FAIL=$((FAIL+1)); FAILED_CMDS+=("klyne audit-sessions (exit $audit_rc)")
elif ! grep -q "klyne audit report\|audit report" "$audit_log"; then
    echo "FAIL (no report header)"; FAIL=$((FAIL+1)); FAILED_CMDS+=("klyne audit-sessions (no header)")
else
    if [[ $audit_rc -eq 1 ]]; then
        echo "PASS (mismatches present — expected exit 1)"
    else
        echo "PASS"
    fi
    PASS=$((PASS+1))
fi
check "klyne eval --help"             "" "$KLYNE_BIN" eval --help

echo
echo "[CLI: config (read-only)]"
check "klyne config show"             "" "$KLYNE_BIN" config show
check "klyne config get plan"         "" "$KLYNE_BIN" config get plan

echo
echo "[CLI: decisions (read-only)]"
check "klyne decisions list --limit=5" "" "$KLYNE_BIN" decisions list --limit=5

echo
echo "[CLI: otel]"
check "klyne otel emit --help"        "" "$KLYNE_BIN" otel emit --help

echo
echo "[MCP: JSON-RPC tools/call end-to-end]"
check_mcp_tool "mcp get_token_timeline"   get_token_timeline   "{\"cwd\":\"$PROJECT_DIR\"}" '"markdown"'
check_mcp_tool "mcp get_context_health"   get_context_health   "{\"cwd\":\"$PROJECT_DIR\"}" '"markdown"'
check_mcp_tool "mcp list_sessions"        list_sessions        "{\"cwd\":\"$PROJECT_DIR\"}" '"markdown"'
check_mcp_tool "mcp generate_handoff"     generate_handoff     "{\"cwd\":\"$PROJECT_DIR\"}" '"markdown"'
check_mcp_tool "mcp get_pre_compact_context" get_pre_compact_context "{\"cwd\":\"$PROJECT_DIR\"}" '"markdown"'
check_mcp_tool "mcp search_messages (empty)" search_messages   '{"query":""}'                  "Empty query"

echo
echo "=== SUMMARY ==="
echo "PASS: $PASS"
echo "FAIL: $FAIL"
if (( FAIL > 0 )); then
    echo
    echo "Failed commands:"
    for f in "${FAILED_CMDS[@]}"; do
        echo "  - $f"
    done
    echo
    echo "See $LOG_DIR/*.out for output."
    exit 1
fi
echo
echo "All commands passed."
exit 0
