#!/usr/bin/env python3
"""Record decide ticks from a real dispatcher log.

The Go decide loop is a port of `~/bin/fleet-autodispatch.py`. Its log is the proven
contract's own output, so each tick here is the decision-relevant projection of the
inputs the dispatcher read at that timestamp, and `expected.json` is the dispatcher's
own output for the same tick (ready set, dispatched set, skip reasons).

Usage:
    scripts/record-decide-ticks.py --log ~/logs/fleet-autodispatch.log --out internal/decide/testdata/ticks
    scripts/record-decide-ticks.py --dry-run

The projection keeps only what the rules read: item number/kind/state/labels/parent,
links, origin branches, live rows, busy seats, and the tick's limit. Titles and bodies
do not change a decision.
"""
from __future__ import annotations

import argparse
import json
import os
import re
import sys
from pathlib import Path

TICK_START = re.compile(r"^(\S+) builders busy=(.*?) free=(\d+) limit=(\d+)$")
OPEN = re.compile(r"open epics \[([^\]]*)\]; open tasks \[([^\]]*)\]")
READY = re.compile(r"ready: \[([^\]]*)\]")
RE_ONHOLD = re.compile(r"#(\d+) skip: label onhold")
RE_PARENT_HOLD = re.compile(r"#(\d+) skip: parent #(\d+) carries onhold")
RE_BLOCKED = re.compile(r"#(\d+) skip: blocked by open (.*)")
RE_LIVE_ROW = re.compile(r"#(\d+) skip: already in a live queue row")
RE_DISPATCHED = re.compile(r"#(\d+) skip: dispatched earlier")
RE_OPEN_CR = re.compile(r"#(\d+) skip: branch feature/(\d+)-.* is in an open MR")
RE_ADOPT = re.compile(r"#(\d+) adopt: branch feature/(\d+)-.* on origin but no MR")
RE_MERGED = re.compile(r"#(\d+) WARN: MR !\d+ is merged but the task is still open")
RE_PLAN = re.compile(r"#(\d+) plan: seat=(\S+)")
RE_DISPATCHED_NOW = re.compile(r"#(\d+) dispatched:")


def numbers(text: str) -> list[int]:
    return [int(x) for x in re.findall(r"\d+", text or "")]


def split_ticks(text: str) -> list[tuple[str, list[str]]]:
    ticks: list[tuple[str, list[str]]] = []
    current_ts = ""
    current: list[str] = []
    for line in text.splitlines():
        match = TICK_START.match(line)
        if match:
            if current and current_ts:
                ticks.append((current_ts, current))
            current_ts = match.group(1)
            current = [line]
            continue
        if current_ts:
            current.append(line)
    if current and current_ts:
        ticks.append((current_ts, current))
    return ticks


def build_tick(ts: str, lines: list[str]) -> dict | None:
    head = TICK_START.match(lines[0])
    if not head:
        return None
    busy_raw = head.group(2).strip()
    busy = [] if busy_raw == "-" else [s.strip().strip("'") for s in busy_raw.strip("[]").split(",") if s.strip()]
    free = int(head.group(3))
    limit = int(head.group(4))

    epics: set[int] = set()
    candidates: set[int] = set()
    onhold: set[int] = set()
    parent_hold: dict[int, int] = {}
    blocked: dict[int, list[int]] = {}
    live_rows: set[int] = set()
    dispatched_earlier: set[int] = set()
    open_cr: set[int] = set()
    adopt: set[int] = set()
    merged: set[int] = set()
    ready: set[int] = set()
    planned: set[int] = set()
    dispatched_now: set[int] = set()
    plan_seats: dict[int, str] = {}

    saw_open = False
    for line in lines:
        if (m := OPEN.search(line)):
            saw_open = True
            epics |= set(numbers(m.group(1)))
            candidates |= set(numbers(m.group(2)))
        if m := RE_ONHOLD.search(line):
            onhold.add(int(m.group(1)))
        if m := RE_PARENT_HOLD.search(line):
            parent_hold[int(m.group(1))] = int(m.group(2))
        if m := RE_BLOCKED.search(line):
            blocked[int(m.group(1))] = numbers(m.group(2))
        if m := RE_LIVE_ROW.search(line):
            live_rows.add(int(m.group(1)))
        if m := RE_DISPATCHED.search(line):
            dispatched_earlier.add(int(m.group(1)))
        if m := RE_OPEN_CR.search(line):
            open_cr.add(int(m.group(1)))
        if m := RE_ADOPT.search(line):
            adopt.add(int(m.group(1)))
        if m := RE_MERGED.search(line):
            merged.add(int(m.group(1)))
        if m := READY.search(line):
            ready |= set(numbers(m.group(1)))
        if m := RE_PLAN.search(line):
            number = int(m.group(1))
            planned.add(number)
            plan_seats[number] = m.group(2)
        if m := RE_DISPATCHED_NOW.search(line):
            dispatched_now.add(int(m.group(1)))

    if not saw_open or free <= 0:
        return None

    tasks = candidates | onhold | set(parent_hold) | set(blocked) | live_rows | dispatched_earlier | open_cr | adopt | merged | ready | planned
    tasks -= epics

    items: dict[int, dict] = {}
    for number in sorted(epics):
        items[number] = {"number": number, "kind": "epic", "state": "open", "labels": [], "title": f"Epic {number}"}
    for number in sorted(tasks):
        label = ["onhold"] if number in onhold else []
        item = {"number": number, "kind": "task", "state": "open", "labels": label, "title": f"Task {number}"}
        if number in parent_hold:
            parent = parent_hold[number]
            item["parent"] = parent
            if parent not in items:
                items[parent] = {"number": parent, "kind": "epic", "state": "open", "labels": ["onhold"], "title": f"Epic {parent}"}
            else:
                labels = set(items[parent].get("labels") or [])
                labels.add("onhold")
                items[parent]["labels"] = sorted(labels)
        if number in blocked:
            item["related"] = [{"number": b, "state": "open"} for b in blocked[number]]
            for blocker in blocked[number]:
                if blocker not in items and blocker not in epics:
                    # The link is open, but the blocker is not a candidate in this tick
                    # (epic, other project, or closed after the link was read). Keep it
                    # out of dispatch without changing the link state.
                    items[blocker] = {"number": blocker, "kind": "task", "state": "closed",
                                      "labels": [], "title": f"Blocker {blocker}"}
        items[number] = item

    rows = []
    for number in sorted(live_rows):
        rows.append({
            "qitem_id": f"fixture-live-{number}",
            "state": "pending",
            "pickup": "",
            "destination": "row-holder@fixture",
            "body": f"Continuing #{number}",
            "summary": "",
        })
    for index, seat in enumerate(busy):
        rows.append({
            "qitem_id": f"fixture-busy-{index}",
            "state": "in-progress",
            "pickup": "working",
            "destination": seat,
            "body": f"Work on fixture item {900000 + index}",
            "summary": "",
        })

    branches = []
    for number in sorted(open_cr):
        branches.append({"name": f"feature/{number}-fixture", "item": number, "has_open_cr": True})
    for number in sorted(merged):
        branches.append({"name": f"feature/{number}-fixture", "item": number, "has_merged_cr": True})
    for number in sorted(adopt):
        branches.append({"name": f"feature/{number}-fixture", "item": number})

    builders = list(busy) + [f"free-{i}@fixture" for i in range(free)]

    reasons: dict[str, str] = {}
    for number in sorted(epics):
        reasons[str(number)] = "epic"
    for number in sorted(onhold):
        reasons[str(number)] = "onhold"
    for number in sorted(parent_hold):
        reasons[str(number)] = "parent_onhold"
    for number in sorted(blocked):
        reasons[str(number)] = "blocked"
    for number in sorted(dispatched_earlier):
        reasons[str(number)] = "already_dispatched"
    for number in sorted(live_rows):
        reasons[str(number)] = "live_row"
    for number in sorted(open_cr):
        reasons[str(number)] = "branch_open_cr"
    for number in sorted(merged):
        reasons[str(number)] = "branch_merged_cr"
    joined = "\n".join(lines)
    if "closed while planning" in joined:
        return {"error": "plan-time re-read closed an item; not a pure decide tick", "ts": ts}
    if "skip: worktree add:" in joined or "abort:" in joined or "skip: queue create:" in joined:
        return {"error": "act-time failure after planning; not a pure decide tick", "ts": ts}
    # An applied tick records the dispatches that actually happened. A plan line with
    # no dispatch line is an act-time failure and must not become oracle expectation.
    if dispatched_now:
        if planned - dispatched_now:
            return {"error": f"planned but not dispatched: {sorted(planned - dispatched_now)}", "ts": ts}
        dispatched_set = dispatched_now
    else:
        dispatched_set = planned

    dispatched = sorted(dispatched_set)
    # `ready:` is logged truncated to the limit. An open task with no skip log and no
    # plan was in that tail: ready but beyond the free builders.
    task_numbers = {item["number"] for item in items.values() if item["kind"] == "task" and item["state"] == "open"}
    for number in sorted(task_numbers - set(dispatched)):
        reasons.setdefault(str(number), "no_free_builder")

    expected = {
        "status": "planned" if dispatched else "nothing",
        "dispatches": dispatched,
        "reasons": reasons,
    }
    snapshot = {
        "project": {"key": "horisen/mnp/engine/traffic-simulator-v2", "forge": "gitlab",
                    "enabled": True, "onhold": False, "concurrency_cap": limit},
        "items": [items[n] for n in sorted(items)],
        "rows": rows,
        "branches": branches,
        "builders": builders,
        "dispatched": sorted(dispatched_earlier),
        "forge_ok": True,
    }

    if set(dispatched) & {int(n) for n in reasons}:
        return {"error": "a dispatched task also carries a reason", "ts": ts}
    if not set(dispatched) <= task_numbers:
        return {"error": "dispatched something that is not an open task", "ts": ts}

    return {
        "ts": ts,
        "snapshot": snapshot,
        "expected": expected,
        "score": (10 * bool(dispatched) + 3 * bool(adopt) + 2 * bool(open_cr)
                  + 2 * bool(blocked) + 2 * bool(onhold) + 2 * bool(live_rows)
                  + bool(ready - planned)),
        "seats": plan_seats,
        "lines": lines,
    }


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--log", default=os.environ.get("FLEET_LOG", os.path.expanduser("~/logs/fleet-autodispatch.log")))
    ap.add_argument("--out", default="internal/decide/testdata/ticks")
    ap.add_argument("--count", type=int, default=20)
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--verify", action="store_true",
                    help="replay each tick through the Python dispatcher and require the recorded outcome")
    ap.add_argument("--dispatcher", default=os.environ.get(
        "FLEET_DISPATCHER", os.path.expanduser("~/bin/fleet-autodispatch.py")))
    args = ap.parse_args()

    text = Path(args.log).read_text()
    ticks = []
    errors = []
    for ts, lines in split_ticks(text):
        built = build_tick(ts, lines)
        if built is None:
            continue
        if "error" in built:
            errors.append(built)
            continue
        if args.verify:
            replayed = replay(built["snapshot"], args.dispatcher)
            if replayed != built["expected"]:
                errors.append({"ts": built["ts"], "error": f"replay mismatch: {replayed} != {built['expected']}"})
                continue
            built["verified"] = True
        ticks.append(built)

    ticks.sort(key=lambda t: (-t["score"], t["ts"]))
    selected = []
    seen = set()

    def add(tick) -> None:
        signature = (tuple(tick["expected"]["dispatches"]), tuple(sorted(tick["expected"]["reasons"].items())))
        if signature in seen or len(selected) >= args.count:
            return
        seen.add(signature)
        selected.append(tick)

    # First pass: one tick per reason the oracle must exercise.
    for code in ("branch_open_cr", "branch_merged_cr", "no_free_builder", "parent_onhold",
                 "live_row", "onhold", "blocked", "already_dispatched"):
        for tick in ticks:
            if code in tick["expected"]["reasons"].values():
                add(tick)
                break
    # Second pass: the highest-scoring remaining ticks (dispatches, adopts, cap).
    for tick in ticks:
        if len(selected) >= args.count:
            break
        add(tick)

    print(f"parsed {len(ticks)} usable ticks, {len(errors)} rejected, selecting {len(selected)}")
    for tick in selected:
        print(f"  {tick['ts']} score={tick['score']} dispatched={tick['expected']['dispatches']} reasons={len(tick['expected']['reasons'])}")
    if errors:
        print(f"rejected: {errors[:5]}", file=sys.stderr)
    if args.dry_run:
        return 0
    if len(selected) < args.count:
        print(f"not enough ticks: {len(selected)} < {args.count}", file=sys.stderr)
        return 1

    root = Path(args.out)
    for index, tick in enumerate(selected, start=1):
        slug = re.sub(r"[^0-9]", "", tick["ts"])
        directory = root / f"tick-{index:02d}-{slug}"
        directory.mkdir(parents=True, exist_ok=True)
        (directory / "snapshot.json").write_text(json.dumps(tick["snapshot"], indent=2) + "\n")
        (directory / "expected.json").write_text(json.dumps(tick["expected"], indent=2) + "\n")
        (directory / "provenance.txt").write_text(
            f"Recorded from {args.log} at {tick['ts']}.\n"
            f"Replay-verified against {args.dispatcher}: {bool(tick.get('verified'))}.\n"
            "snapshot.json is the decision-relevant projection of the inputs the dispatcher read on\n"
            "that tick; expected.json is the dispatcher's own output (ready/dispatched/skip reasons).\n\n"
            + "\n".join(tick["lines"]) + "\n"
        )
    print(f"wrote {len(selected)} ticks to {root}")
    return 0


# --------------------------------------------------------------------------- replay


def replay(snapshot: dict, dispatcher: str) -> dict:
    """Run the real Python dispatcher over a recorded snapshot, without side effects.

    The decision loop lives in `main()`. Every read is replaced by the snapshot, every
    housekeeping action and message by a no-op, and `--apply` stays off, so the only
    thing that happens is the plan the contract would produce for these inputs.
    """
    import importlib.util
    import subprocess
    import tempfile

    spec = importlib.util.spec_from_file_location("fleet_autodispatch_replay", dispatcher)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)

    items = snapshot["items"]
    children: dict[int, list[dict]] = {}
    for item in items:
        parent = item.get("parent")
        if parent is not None:
            children.setdefault(parent, []).append(item)

    def node(item: dict) -> dict:
        widgets = []
        if item.get("labels"):
            widgets.append({"type": "LABELS", "labels": {"nodes": [{"title": x} for x in item["labels"]]}})
        if item["kind"] == "epic":
            widgets.append({"type": "HIERARCHY", "children": {"nodes": [
                {"iid": c["number"], "state": "OPEN", "workItemType": {"name": "Task"}}
                for c in children.get(item["number"], [])
            ]}})
        elif item.get("parent") is not None:
            widgets.append({"type": "HIERARCHY", "parent": {"iid": item["parent"]}})
        return {
            "iid": item["number"],
            "state": "OPEN" if item["state"] == "open" else "CLOSED",
            "title": item.get("title") or f"Item {item['number']}",
            "workItemType": {"name": "Issue" if item["kind"] == "epic" else "Task"},
            "widgets": widgets,
        }

    rows = snapshot["rows"]
    by_qid = {row["qitem_id"]: row for row in rows}
    branches = snapshot["branches"]

    module.work_items = lambda: {"nodes": [node(i) for i in items], "pages": 1}
    module.queue_rows = lambda: [{
        "qitemId": row["qitem_id"], "state": row["state"],
        "pickup": {"state": row["pickup"]},
        "destinationSession": row["destination"],
        "body": row["body"], "summary": row["summary"],
    } for row in rows]
    module.related = lambda iid: [
        {"iid": link["number"], "state": "opened" if link["state"] == "open" else "closed"}
        for item in items if item["number"] == iid for link in item.get("related") or []
    ]
    module.branch_exists = lambda iid: any(b.get("item") == iid for b in branches)
    module.branch_in_open_mr = lambda iid: any(
        b.get("item") == iid and b.get("has_open_cr") for b in branches)
    module.merged_mr_for = lambda iid: (
        {"iid": 9000 + iid} if any(b.get("item") == iid and b.get("has_merged_cr") for b in branches)
        else None)
    module.BUILDERS = list(snapshot["builders"])
    module.EPICS = []

    for name in ("ensure_seat_config", "prune_closed", "reset_stalled", "reconcile_ongoing",
                 "renudge_unclaimed", "announce_parked", "watchdog_merge", "rebase_conflicts",
                 "reclaim_worktrees", "announce_escalations", "close_finished_epics",
                 "send", "set_labels"):
        setattr(module, name, lambda *args, **kwargs: None)
    module.find_parked = lambda state, rows: []

    lines: list[str] = []
    module.log = lambda message: lines.append(str(message))

    state_file = Path(tempfile.mkdtemp()) / "state.json"
    module.STATE = state_file

    original_run = subprocess.run

    def fake_run(args, *rest, **kwargs):
        args = list(args)
        if args[:3] == ["rig", "queue", "show"]:
            row = by_qid.get(args[3], {})
            return subprocess.CompletedProcess(args, 0, json.dumps({
                "body": row.get("body", ""), "summary": row.get("summary", "")}), "")
        if args and args[0] == "git":
            return subprocess.CompletedProcess(args, 0, "deadbeef\n", "")
        return subprocess.CompletedProcess(args, 0, "", "")

    subprocess.run = fake_run
    original_argv = sys.argv
    sys.argv = ["fleet-autodispatch.py"]
    try:
        module.main()
    finally:
        subprocess.run = original_run
        sys.argv = original_argv

    dispatched_now: set[int] = set()
    reasons: dict[str, str] = {}
    ready_logged: set[int] = set()
    for line in lines:
        if m := RE_PLAN.search(line):
            dispatched_now.add(int(m.group(1)))
        elif m := RE_DISPATCHED_NOW.search(line):
            dispatched_now.add(int(m.group(1)))
        elif m := READY.search(line):
            ready_logged |= set(numbers(m.group(1)))
        else:
            for code, regex in (
                ("onhold", RE_ONHOLD), ("parent_onhold", RE_PARENT_HOLD),
                ("blocked", RE_BLOCKED), ("live_row", RE_LIVE_ROW),
                ("already_dispatched", RE_DISPATCHED), ("branch_open_cr", RE_OPEN_CR),
                ("branch_merged_cr", RE_MERGED),
            ):
                if m := regex.search(line):
                    reasons[str(int(m.group(1)))] = code
                    break

    for item in items:
        if item["kind"] == "epic" and item["state"] == "open":
            reasons.setdefault(str(item["number"]), "epic")
    task_numbers = {item["number"] for item in items if item["kind"] == "task" and item["state"] == "open"}
    for number in sorted(task_numbers - dispatched_now):
        reasons.setdefault(str(number), "no_free_builder")

    return {
        "status": "planned" if dispatched_now else "nothing",
        "dispatches": sorted(dispatched_now),
        "reasons": reasons,
    }


if __name__ == "__main__":
    sys.exit(main())
