#!/usr/bin/env python3
"""Audit persisted-model ownership references and direct GORM access.

The default report keeps the broad repository candidate inventory. ``--direct``
reports only database operations that resolve to a persisted model or table.
"""

import argparse
import json
import re
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SOURCES = json.loads((ROOT / "architecture/dependency_sources.json").read_text())
OWNER_DECLARATION = (ROOT / "internal/database/model_owners.go").read_text()
MODEL_OWNERS = dict(re.findall(r'"(\w+)":\s*"(\w+)"', OWNER_DECLARATION))
REPOSITORY_OWNERS = SOURCES["repositoryOwners"]
SCOPE_OWNERS = {item["file"]: item["consumer"] for item in SOURCES["scanScopes"]}
TABLE_OWNERS = {}
for model, owner in MODEL_OWNERS.items():
    snake = re.sub(r"(?<!^)(?=[A-Z])", "_", model).lower()
    plural = snake + ("es" if snake.endswith(("s", "x", "ch", "sh")) else "s")
    if snake.endswith("y") and not snake.endswith(("ay", "ey", "iy", "oy", "uy")):
        plural = snake[:-1] + "ies"
    TABLE_OWNERS[plural] = (model, owner)

DB_CALLS = "Model|Table|Preload|Joins|First|Find|Create|Save|Delete"


def file_owner(path, source):
    receivers = set(re.findall(r"func \([^)]*\*(\w+Repository)\)", source))
    owners = {REPOSITORY_OWNERS.get(receiver) for receiver in receivers}
    owners.discard(None)
    if len(owners) == 1:
        return owners.pop()
    return SCOPE_OWNERS.get(str(path.relative_to(ROOT)))


def direct_accesses(path, source):
    owner = file_owner(path, source)
    if not owner:
        return []
    variables = {}
    for match in re.finditer(r"\bvar\s+(\w+)\s+(?:\[\])?\*?domain\.(\w+)", source):
        variables[match.group(1)] = match.group(2)
    for match in re.finditer(r"\b(\w+)\s*:=\s*(?:&)?domain\.(\w+)\s*\{", source):
        variables[match.group(1)] = match.group(2)
    gorm_variables = set(re.findall(r"\b(\w+)\s*:?=\s*(?:[hrst]\.db|db|tx)\.", source))

    hits = []
    seen = set()
    lines = source.splitlines()
    patterns = [
        re.compile(r"\.(Model|Create|Save|Delete)\(\s*&?domain\.(\w+)"),
        re.compile(r"\.(First|Find|Create|Save|Delete)\(\s*&?(\w+)"),
        re.compile(r"\.(Table|Joins)\(\s*\"([^\"]+)\""),
        re.compile(r"\.Preload\(\s*\"(\w+)\""),
    ]
    for number, line in enumerate(lines, 1):
        if not re.search(rf"\.({DB_CALLS})\(", line):
            continue
        receivers = re.findall(rf"\b(\w+(?:\.\w+)*)\.({DB_CALLS})\(", line)
        direct_root = re.search(r"\b(?:[hrst]\.db|db|tx)\.", line)
        if not direct_root and not any(receiver in gorm_variables for receiver, _ in receivers):
            continue
        targets = []
        explicit = patterns[0].search(line)
        if explicit:
            targets.append((explicit.group(2), explicit.group(1)))
        variable = patterns[1].search(line)
        if variable and variable.group(2) in variables:
            targets.append((variables[variable.group(2)], variable.group(1)))
        table = patterns[2].search(line)
        if table:
            table_name = table.group(2).split()[0].strip("`")
            if table_name in TABLE_OWNERS:
                targets.append((TABLE_OWNERS[table_name][0], table.group(1)))
        preload = patterns[3].search(line)
        if preload and preload.group(1) in MODEL_OWNERS:
            targets.append((preload.group(1), "Preload"))
        for model, operation in targets:
            target_owner = MODEL_OWNERS.get(model)
            if target_owner is None or target_owner == owner or (owner == "L0" and target_owner == "AUD"):
                continue
            key = (number, model, operation)
            if key in seen:
                continue
            seen.add(key)
            hits.append((number, owner, model, target_owner, operation))
    return hits


def report():
    files = 0
    models = set()
    for path in sorted((ROOT / "internal/repository").glob("*.go")):
        source = path.read_text()
        receivers = set(re.findall(r"func \([^)]*\*(\w+Repository)\)", source))
        if not receivers:
            continue
        owners = {REPOSITORY_OWNERS.get(receiver, "?") for receiver in receivers}
        seen = set()
        for number, line in enumerate(source.splitlines(), 1):
            for model in re.findall(r"domain\.(\w+)\b", line):
                owner = MODEL_OWNERS.get(model)
                if owner is None or owner in owners:
                    continue
                key = (model, owner)
                if key in seen:
                    continue
                seen.add(key)
                models.add((path.name, model, owner))
                print(f"{path.relative_to(ROOT)}:{number}\t{','.join(sorted(owners))}\t{model}\t{owner}")
        if seen:
            files += 1
    print(f"CANDIDATE_FILES={files} DISTINCT_FOREIGN_MODEL_REFERENCES={len(models)}")


def report_direct():
    hits = []
    for directory in ("repository", "service", "handler"):
        for path in sorted((ROOT / "internal" / directory).glob("*.go")):
            for hit in direct_accesses(path, path.read_text()):
                hits.append((path, *hit))
    for path, number, owner, model, target_owner, operation in hits:
        print(f"{path.relative_to(ROOT)}:{number}\t{owner}\t{operation}\t{model}\t{target_owner}")
    print(f"DIRECT_STORAGE_VIOLATIONS={len(hits)}")
    return len(hits)


def self_test():
    path = ROOT / "internal/handler/contact_handler.go"
    bad = """
var user domain.User
h.db.Where("id = ?", 1).First(&user)
h.db.Model(&domain.Conversation{})
h.db.Table("messages")
h.db.Preload("Inbox")
"""
    hits = direct_accesses(path, bad)
    found = {(model, operation) for _, _, model, _, operation in hits}
    expected = {("User", "First"), ("Conversation", "Model"), ("Message", "Table"), ("Inbox", "Preload")}
    if found != expected:
        raise SystemExit(f"direct audit self-test mismatch: got={sorted(found)} want={sorted(expected)}")
    safe = "contact := domain.Contact{}\nh.contactRepo.Create(&contact)\n"
    if direct_accesses(path, safe):
        raise SystemExit("repository port call was misclassified as direct storage")
    print("DIRECT_STORAGE_AUDIT_SELF_TEST=PASS")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--direct", action="store_true")
    parser.add_argument("--fail-on-violation", action="store_true")
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    if args.self_test:
        self_test()
        sys.exit(0)
    count = report_direct() if args.direct else (report() or 0)
    if args.fail_on_violation and count:
        sys.exit(1)
