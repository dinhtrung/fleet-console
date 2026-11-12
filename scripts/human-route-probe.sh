#!/usr/bin/env bash
# human-route-probe.sh — prove the daemon's human-route evidence contract
# against a real rig daemon, on throwaway state, without touching the
# operator's queue.
#
# Case A: a queue create aimed at a human seat without --evidence-ref is
#         refused with error=human_route_fields_required and
#         missingFields containing evidence_ref.
# Case B: the same create with --evidence-ref is accepted, the row read back
#         carries that evidenceRef, and the row is closed by the human seat
#         it was parked on so nothing is left waiting.
#
# Every exit path stops the throwaway daemon and deletes its temp home — the
# listener is polled until it is gone, with a direct SIGTERM as the fallback,
# because "the stop command returned" is not "the port is free". A rig release
# that changes either case fails loudly here instead of at the next decision.
#
# Usage: scripts/human-route-probe.sh

set -u -o pipefail

PROBE_HOME=""
PORT=""
DAEMON_PID=""

cleanup() {
	if [ -n "$PROBE_HOME" ]; then
		OPENRIG_HOME="$PROBE_HOME" rig daemon stop >/dev/null 2>&1 || true
		# Verify, don't assume: wait until nothing listens on the port, then
		# SIGTERM the started pid directly if the stop command did not land.
		if [ -n "$PORT" ]; then
			for _ in $(seq 1 8); do
				ss -lptn "sport = :$PORT" 2>/dev/null | grep -q LISTEN || break
				sleep 1
			done
		fi
		if [ -n "$PORT" ] && ss -lptn "sport = :$PORT" 2>/dev/null | grep -q LISTEN && [ -n "$DAEMON_PID" ]; then
			kill "$DAEMON_PID" 2>/dev/null || true
			for _ in $(seq 1 5); do
				ss -lptn "sport = :$PORT" 2>/dev/null | grep -q LISTEN || break
				sleep 1
			done
		fi
		rm -rf "$PROBE_HOME"
	fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

fail() {
	echo "FAIL: $1"
	exit 1
}

# json_of prints the first JSON object it can find in a CLI transcript: the
# whole text parses, else the text from the first '{' through the last '}'.
# rig prints human preamble lines around its JSON, so a whole-text parse is
# not enough.
json_of() {
	python3 - "$1" <<'PY'
import json, sys
text = sys.argv[1]
try:
    print(json.dumps(json.loads(text)))
    raise SystemExit
except ValueError:
    pass
start = text.find("{")
end = text.rfind("}")
if start != -1 and end > start:
    try:
        print(json.dumps(json.loads(text[start:end + 1])))
        raise SystemExit
    except ValueError:
        pass
raise SystemExit(1)
PY
}

# Throwaway state, never ~/.openrig; a port no other listener holds.
PROBE_HOME="$(mktemp -d)"
export OPENRIG_HOME="$PROBE_HOME"
PORT="$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')"

echo "probe: temp OPENRIG_HOME=$PROBE_HOME port=$PORT"
start_out="$(rig daemon start --port "$PORT" --db "$OPENRIG_HOME/openrig.sqlite" --no-kernel 2>&1)" ||
	fail "the throwaway daemon did not start: $start_out"
echo "$start_out"
DAEMON_PID="$(printf '%s' "$start_out" | sed -n 's/.*pid \([0-9][0-9]*\).*/\1/p' | head -1)"

# Readiness is the queue API answering, not daemon status (which exits 0 on a
# stale state file). Bounded retry, no fixed sleep.
ready=0
for _ in $(seq 1 60); do
	if rig queue list -A >/dev/null 2>&1; then
		ready=1
		break
	fi
	sleep 0.5
done
[ "$ready" = 1 ] || fail "the throwaway daemon never answered rig queue list -A"

body="$PROBE_HOME/probe-brief.md"
printf '# probe: human-route evidence contract\n\nA throwaway row for scripts/human-route-probe.sh. Safe to cancel.\n' >"$body"

echo
echo "== case A: human-routed create without --evidence-ref (expect refusal) =="
set +e
outA="$(rig queue create --destination human@kernel \
	--summary "probe: human-route contract" --body-file "$body" --source human@kernel --json 2>&1)"
rcA=$?
set -e
echo "$outA"
[ "$rcA" -ne 0 ] || fail "case A: the create without evidence_ref exited 0; the daemon did not refuse"
docA="$(json_of "$outA")" || fail "case A: the refusal output carried no JSON body"
printf '%s' "$docA" | python3 -c '
import json, sys
doc = json.loads(sys.stdin.read())
assert doc.get("error") == "human_route_fields_required", "error=%r" % (doc.get("error"),)
assert "evidence_ref" in (doc.get("missingFields") or []), "missingFields=%r" % (doc.get("missingFields"),)
' || fail "case A: the refusal body did not name human_route_fields_required / evidence_ref"

echo
echo "== case B: the same create with --evidence-ref (expect accepted) =="
set +e
outB="$(rig queue create --destination human@kernel \
	--summary "probe: human-route contract" --body-file "$body" --source human@kernel \
	--evidence-ref "$body" --json 2>&1)"
rcB=$?
set -e
echo "$outB"
[ "$rcB" -eq 0 ] || fail "case B: the create with evidence_ref exited $rcB"
docB="$(json_of "$outB")" || fail "case B: the accepted output carried no JSON body"
row="$(printf '%s' "$docB" | python3 -c '
import json, sys
doc = json.loads(sys.stdin.read())
print(doc.get("qitemId") or doc.get("id") or "")
')"
[ -n "$row" ] || fail "case B: the accepted body carried no row id"

shown="$(rig queue show "$row" --full --json)" || fail "case B: the created row could not be read back"
echo "$shown"
docS="$(json_of "$shown")" || fail "case B: the show output carried no JSON body"
printf '%s' "$docS" | python3 -c '
import json, sys
doc = json.loads(sys.stdin.read())
assert doc.get("evidenceRef") == sys.argv[1], "evidenceRef=%r" % (doc.get("evidenceRef"),)
' "$body" || fail "case B: the row did not carry the evidence ref it was created with"

# Close the row as the human seat it is parked on, so nothing is left waiting.
set +e
closed="$(OPENRIG_SESSION_NAME=human@kernel rig queue update "$row" \
	--state done --closure-reason canceled --note "probe: human-route contract" --json 2>&1)"
rcC=$?
set -e
echo "$closed"
[ "$rcC" -eq 0 ] || fail "closing the probe row exited $rcC"

echo
echo "PASS: the daemon refuses a human-routed row without --evidence-ref, accepts it with one, and the row reads back with its evidence."
