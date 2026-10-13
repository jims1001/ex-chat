#!/usr/bin/env python3
"""Validate complete Go JSON streams, including every started child and package.

The runner supplies an independently discovered inventory and explicit skip
obligations. The compatibility CLI accepts a complete go test -json -list log.
Neither a passing parent nor a partial successful stream proves child coverage.
"""

import json
import re
import sys
from pathlib import Path


NAME = re.compile(r"(?:Test|Example|Fuzz)\w*\Z")
TERMINALS = {"pass", "skip", "fail"}
ACTIONS = {"start", "run", "pause", "cont", "output", "bench"} | TERMINALS


def events(path):
    with Path(path).open(encoding="utf-8") as stream:
        for number, line in enumerate(stream, 1):
            if not line.endswith("\n"):
                raise ValueError(f"Unterminated JSON line {number}")
            if not line.strip():
                raise ValueError(f"Empty JSON line {number}")
            event = json.loads(line)
            if (not isinstance(event, dict) or event.get("Action") not in ACTIONS
                    or not isinstance(event.get("Package"), str) or not event["Package"]
                    or not isinstance(event.get("Test", ""), str)
                    or not isinstance(event.get("Output", ""), str)):
                raise ValueError(f"Malformed Go event at line {number}")
            yield event


def inventory(rows, packages):
    """Listing must compile and terminate every package, even those with no tests."""
    expected = {package: [] for package in packages}
    starts, terminal = set(), set()
    for row in rows:
        package, action = row["Package"], row["Action"]
        if package not in expected or package in terminal:
            raise ValueError("Unexpected package or data after discovery terminal: " + package)
        if action == "start":
            if package in starts:
                raise ValueError("Duplicate discovery start: " + package)
            starts.add(package)
        elif package not in starts:
            raise ValueError("Missing discovery package start: " + package)
        if action == "fail" or row.get("Test"):
            raise ValueError("Failed or executing test discovery: " + package)
        if action in TERMINALS:
            if action == "skip" and expected[package]:
                raise ValueError("Skipped discovery with listed cases: " + package)
            terminal.add(package)
        name = row.get("Output", "").strip()
        if NAME.fullmatch(name):
            if name in expected[package]:
                raise ValueError("Duplicate discovered test: " + name)
            expected[package].append(name)
    if terminal != set(expected) or starts != terminal:
        raise ValueError("Incomplete package discovery")
    return expected


def validate(rows, expected, required=None, allowed_skips=(), exact=False):
    """Only explicit (package, test) pairs may skip; every started case must end.

    expected maps packages to discovered roots. required adds exact nested paths.
    exact compares the complete observed set with the reviewed native manifest.
    """
    expected_pairs = {(p, t) for p, names in expected.items() for t in names}
    required = set(required if required is not None else expected_pairs)
    allowed_skips = set(allowed_skips)
    starts, package_terminal, running, finished = set(), {}, set(), {}
    for row in rows:
        package, test, action = row["Package"], row.get("Test", ""), row["Action"]
        pair = (package, test)
        if package not in expected or package in package_terminal:
            raise ValueError("Unexpected package or data after package terminal: " + package)
        if action == "start":
            if test or package in starts:
                raise ValueError("Duplicate/invalid package start: " + package)
            starts.add(package)
        elif package not in starts:
            raise ValueError("Missing package start: " + package)
        if action == "fail":
            raise ValueError(f"Failed case/package: {package} {test}")
        if test:
            if (package, test.split("/", 1)[0]) not in expected_pairs:
                raise ValueError(f"Unexpected test: {package} {test}")
            if action == "run":
                if pair in running or pair in finished:
                    raise ValueError("Duplicate test start: " + test)
                running.add(pair)
            elif action in TERMINALS:
                if pair not in running or pair in finished:
                    raise ValueError("Missing run or duplicate test terminal: " + test)
                if action == "skip" and pair not in allowed_skips:
                    raise ValueError(f"Unmapped or selected skip: {package} {test}")
                finished[pair] = action
            elif action in {"pause", "cont"} and pair not in running:
                raise ValueError("Unstarted parallel test: " + test)
        elif action in TERMINALS:
            if action == "skip" and expected[package]:
                raise ValueError("Skipped package with required tests: " + package)
            if any(p == package and (p, t) not in finished for p, t in running):
                raise ValueError("Package ended with unfinished tests: " + package)
            package_terminal[package] = action
    if set(package_terminal) != set(expected):
        raise ValueError("Missing package terminal")
    if not expected_pairs <= finished.keys() or not required <= finished.keys():
        raise ValueError("Missing required test terminal: " + repr(sorted((expected_pairs | required) - finished.keys())))
    if exact and set(finished) != required:
        raise ValueError("Observed native cases differ from reviewed manifest: " + repr(sorted(set(finished) ^ required)))
    return {"passed": [list(pair) for pair, action in sorted(finished.items()) if action == "pass"],
            "skipped": [list(pair) for pair, action in sorted(finished.items()) if action == "skip"],
            "packages": package_terminal}


def check(list_events, run_events):
    listed = list(list_events)
    packages = sorted({row["Package"] for row in listed})
    expected = inventory(listed, packages)
    if not any(expected.values()):
        raise ValueError("No selected tests were discovered; this is not a pass")
    validate(run_events, expected)
    return sum(map(len, expected.values()))


if __name__ == "__main__":
    if len(sys.argv) != 3:
        raise SystemExit("Usage: check-results.py test-list.jsonl test-run.jsonl")
    try:
        count = check(events(sys.argv[1]), events(sys.argv[2]))
    except (ValueError, KeyError, OSError) as error:
        raise SystemExit(str(error)) from error
    print(f"Verified {count} selected top-level cases and all started children; none skipped")
