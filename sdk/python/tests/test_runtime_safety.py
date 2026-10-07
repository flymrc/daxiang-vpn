import copy
import json
import subprocess
import sys
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from zongheng_vpn import Client, Status, ZHVpnCommandError, ZHVpnContractError, ZHVpnTimeout

ROOT = Path(__file__).resolve().parents[3]
FIXTURES = json.loads((ROOT / "shared/contracts/fixtures/cli-v1.json").read_text(encoding="utf-8"))
READY = next(item["payload"] for item in FIXTURES if item["name"] == "ready IP observation with compatible extra field")
LEASE = "0123456789abcdef0123456789abcdef"


class RuntimeSafetyTests(unittest.TestCase):
    def test_successful_login_redacts_stdin_and_nested_private_field_echoes(self):
        secret = "synthetic-license-without-known-prefix"
        extra_secret = "synthetic-nested-private-value"
        payload = {"ok": True, "egress": secret, "proxy": "127.0.0.1:7890",
                   "extra": {"ToKeN": extra_secret, "echo": extra_secret,
                             "nested": [secret, {"PRIVATE_KEY": extra_secret}]}}
        completed = subprocess.CompletedProcess([], 0, json.dumps(payload), "")
        client = Client(command=[sys.executable])
        with patch("zongheng_vpn.client.subprocess.run", return_value=completed) as run:
            result = client.login(secret)
        self.assertEqual(run.call_args.kwargs["input"], secret)
        self.assertNotIn(secret, run.call_args.args[0])
        self.assertEqual(result.egress, "<redacted>")
        self.assertNotIn(secret, repr(result))
        self.assertNotIn(extra_secret, repr(result))
        self.assertEqual(result.raw["extra"]["ToKeN"], "<redacted>")
        self.assertEqual(result.raw["extra"]["echo"], "<redacted>")

    def test_login_secret_uses_stdin_and_is_redacted_from_rejection(self):
        secret = "synthetic-license-without-known-prefix"
        client = Client(command=[sys.executable])
        completed = subprocess.CompletedProcess([], 1, json.dumps({"ok": False, "error": "denied " + secret}), secret)
        with patch("zongheng_vpn.client.subprocess.run", return_value=completed) as run:
            with self.assertRaises(ZHVpnCommandError) as raised:
                client.login(secret)
        self.assertEqual(run.call_args.kwargs["input"], secret)
        self.assertNotIn(secret, run.call_args.args[0])
        self.assertIn("--token-stdin", run.call_args.args[0])
        self.assertNotIn(secret, str(raised.exception))
        self.assertNotIn(secret, repr(vars(raised.exception)))
        with patch("zongheng_vpn.client.subprocess.run", side_effect=AssertionError("invalid input started CLI")):
            for token in ("", "x\ny", "x" * 4097):
                with self.assertRaises(ValueError):
                    client.login(token)

    def test_implicit_request_refuses_unknown_degraded_or_unreachable_proxy(self):
        observations = [
            {"running": True, "proxy_reachable": True, "proxy": "127.0.0.1:7890"},
            {"running": False, "proxy_reachable": False, "proxy": "127.0.0.1:7890", "engine_state": "stopped"},
        ]
        partial = copy.deepcopy(READY)
        partial["proxy_reachable"] = False
        for name in ("egress_ip", "egress_ipv4", "egress_ipv6"):
            partial.pop(name, None)
        observations.append(partial)
        diagnostic = copy.deepcopy(READY)
        diagnostic["error"] = "synthetic readiness denial"
        diagnostic.update({"running": False, "proxy_reachable": False, "engine_state": "degraded"})
        for name in ("egress_ip", "egress_ipv4", "egress_ipv6", "instance_id", "config_generation", "control_protocol_version"):
            diagnostic.pop(name, None)
        observations.append(diagnostic)
        request = SimpleNamespace(request=lambda *args, **kwargs: self.fail("network request was attempted"))
        for observation in observations:
            with self.subTest(observation=observation):
                client = Client(command=[sys.executable])
                with patch.object(client, "status", return_value=Status.from_dict(observation)), patch.dict(sys.modules, {"requests": request}):
                    with self.assertRaises(ZHVpnCommandError) as raised:
                        client.get("https://synthetic.invalid")
                self.assertEqual(raised.exception.error_code, "proxy_readiness_unverified")

    def test_explicit_proxy_choice_does_not_claim_authenticated_readiness(self):
        client = Client(command=[sys.executable])
        with patch.object(client, "status", side_effect=AssertionError("unexpected implicit status")):
            self.assertEqual(client.proxies("127.0.0.1:7891")["https"], "http://127.0.0.1:7891")

    def test_rotate_wait_budget_and_explicit_timeout_precedence(self):
        completed = subprocess.CompletedProcess([], 0, '{"ok":true,"status":"triggered"}', "")
        client = Client(command=[sys.executable])
        with patch("zongheng_vpn.client.subprocess.run", return_value=completed) as run:
            client.rotate_ip()
            self.assertEqual(run.call_args.kwargs["timeout"], 180)
            client.rotate_ip(wait_seconds=240)
            self.assertEqual(run.call_args.kwargs["timeout"], 330)
            client.rotate_ip(timeout=4)
            self.assertEqual(run.call_args.kwargs["timeout"], 4)
        client = Client(command=[sys.executable], timeout=5)
        with patch("zongheng_vpn.client.subprocess.run", return_value=completed) as run:
            client.rotate_ip()
            self.assertEqual(run.call_args.kwargs["timeout"], 5)

    def test_timeout_preserves_unknown_mutation_outcome_without_retry(self):
        client = Client(command=[sys.executable], timeout=0.01)
        for action, expected in ((client.rotate_ip, True), (client.disconnect, True), (client.system_proxy_recover, True), (client.status, False), (client.system_proxy_inspect, False)):
            with self.subTest(action=action.__name__):
                with patch("zongheng_vpn.client.subprocess.run", side_effect=subprocess.TimeoutExpired(["synthetic"], 0.01)) as run:
                    with self.assertRaises(ZHVpnTimeout) as raised:
                        action()
                self.assertEqual(run.call_count, 1)
                self.assertEqual(raised.exception.result_unknown, expected)
                self.assertEqual(raised.exception.error_code, "command_outcome_unknown" if expected else "command_timeout")

    def test_invalid_budgets_or_rotate_values_do_not_launch_a_cli(self):
        client = Client(command=[sys.executable])
        with patch("zongheng_vpn.client.subprocess.run", side_effect=AssertionError("unexpected command")):
            for value in (False, 0, -1, float("nan"), float("inf")):
                with self.subTest(timeout=value), self.assertRaises(ValueError):
                    client.status(timeout=value)
            for value in (False, 0, -1, 1.5, "1"):
                with self.subTest(wait=value), self.assertRaises(ValueError):
                    client.rotate_ip(wait_seconds=value)
            with self.assertRaises(ValueError):
                client.rotate_ip(down_seconds=61)

    def test_typed_lease_actions_and_persisted_inspect_do_not_create_health(self):
        client = Client(command=[sys.executable])
        def command(args, **kwargs):
            action = args[1]
            state = {"acquire": "acquired", "release": "released", "inspect": "recorded", "recover": "recovered"}[action]
            payload = {"ok": True, "contract_version": 1, "system_proxy_state": state, "lease_id": LEASE, "owned": True, "noop": False}
            return subprocess.CompletedProcess([], 0, json.dumps(payload), "")
        with patch.object(client, "_run", side_effect=command) as run:
            lease = client.system_proxy_acquire()
            self.assertEqual(lease.lease_id, LEASE)
            self.assertEqual(client.system_proxy_inspect().system_proxy_state, "recorded")
            self.assertFalse(hasattr(lease, "engine_ready"))
            client.system_proxy_release(LEASE)
            self.assertEqual(run.call_args.args[0], ["system-proxy", "release", "--lease-id", LEASE, "--json"])
            self.assertEqual(client.system_proxy_recover().system_proxy_state, "recovered")

    def test_old_or_incomplete_receipts_never_authorize_lease_use(self):
        payloads = [
            {"ok": True},
            {"ok": True, "contract_version": 1, "system_proxy_state": "acquired", "owned": False, "noop": False, "lease_id": LEASE},
            {"ok": True, "contract_version": 1, "system_proxy_state": "recorded", "owned": True, "noop": False, "lease_id": LEASE},
        ]
        client = Client(command=[sys.executable])
        for payload in payloads:
            with self.subTest(payload=payload), patch.object(client, "_run", return_value=subprocess.CompletedProcess([], 0, json.dumps(payload), "")):
                with self.assertRaises(ZHVpnContractError):
                    client.system_proxy_acquire()
        with patch.object(client, "_run", side_effect=AssertionError("invalid ID must not reach CLI")):
            for invalid in ("", LEASE.upper(), "../../foreign", None):
                with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                    client.system_proxy_release(invalid)

    def test_recover_without_a_journal_is_an_explicit_unowned_noop(self):
        client = Client(command=[sys.executable])
        payload = {"ok": True, "contract_version": 1, "system_proxy_state": "absent", "owned": False, "noop": True}
        with patch.object(client, "_run", return_value=subprocess.CompletedProcess([], 0, json.dumps(payload), "")):
            result = client.system_proxy_recover()
            self.assertEqual(result.system_proxy_state, "absent")
            self.assertTrue(result.noop)
            self.assertFalse(result.owned)
            self.assertIsNone(result.lease_id)
        payload["noop"] = False
        with patch.object(client, "_run", return_value=subprocess.CompletedProcess([], 0, json.dumps(payload), "")):
            with self.assertRaises(ZHVpnContractError):
                client.system_proxy_recover()


if __name__ == "__main__":
    unittest.main()
