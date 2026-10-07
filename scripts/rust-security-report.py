"""Gate cargo vulnerabilities and reviewed warnings for the selected target."""
import json
import sys
from datetime import date
from pathlib import Path

def report(raw: dict, policy: dict, selected: list[str]) -> dict:
    required = {"database", "lockfile", "settings", "vulnerabilities", "warnings"}
    if not isinstance(raw, dict) or not required.issubset(raw) or not selected:
        raise ValueError("incomplete Rust scanner or target tree")
    if not raw["database"].get("last-commit") or raw["lockfile"].get("dependency-count", 0) <= 0:
        raise ValueError("missing advisory/lockfile snapshot")
    vulnerabilities = raw["vulnerabilities"]
    count = vulnerabilities.get("count")
    if (type(count) is not int or count < 0 or not isinstance(vulnerabilities.get("list"), list)
            or len(vulnerabilities["list"]) != count or vulnerabilities.get("found") is not (count > 0)):
        raise ValueError("inconsistent Rust vulnerability evidence")
    known = {(e["advisory"], e["kind"], e["package"]): e for e in policy.get("cargo_warnings", [])}
    unreviewed = []
    for kind, warnings in raw.get("warnings", {}).items():
        for warning in warnings:
            key = (warning["advisory"]["id"], kind, warning["package"]["name"])
            exception = known.get(key)
            imported = any(line.startswith(key[2] + " v") for line in selected)
            if (not exception or not exception.get("reason") or date.fromisoformat(exception["expires"]) < date.today()
                    or (kind == "unsound" and imported)
                    or (key[2] == "proc-macro-error" and imported)):
                unreviewed.append(key)
    return {"vulnerabilities": count, "unreviewed_warnings": unreviewed}


if __name__ == "__main__":
    raw = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8-sig"))
    policy = json.loads(Path(sys.argv[2]).read_text(encoding="utf-8-sig"))
    selected = Path(sys.argv[3]).read_text(encoding="utf-8-sig").splitlines()
    summary = report(raw, policy, selected)
    print(json.dumps(summary))
    sys.exit(1 if summary["vulnerabilities"] or summary["unreviewed_warnings"] else 0)
