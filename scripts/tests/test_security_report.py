import importlib.util
import json
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("security_report", Path(__file__).parents[1] / "security-report.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class SecurityReportTests(unittest.TestCase):
    def stream(self, trace):
        config = {"scanner_name": "govulncheck", "scan_level": "symbol", "scan_mode": "source", "go_version": "synthetic", "db_last_modified": "synthetic"}
        return json.dumps({"config": config}) + "\n" + json.dumps({"progress": {"message": "Checking the code against the vulnerabilities..."}}) + "\n" + json.dumps({"finding": {"osv": "synthetic-advisory", "trace": [trace]}})

    def test_json_exit_success_cannot_hide_symbol_finding(self):
        summary = module.report(self.stream({"module": "example", "package": "example/p", "function": "Serve"}))
        self.assertFalse(summary["passed"])

    def test_module_exception_does_not_allow_imported_package(self):
        exception = {"entries": [{"advisory": "synthetic-advisory", "tier": "module", "expires": "2099-01-01", "reason": "synthetic no import"}]}
        self.assertTrue(module.report(self.stream({"module": "example"}), exception)["passed"])
        self.assertFalse(module.report(self.stream({"module": "example", "package": "example/p"}), exception)["passed"])

    def test_empty_partial_and_expired_evidence_refused(self):
        for raw in ("", "{}", '{"config":', '{"config": {}}', '{"config": {"scan_level":"symbol"}}'):
            with self.assertRaises(ValueError):
                module.report(raw)
        with self.assertRaises(ValueError):
            module.report(self.stream({"module": "example"}), {"entries": [{"advisory": "synthetic-advisory", "tier": "module", "expires": "2000-01-01", "reason": "expired"}]})


if __name__ == "__main__":
    unittest.main()
