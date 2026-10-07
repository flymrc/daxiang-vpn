"""Validate full npm audit evidence before accepting its exit status."""
import json
import sys
from pathlib import Path


def report(raw: dict) -> dict:
    if raw.get("auditReportVersion") != 2 or not isinstance(raw.get("vulnerabilities"), dict):
        raise ValueError("incomplete npm audit evidence")
    metadata = raw.get("metadata", {})
    counts = metadata.get("vulnerabilities", {})
    if metadata.get("dependencies", {}).get("total", 0) <= 0 or any(type(counts.get(key)) is not int for key in ("info", "low", "moderate", "high", "critical", "total")):
        raise ValueError("missing npm inventory or vulnerability totals")
    if sum(counts[key] for key in ("info", "low", "moderate", "high", "critical")) != counts["total"] or len(raw["vulnerabilities"]) != counts["total"]:
        raise ValueError("inconsistent npm audit counts")
    return counts


if __name__ == "__main__":
    counts = report(json.loads(Path(sys.argv[1]).read_text(encoding="utf-8-sig")))
    print(json.dumps(counts))
    sys.exit(1 if counts["total"] else 0)
