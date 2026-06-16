import json
import os
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from zongheng_vpn import Client, ZHVpnCommandError, ZHVpnJSONError


FAKE_CLI = r"""
import json
import sys

args = sys.argv[1:]

def emit(value, code=0):
    print(json.dumps(value, ensure_ascii=False))
    raise SystemExit(code)

if not args:
    raise SystemExit(2)

cmd = args[0]
if cmd == "login":
    token = args[1]
    if token == "ZH-BAD-SECRET":
        emit({"ok": False, "error": "invalid token: ZH-BAD-SECRET"}, 1)
    emit({"ok": True, "egress": "Rakuten", "proxy": "127.0.0.1:7890"})
if cmd == "start":
    emit({"ok": True, "message": "started", "egress": "Rakuten", "proxy": "127.0.0.1:7890"})
if cmd == "stop":
    emit({"ok": True, "message": "stopped"})
if cmd == "status":
    if "--no-ip-check" in args:
        emit({"running": True, "proxy_reachable": True, "proxy": "127.0.0.1:7890", "egress": "Rakuten"})
    emit({"running": True, "proxy_reachable": True, "proxy": "127.0.0.1:7890", "egress": "Rakuten", "egress_ipv6": "240b::1", "egress_ip": "240b::1"})
if cmd == "rotate-ip":
    emit({"ok": True, "egress": "Rakuten", "before": "240b::1", "after": "240b::2"})
if cmd == "logout":
    emit({"ok": True})
if cmd == "version":
    if "--bad-json" in args:
        print("not-json")
        raise SystemExit(0)
    emit({"ok": True, "version": "dev"})

raise SystemExit(2)
"""


class ClientTests(unittest.TestCase):
    def make_client(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        fake = Path(temp.name) / "fake_zhvpn.py"
        fake.write_text(textwrap.dedent(FAKE_CLI), encoding="utf-8")
        return Client(command=[sys.executable, str(fake)])

    def test_control_methods_use_cli_json(self):
        client = self.make_client()

        login = client.login("ZH-OK")
        self.assertTrue(login.ok)
        self.assertEqual(login.proxy, "127.0.0.1:7890")

        connected = client.connect()
        self.assertTrue(connected.ok)
        self.assertEqual(connected.egress, "Rakuten")

        status = client.status()
        self.assertTrue(status.running)
        self.assertEqual(status.proxy, "127.0.0.1:7890")

        status_ip = client.status_ip()
        self.assertEqual(status_ip.egress_ipv6, "240b::1")

        rotated = client.rotate_ip()
        self.assertEqual(rotated.after, "240b::2")

        stopped = client.disconnect()
        self.assertEqual(stopped.message, "stopped")

        version = client.version()
        self.assertEqual(version.version, "dev")

        logout = client.logout()
        self.assertTrue(logout.ok)

    def test_proxy_helpers(self):
        client = self.make_client()
        self.assertEqual(
            client.proxies(),
            {"http": "http://127.0.0.1:7890", "https": "http://127.0.0.1:7890"},
        )
        self.assertEqual(client.proxy_url("http://localhost:8888"), "http://localhost:8888")

    def test_command_errors_redact_tokens(self):
        client = self.make_client()
        with self.assertRaises(ZHVpnCommandError) as raised:
            client.login("ZH-BAD-SECRET")
        text = str(raised.exception)
        self.assertNotIn("ZH-BAD-SECRET", text)
        self.assertNotIn("ZH-BAD-SECRET", " ".join(raised.exception.command))
        self.assertNotIn("ZH-BAD-SECRET", json.dumps(raised.exception.payload, ensure_ascii=False))
        self.assertIn("ZH-<redacted>", text)

    def test_invalid_json_raises(self):
        client = self.make_client()
        with self.assertRaises(ZHVpnJSONError):
            client._run_json(["version", "--bad-json"])

    def test_cli_discovery_uses_bundled_before_env_when_available(self):
        client = self.make_client()
        bundled = client._bundled_cli()

        with tempfile.TemporaryDirectory() as d:
            fake = Path(d) / "fake_zhvpn.py"
            fake.write_text(textwrap.dedent(FAKE_CLI), encoding="utf-8")
            old = os.environ.get("ZHVPN_EXE")
            os.environ["ZHVPN_EXE"] = str(fake)
            try:
                resolved = client._resolve_command(None, None)
            finally:
                if old is None:
                    os.environ.pop("ZHVPN_EXE", None)
                else:
                    os.environ["ZHVPN_EXE"] = old
            if bundled is not None:
                self.assertEqual(resolved, [str(bundled)])
            else:
                self.assertEqual(resolved, [str(fake)])


if __name__ == "__main__":
    unittest.main()
