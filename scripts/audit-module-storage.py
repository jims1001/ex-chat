#!/usr/bin/env python3
"""List repository references to another module's persisted model.

This is a candidate inventory: a type reference can be a DTO rather than a DB
access. Review each hit before treating it as a dependency or violation.
"""

import json
import re
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SOURCES = json.loads((ROOT / "architecture/dependency_sources.json").read_text())
OWNER_DECLARATION = (ROOT / "internal/database/model_owners.go").read_text()
MODEL_OWNERS = dict(re.findall(r'"(\w+)":\s*"(\w+)"', OWNER_DECLARATION))
REPOSITORY_OWNERS = SOURCES["repositoryOwners"]


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


if __name__ == "__main__":
    report()
