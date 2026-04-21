#!/usr/bin/env bash
# test-merge-flow.sh — exercise the VM session + merge pipeline end-to-end
# without a real hypervisor. Creates a fake VM SQLite with 5 events (3 clean,
# 2 that should be filtered), drives it through VMStart → VMStop → VMMerge,
# and prints corpus/audit state at each step.
#
# Requires: sigild running (make run), python3, sigilctl built at ./bin/.
# Run from the sigil/ repo root.

set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$REPO/bin"
SIGILCTL="$BIN/sigilctl"
HOST_DB="${HOST_DB:-$HOME/.local/share/sigild/data.db}"
WORK="${WORK:-/tmp/sigil-merge-test}"
IMAGE="$WORK/fake.img"
VM_DB="$WORK/vm.db"

step() { printf '\n\033[1;36m=== %s ===\033[0m\n' "$*"; }

[[ -x "$SIGILCTL" ]] || { echo "sigilctl not built — run 'make build' first" >&2; exit 1; }
[[ -f "$HOST_DB" ]] || { echo "host db not found at $HOST_DB — start sigild with 'make run' first" >&2; exit 1; }
"$SIGILCTL" status >/dev/null 2>&1 || { echo "sigild not responding — is 'make run' still alive?" >&2; exit 1; }

mkdir -p "$WORK"
rm -f "$IMAGE" "$VM_DB" "$VM_DB-journal" "$VM_DB-wal" "$VM_DB-shm"
: > "$IMAGE"  # placeholder; sigild does not open it, only stores the path

step "Create fake VM SQLite with 5 events"
python3 - "$VM_DB" <<'PY'
import sqlite3, sys, time, json
db = sqlite3.connect(sys.argv[1])
db.execute("""
    CREATE TABLE events (
        id      INTEGER PRIMARY KEY AUTOINCREMENT,
        kind    TEXT    NOT NULL,
        source  TEXT    NOT NULL,
        payload TEXT    NOT NULL,
        ts      INTEGER NOT NULL,
        vm_id   TEXT    NOT NULL DEFAULT ''
    )
""")
now = int(time.time() * 1000)
rows = [
    # 3 clean rows — should land in training_corpus
    ("file",        "vm-fs",    {"path": "/workspace/foo.go", "op": "write"}),
    ("terminal",    "vm-shell", {"cmd": "go test ./...", "exit": 0}),
    ("process",     "vm-proc",  {"name": "go", "args": ["test", "./..."]}),
    # denylist hit: .env basename matches *.env glob
    ("file",        "vm-fs",    {"path": "/workspace/.env", "op": "write"}),
    # RFC1918 destination — merge strips these for net.connect
    ("net.connect", "vm-net",   {"dest": "10.0.0.5", "port": 5432}),
]
for kind, source, payload in rows:
    db.execute(
        "INSERT INTO events (kind, source, payload, ts, vm_id) VALUES (?,?,?,?,?)",
        (kind, source, json.dumps(payload), now, "vm-smoke"),
    )
db.commit()
db.close()
print(f"wrote {len(rows)} events to {sys.argv[1]}")
PY

step "sigilctl vm start"
"$SIGILCTL" vm start --image "$IMAGE" --vm-db "$VM_DB"

# The CLI response prints empty fields (payload uses `id`, CLI expects
# `session_id`), so read the session ID back from the host sessions table.
SESSION=$(python3 - "$HOST_DB" "$IMAGE" <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
row = db.execute(
    "SELECT id FROM sessions WHERE disk_image_path = ? ORDER BY started_at DESC LIMIT 1",
    (sys.argv[2],),
).fetchone()
print(row[0] if row else "")
PY
)
[[ -n "$SESSION" ]] || { echo "could not find session row" >&2; exit 1; }
echo "SESSION=$SESSION"

step "sigilctl vm stop --session $SESSION  (transitions to 'stopping')"
"$SIGILCTL" vm stop --session "$SESSION"

step "sigilctl merge retry --session $SESSION  (drives VMMerge → training_corpus)"
"$SIGILCTL" merge retry --session "$SESSION"

step "sigilctl corpus stats"
"$SIGILCTL" corpus stats || true

step "sigilctl audit corpus  (expect 3 rows: file/terminal/process)"
"$SIGILCTL" audit corpus || true

step "sigilctl audit merge  (expect 1 row: status=complete, 3 merged, 2 filtered)"
"$SIGILCTL" audit merge || true

step "sigilctl audit filtered  (expect 2 rows: denylist:*.env, private_destination)"
"$SIGILCTL" audit filtered || true

step "Direct SQL verification"
python3 - "$HOST_DB" "$SESSION" <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
sid = sys.argv[2]
print("training_corpus rows for this session:")
for r in db.execute(
    "SELECT id, origin, event_type, source FROM training_corpus WHERE origin_session=?",
    (sid,),
):
    print(" ", r)
print("merge_log:")
for r in db.execute(
    "SELECT session_id, status, rows_merged, rows_filtered FROM merge_log WHERE session_id=?",
    (sid,),
):
    print(" ", r)
print("filtered_log:")
for r in db.execute(
    "SELECT event_type, filter_rule, excluded_reason FROM filtered_log WHERE session_id=?",
    (sid,),
):
    print(" ", r)
PY

echo
echo "Done. Artifacts in $WORK; session id: $SESSION"
