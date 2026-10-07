import hashlib
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))
from zongheng_vpn import Client, ZHVpnExecutableNotFound


class BundleTests(unittest.TestCase):
    def setUp(self):
        task_temp = tempfile.TemporaryDirectory()
        self.addCleanup(task_temp.cleanup)
        self.image = Path(task_temp.name) / "zhvpn.exe"
        self.manifest = Path(task_temp.name) / "build-manifest.json"
        header = bytearray(64)
        header[:2] = b"MZ"
        header[60:64] = (64).to_bytes(4, "little")
        self.image.write_bytes(header + b"PE\x00\x00\x64\x86" + b"synthetic-not-executable")
        self.data = {"schema_version": 1, "product": "python-sdk", "target": "windows-amd64",
                     "cli_contract_version": 1, "control_protocol_version": 1, "sidecar_protocol_version": 2,
                     "source_commit": "a" * 40, "source_state": "dirty", "development": True, "signed": False,
                     "sha256": hashlib.sha256(self.image.read_bytes()).hexdigest()}

    def verify(self, data=None, *, machine="AMD64", system="Windows", development="1"):
        self.manifest.write_text(json.dumps(self.data if data is None else data), encoding="utf-8")
        with patch("zongheng_vpn.client.platform.machine", return_value=machine), patch("zongheng_vpn.client.platform.system", return_value=system), patch.dict(os.environ, {"ZHVPN_ALLOW_DEVELOPMENT_BUNDLE": development}):
            Client._verify_bundle(self.image, self.manifest)

    def test_development_bundle_needs_explicit_opt_in_and_matching_host(self):
        self.verify()
        for kw in ({"development": ""}, {"machine": "ARM64"}, {"system": "Darwin"}):
            with self.subTest(kw=kw), self.assertRaises(ZHVpnExecutableNotFound):
                self.verify(**kw)

    def test_missing_or_corrupt_metadata_and_changed_image_are_rejected(self):
        for change in ({"source_commit": "unknown"}, {"sidecar_protocol_version": 1}, {"sha256": "0" * 64}, {"schema_version": 2}, {"product": "cli"}, {"schema_version": True}, {"sidecar_protocol_version": 2.0}):
            with self.subTest(change=change), self.assertRaises(ZHVpnExecutableNotFound):
                self.verify({**self.data, **change})
        self.image.write_bytes(b"different payload")
        with self.assertRaises(ZHVpnExecutableNotFound):
            self.verify()
        self.manifest.unlink()
        with self.assertRaises(ZHVpnExecutableNotFound):
            Client._verify_bundle(self.image, self.manifest)

    def test_duplicate_manifest_fields_are_rejected(self):
        self.manifest.write_text('{"schema_version":1,"schema_version":1}', encoding="utf-8")
        with self.assertRaises(ZHVpnExecutableNotFound):
            Client._verify_bundle(self.image, self.manifest)

    def test_release_metadata_requires_clean_signed_source(self):
        data = {**self.data, "development": False, "signed": True, "source_state": "clean"}
        self.verify(data, development="")
        for change in ({"signed": False}, {"source_state": "dirty"}, {"development": "false"}):
            with self.subTest(change=change), self.assertRaises(ZHVpnExecutableNotFound):
                self.verify({**data, **change})

    def test_pe_architecture_cannot_be_relabelled_in_manifest(self):
        data = {**self.data, "target": "windows-arm64"}
        with self.assertRaises(ZHVpnExecutableNotFound):
            self.verify(data, machine="ARM64")

    def test_direct_wheel_builder_cannot_bypass_distribution_refusal(self):
        source = Path(__file__).resolve().parents[1] / "setup.py"
        code = "import runpy,sys; sys.argv=['setup.py','bdist_wheel']; runpy.run_path(" + repr(str(source)) + ",run_name='__main__')"
        completed = subprocess.run([sys.executable, "-c", code], cwd=self.image.parent,
                                   capture_output=True, text=True, timeout=30)
        self.assertNotEqual(completed.returncode, 0)
        self.assertIn("wheel distribution is disabled", completed.stderr)
        self.assertFalse((self.image.parent / "dist").exists())


if __name__ == "__main__":
    unittest.main()
