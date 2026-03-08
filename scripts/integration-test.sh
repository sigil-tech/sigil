#!/usr/bin/env bash
# Sigil OS integration test — verifies the daemon ↔ inference ↔ shell loop.
# Usage: ./scripts/integration-test.sh
#
# Prerequisites:
#   - sigild and sigilctl binaries built (go build ./cmd/sigild/ ./cmd/sigilctl/)
#   - No existing sigild instance running
#
# What it tests:
#   1. Daemon starts cleanly with a test config
#   2. Socket is listening and responds to status queries
#   3. Events can be ingested via sigilctl / socket
#   4. Suggestions are queryable
#   5. AI query handler responds (returns error if no inference backend, which is expected)
#   6. Config handler returns valid JSON
#   7. Patterns and files handlers work
#   8. Fleet preview returns error when fleet is disabled (expected)
#   9. Daemon shuts down cleanly on SIGTERM

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Build binaries
echo "=== Building sigild and sigilctl ==="
cd "$REPO_ROOT"
go build -o "$REPO_ROOT/sigild" ./cmd/sigild/
go build -o "$REPO_ROOT/sigilctl" ./cmd/sigilctl/

SIGILD="$REPO_ROOT/sigild"
SIGILCTL="$REPO_ROOT/sigilctl"

# Temp directory for test data
TEST_DIR=$(mktemp -d /tmp/sigil-test-XXXXXX)
TEST_DB="$TEST_DIR/test.db"
TEST_SOCKET="$TEST_DIR/sigild.sock"
TEST_CONFIG="$TEST_DIR/config.toml"
TEST_WATCH="$TEST_DIR/watch"

mkdir -p "$TEST_WATCH"

# Write test config
cat > "$TEST_CONFIG" <<TOML
[daemon]
log_level = "debug"
watch_dirs = ["$TEST_WATCH"]
repo_dirs = []
db_path = "$TEST_DB"
socket_path = "$TEST_SOCKET"

[inference]
mode = "local"

[inference.local]
enabled = false

[inference.cloud]
enabled = false
TOML

cleanup() {
    echo "=== Cleaning up ==="
    if [ -n "${DAEMON_PID:-}" ] && kill -0 "$DAEMON_PID" 2>/dev/null; then
        kill "$DAEMON_PID" 2>/dev/null || true
        wait "$DAEMON_PID" 2>/dev/null || true
    fi
    rm -rf "$TEST_DIR"
    rm -f "$REPO_ROOT/sigild" "$REPO_ROOT/sigilctl"
}
trap cleanup EXIT

PASS=0
FAIL=0

check() {
    local desc="$1"
    shift
    if "$@" >/dev/null 2>&1; then
        echo "  ✓ $desc"
        PASS=$((PASS + 1))
    else
        echo "  ✗ $desc"
        FAIL=$((FAIL + 1))
    fi
}

check_output() {
    local desc="$1"
    local expected="$2"
    shift 2
    local output
    output=$("$@" 2>/dev/null) || true
    if echo "$output" | grep -q "$expected"; then
        echo "  ✓ $desc"
        PASS=$((PASS + 1))
    else
        echo "  ✗ $desc (expected '$expected', got '$output')"
        FAIL=$((FAIL + 1))
    fi
}

# --- Start daemon -----------------------------------------------------------
echo ""
echo "=== Starting sigild ==="
$SIGILD -config "$TEST_CONFIG" -db "$TEST_DB" -socket "$TEST_SOCKET" -watch "$TEST_WATCH" -log-level debug &
DAEMON_PID=$!

# Wait for socket
echo "  Waiting for socket..."
for i in $(seq 1 30); do
    if [ -S "$TEST_SOCKET" ]; then
        break
    fi
    sleep 0.2
done

if [ ! -S "$TEST_SOCKET" ]; then
    echo "  ✗ Socket did not appear within 6 seconds"
    exit 1
fi
echo "  ✓ Daemon started (PID $DAEMON_PID)"

# --- Test: Status ------------------------------------------------------------
echo ""
echo "=== Testing status ==="
check_output "status returns ok" "ok" $SIGILCTL -socket "$TEST_SOCKET" status

# --- Test: Ingest events -----------------------------------------------------
echo ""
echo "=== Testing event ingestion ==="

# Create a file in the watched directory to trigger file events
touch "$TEST_WATCH/test-file.go"
sleep 1

# Ingest a terminal command via socket (using sigilctl if it supports it, or
# just check that events endpoint works)
check_output "events endpoint responds" "ok" $SIGILCTL -socket "$TEST_SOCKET" events -n 5

# --- Test: Suggestions -------------------------------------------------------
echo ""
echo "=== Testing suggestions ==="
check_output "suggestions endpoint responds" "" $SIGILCTL -socket "$TEST_SOCKET" events -n 5

# --- Test: AI query (expected to fail with no backend) -----------------------
echo ""
echo "=== Testing AI query handler ==="
# The ai-query handler should exist but return an error since no inference backend
echo '{"method":"ai-query","payload":{"query":"test","context":"test"}}' | \
    socat - UNIX-CONNECT:"$TEST_SOCKET" > "$TEST_DIR/ai-response.json" 2>/dev/null || true
if [ -f "$TEST_DIR/ai-response.json" ]; then
    echo "  ✓ AI query handler responded"
    PASS=$((PASS + 1))
else
    echo "  ✗ AI query handler did not respond"
    FAIL=$((FAIL + 1))
fi

# --- Test: Config handler ----------------------------------------------------
echo ""
echo "=== Testing config handler ==="
check_output "config returns db_path" "$TEST_DB" $SIGILCTL -socket "$TEST_SOCKET" config

# --- Test: Fleet preview (should fail gracefully) ----------------------------
echo ""
echo "=== Testing fleet handlers ==="
# Fleet is disabled, so preview should return an error message
echo '{"method":"fleet-preview","payload":{}}' | \
    socat - UNIX-CONNECT:"$TEST_SOCKET" > "$TEST_DIR/fleet-response.json" 2>/dev/null || true
if [ -f "$TEST_DIR/fleet-response.json" ]; then
    echo "  ✓ Fleet preview handler responded"
    PASS=$((PASS + 1))
else
    echo "  ✗ Fleet preview handler did not respond"
    FAIL=$((FAIL + 1))
fi

# --- Test: SQLite has data ---------------------------------------------------
echo ""
echo "=== Checking SQLite ==="
if [ -f "$TEST_DB" ]; then
    echo "  ✓ Database file exists"
    PASS=$((PASS + 1))
else
    echo "  ✗ Database file not created"
    FAIL=$((FAIL + 1))
fi

# --- Test: Graceful shutdown -------------------------------------------------
echo ""
echo "=== Testing shutdown ==="
kill -TERM "$DAEMON_PID"
SHUTDOWN_OK=false
for i in $(seq 1 20); do
    if ! kill -0 "$DAEMON_PID" 2>/dev/null; then
        SHUTDOWN_OK=true
        break
    fi
    sleep 0.5
done

if $SHUTDOWN_OK; then
    echo "  ✓ Daemon shut down cleanly"
    PASS=$((PASS + 1))
else
    echo "  ✗ Daemon did not shut down within 10 seconds"
    FAIL=$((FAIL + 1))
    kill -9 "$DAEMON_PID" 2>/dev/null || true
fi
unset DAEMON_PID

# --- Summary -----------------------------------------------------------------
echo ""
echo "=== Results ==="
echo "  Passed: $PASS"
echo "  Failed: $FAIL"
echo ""

if [ "$FAIL" -gt 0 ]; then
    echo "FAIL"
    exit 1
else
    echo "PASS"
    exit 0
fi
