import copy
import json
import subprocess
import sys
import traceback
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from zongheng_vpn import Client, Status, ZHVpnCommandError, ZHVpnContractError, ZHVpnJSONError, ZHVpnTimeout
from zongheng_vpn.contracts import validate_payload
from zongheng_vpn.contracts import _check_schema_supported
from zongheng_vpn._contract_generated import DEFINITIONS, PRIVATE_FIELD_NAMES


ROOT = Path(__file__).resolve().parents[3]
FIXTURES = json.loads((ROOT / "shared/contracts/fixtures/cli-v1.json").read_text(encoding="utf-8"))
READY = next(item["payload"] for item in FIXTURES if item["name"] == "ready IP observation with compatible extra field")


class ContractTests(unittest.TestCase):
    def test_shared_schema_fixtures_allow_and_reject(self):
        for fixture in FIXTURES:
            with self.subTest(name=fixture["name"]):
                if fixture["valid"]:
                    validate_payload(fixture["kind"], fixture["payload"])
                else:
                    with self.assertRaises(ZHVpnContractError) as raised:
                        validate_payload(fixture["kind"], fixture["payload"])
                    self.assertTrue(raised.exception.field_path.startswith("$"))

    def test_legacy_semantics_are_preserved_without_authenticated_evidence(self):
        payload = {"running": True, "proxy_reachable": True, "egress_ip": "2001:db8::1", "egress": "cached"}
        status = Status.from_dict(payload)
        self.assertTrue(status.running)
        self.assertTrue(status.proxy_reachable)
        self.assertEqual(status.egress_ip, "2001:db8::1")
        self.assertIsNone(status.evidence.engine_ready)
        self.assertIsNone(status.evidence.proxy_tcp_reachable)
        self.assertIsNone(status.evidence.tunnel_healthy)
        self.assertIsNone(status.evidence.verified_at)
        self.assertIsNone(status.evidence.ipv6_observation)
        self.assertIsNone(status.evidence.egress_ip_observed)

    def test_partial_health_is_valid_and_missing_observations_remain_unknown(self):
        payload = copy.deepcopy(READY)
        payload["proxy_reachable"] = False
        payload.pop("egress_ip")
        payload.pop("egress_ipv4")
        status = Status.from_dict(payload)
        self.assertTrue(status.evidence.engine_ready)
        self.assertFalse(status.evidence.proxy_tcp_reachable)
        self.assertIsNone(status.evidence.egress_ip_observed)
        self.assertIsNone(status.evidence.tunnel_healthy)

    def test_extra_unknown_fields_do_not_create_health_or_verification_time(self):
        payload = copy.deepcopy(READY)
        payload.update({"tunnel_healthy": True, "verified_at": "2099-01-01T00:00:00Z", "future_object": {"ready": True}})
        status = Status.from_dict(payload)
        self.assertEqual(status.raw["future_object"], {"ready": True})
        self.assertIsNone(status.evidence.tunnel_healthy)
        self.assertIsNone(status.evidence.verified_at)
        self.assertTrue(status.evidence.egress_ip_observed)

    def test_public_schema_has_no_secret_properties(self):
        for definition in DEFINITIONS.values():
            self.assertFalse(set(PRIVATE_FIELD_NAMES).intersection(definition["properties"]))

    def test_new_schema_constraints_cannot_be_silently_ignored(self):
        with self.assertRaises(ValueError):
            _check_schema_supported({"type": "string", "maxLength": 1})

    def test_client_contract_error_keeps_safe_location_and_cli_diagnostic(self):
        payload = {"contract_version": 2, "running": False, "proxy_reachable": False, "engine_state": "degraded", "error": "synthetic ZH-BAD-SECRET", "error_code": "engine_control_unavailable"}
        completed = subprocess.CompletedProcess([], 1, json.dumps(payload), "")
        client = Client(command=[sys.executable])
        with patch.object(client, "_run", return_value=completed):
            with self.assertRaises(ZHVpnContractError) as raised:
                client.status()
        error = raised.exception
        self.assertEqual(error.field_path, "$.contract_version")
        self.assertEqual(error.returncode, 1)
        self.assertEqual(error.error_code, "engine_control_unavailable")
        self.assertEqual(error.command[-1], "--no-ip-check")
        self.assertNotIn("ZH-BAD-SECRET", str(error.payload))
        self.assertNotIn("ZH-BAD-SECRET", error.stdout)

    def test_private_field_rejection_does_not_leak_into_exception(self):
        payload = {"running": False, "proxy_reachable": False, "control_secret": "synthetic-sensitive-value", "error": "failed synthetic-sensitive-value"}
        completed = subprocess.CompletedProcess([], 0, json.dumps(payload), "stderr synthetic-sensitive-value")
        client = Client(command=[sys.executable])
        with patch.object(client, "_run", return_value=completed):
            with self.assertRaises(ZHVpnContractError) as raised:
                client.status()
        self.assertEqual(raised.exception.field_path, "$.control_secret")
        self.assertNotIn("synthetic-sensitive-value", raised.exception.stdout)
        self.assertNotIn("synthetic-sensitive-value", raised.exception.stderr)
        self.assertNotIn("synthetic-sensitive-value", str(raised.exception.payload))
        self.assertEqual(raised.exception.payload["control_secret"], "<redacted>")

    def test_command_diagnostic_redacts_actual_login_argument(self):
        payload = {"ok": False, "error": "bad synthetic-unprefixed-token", "error_code": "login_rejected"}
        completed = subprocess.CompletedProcess([], 1, json.dumps(payload), "synthetic-unprefixed-token")
        client = Client(command=[sys.executable])
        with patch.object(client, "_run", return_value=completed):
            with self.assertRaises(ZHVpnCommandError) as raised:
                client.login("synthetic-unprefixed-token")
        self.assertNotIn("synthetic-unprefixed-token", str(raised.exception))
        self.assertNotIn("synthetic-unprefixed-token", raised.exception.stderr)
        self.assertNotIn("synthetic-unprefixed-token", str(raised.exception.payload))

    def test_timeout_traceback_does_not_include_original_subprocess_command(self):
        token = "synthetic-unprefixed-token"
        client = Client(command=[sys.executable])
        failure = subprocess.TimeoutExpired([sys.executable, "login", token], 1)
        with patch("zongheng_vpn.client.subprocess.run", side_effect=failure):
            with self.assertRaises(ZHVpnTimeout) as raised:
                client.login(token)
        rendered = "".join(traceback.format_exception(type(raised.exception), raised.exception, raised.exception.__traceback__))
        self.assertNotIn(token, rendered)
        self.assertNotIn(token, str(raised.exception.command))
        self.assertIsNone(raised.exception.__cause__)

    def test_unparseable_output_is_not_copied_into_diagnostics(self):
        for output in ("", '{"private_key":"synthetic-sensitive-value"', '[{"control_secret":"synthetic-sensitive-value"}]'):
            with self.subTest(output=output):
                completed = subprocess.CompletedProcess([], 1, output, "synthetic-sensitive-value synthetic-unprefixed-token")
                client = Client(command=[sys.executable])
                with patch.object(client, "_run", return_value=completed):
                    with self.assertRaises(ZHVpnJSONError) as raised:
                        client.login("synthetic-unprefixed-token")
                self.assertEqual(raised.exception.returncode, 1)
                self.assertEqual(raised.exception.stdout, "")
                self.assertEqual(raised.exception.stderr, "")
                self.assertNotIn("synthetic-unprefixed-token", str(raised.exception.command))
                self.assertNotIn("synthetic-sensitive-value", str(raised.exception))

    def test_nonzero_current_status_exit_requires_diagnostic(self):
        completed = subprocess.CompletedProcess([], 1, json.dumps(READY), "")
        client = Client(command=[sys.executable])
        with patch.object(client, "_run", return_value=completed):
            with self.assertRaises(ZHVpnContractError) as raised:
                client.status()
        self.assertEqual(raised.exception.field_path, "$.error")


if __name__ == "__main__":
    unittest.main()
