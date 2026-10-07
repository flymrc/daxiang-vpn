"""Parse govulncheck's JSON stream without confusing process success with safety."""
import json
import sys
from datetime import date
from pathlib import Path


def report(raw: str, exceptions: dict | None = None) -> dict:
    decoder = json.JSONDecoder()
    messages = []
    rest = raw
    while rest.strip():
        rest = rest.lstrip()
        message, length = decoder.raw_decode(rest)
        if not isinstance(message, dict):
            raise ValueError("invalid scanner message")
        messages.append(message)
        rest = rest[length:]
    configs = [item["config"] for item in messages if "config" in item]
    if len(configs) != 1 or not isinstance(configs[0], dict):
        raise ValueError("missing scanner configuration; incomplete/empty scan")
    config = configs[0]
    if (config.get("scanner_name") != "govulncheck" or config.get("scan_level") != "symbol"
            or config.get("scan_mode") != "source" or not config.get("go_version") or not config.get("db_last_modified")
            or not any("Checking the code" in item.get("progress", {}).get("message", "") for item in messages)):
        raise ValueError("incomplete source/symbol scan identity or analysis progress")
    findings = [item["finding"] for item in messages if "finding" in item]
    tiers = {"symbol": set(), "package": set(), "module": set()}
    modules = {}
    for finding in findings:
        trace = finding.get("trace")
        if not trace or not isinstance(trace, list) or not finding.get("osv"):
            raise ValueError("incomplete vulnerability finding")
        origin = trace[0]
        tier = "symbol" if origin.get("function") else "package" if origin.get("package") else "module"
        tiers[tier].add(finding["osv"])
        modules.setdefault(origin.get("module", "unknown"), set()).add(finding.get("fixed_version") or "no_published_fix")
    # Use strongest evidence once per advisory; module-only is not reachability.
    tiers["package"] -= tiers["symbol"]
    tiers["module"] -= tiers["symbol"] | tiers["package"]
    # Any new finding tier needs review. An exception cannot suppress a symbol
    # finding or silently expand from module evidence to imported package evidence.
    allowed = {"package": set(), "module": set()}
    for entry in (exceptions or {}).get("entries", []):
        if entry.get("tier") not in allowed or not entry.get("reason") or date.fromisoformat(entry["expires"]) < date.today():
            raise ValueError("invalid or expired security exception")
        allowed[entry["tier"]].add(entry["advisory"])
    unreviewed = {tier: sorted(ids - allowed.get(tier, set())) for tier, ids in tiers.items()}
    return {
        "schema_version": 1,
        "config": next(item["config"] for item in messages if "config" in item),
        "counts": {tier: len(ids) for tier, ids in tiers.items()},
        "advisories": {tier: sorted(ids) for tier, ids in tiers.items()},
        "module_fixes": {name: sorted(fixes) for name, fixes in sorted(modules.items())},
        "unreviewed": unreviewed,
        "passed": not any(unreviewed.values()),
        "meaning": "Symbol reports block this local gate. Package/module reports require documented platform/function review; they are not proven exploits.",
    }


if __name__ == "__main__":
    exceptions = json.loads(Path(sys.argv[3]).read_text(encoding="utf-8-sig")) if len(sys.argv)>3 else None
    summary = report(Path(sys.argv[1]).read_text(encoding="utf-8-sig"), exceptions)
    Path(sys.argv[2]).write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(summary["counts"]))
    sys.exit(0 if summary["passed"] else 1)
