import copy
import importlib.util
import unittest
from pathlib import Path


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).parents[1] / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


rust = load("rust_report", "rust-security-report.py")
npm = load("npm_report", "npm-security-report.py")


class DependencyReportTests(unittest.TestCase):
    def cargo(self):
        return {"database": {"last-commit": "synthetic"}, "lockfile": {"dependency-count": 1}, "settings": {}, "vulnerabilities": {"found": False, "count": 0, "list": []}, "warnings": {}}

    def test_cargo_partial_or_inconsistent_report_refused(self):
        for raw in ({}, {"vulnerabilities": {"count": 0}}, {**self.cargo(), "vulnerabilities": {"found": False, "count": 0, "list": [{}]}}):
            with self.assertRaises(ValueError):
                rust.report(raw, {}, ["synthetic v1"])

    def test_unsound_warning_cannot_be_waived_on_selected_target(self):
        raw = self.cargo()
        raw["warnings"] = {"unsound": [{"advisory": {"id": "synthetic-advisory"}, "package": {"name": "unsafe-dep"}}]}
        policy = {"cargo_warnings": [{"advisory": "synthetic-advisory", "kind": "unsound", "package": "unsafe-dep", "reason": "not selected", "expires": "2099-01-01"}]}
        self.assertFalse(rust.report(raw, policy, ["synthetic v1"])["unreviewed_warnings"])
        self.assertTrue(rust.report(raw, policy, ["unsafe-dep v1"])["unreviewed_warnings"])
        expired = copy.deepcopy(policy)
        expired["cargo_warnings"][0]["expires"] = "2000-01-01"
        self.assertTrue(rust.report(raw, expired, ["synthetic v1"])["unreviewed_warnings"])

    def test_npm_missing_inventory_and_false_zero_refused(self):
        counts = {key: 0 for key in ("info", "low", "moderate", "high", "critical", "total")}
        raw = {"auditReportVersion": 2, "vulnerabilities": {}, "metadata": {"dependencies": {"total": 1}, "vulnerabilities": counts}}
        self.assertEqual(npm.report(raw)["total"], 0)
        for bad in ({}, {**raw, "vulnerabilities": {"unsafe-dep": {}}}, {**raw, "metadata": {"vulnerabilities": counts}}):
            with self.assertRaises(ValueError):
                npm.report(bad)
