from __future__ import annotations

import json
import hashlib
import math
import os
import platform
import re
import shutil
import subprocess
from pathlib import Path
from typing import Any, Dict, Mapping, Optional, Sequence, Union

from .errors import (
    ZHVpnCommandError,
    ZHVpnContractError,
    ZHVpnExecutableNotFound,
    ZHVpnJSONError,
    ZHVpnTimeout,
)
from .contracts import PRIVATE_FIELD_NAMES, SCHEMA_ID, validate_payload
from .models import ActionResult, LoginResult, RotateResult, Status, SystemProxyResult, VersionResult

PathLike = Union[str, os.PathLike]

_TOKEN_RE = re.compile(r"ZH-[A-Za-z0-9_-]+")


class Client:
    """Control Zongheng VPN by invoking the local zhvpn CLI."""

    def __init__(
        self,
        exe_path: Optional[PathLike] = None,
        *,
        command: Optional[Sequence[PathLike]] = None,
        timeout: Optional[float] = None,
        env: Optional[Mapping[str, str]] = None,
        cwd: Optional[PathLike] = None,
    ):
        if exe_path is not None and command is not None:
            raise ValueError("provide either exe_path or command, not both")
        self._explicit_timeout = timeout is not None
        self.timeout = 30.0 if timeout is None else self._validated_timeout(timeout)
        self.env = dict(env or {})
        self.cwd = os.fspath(cwd) if cwd is not None else None
        self._command = self._resolve_command(exe_path, command)

    @property
    def command(self) -> Sequence[str]:
        return tuple(self._command)

    def login(self, token: str, *, timeout: Optional[float] = None) -> LoginResult:
        if not isinstance(token, str) or not token.strip() or len(token.encode("utf-8")) > 4096 or any(c in token for c in "\r\n\x00"):
            raise ValueError("token must be a nonempty single line of at most 4096 UTF-8 bytes")
        data = self._run_json(["login", "--token-stdin", "--json"], timeout=timeout, input_text=token.strip())
        return LoginResult.from_dict(data)

    def connect(
        self,
        *,
        fast: bool = False,
        port: Optional[int] = None,
        timeout: Optional[float] = None,
    ) -> ActionResult:
        args = ["start"]
        if fast:
            args.append("--fast")
        if port is not None:
            args.extend(["--port", str(port)])
        args.append("--json")
        data = self._run_json(args, timeout=timeout)
        return ActionResult.from_dict(data)

    def disconnect(self, *, timeout: Optional[float] = None) -> ActionResult:
        data = self._run_json(["stop", "--json"], timeout=timeout)
        return ActionResult.from_dict(data)

    def status(self, *, check_ip: bool = False, timeout: Optional[float] = None) -> Status:
        args = ["status", "--json"]
        if not check_ip:
            args.append("--no-ip-check")
        data = self._run_json(args, timeout=timeout, check=False)
        return Status.from_dict(data)

    def status_ip(self, *, timeout: Optional[float] = None) -> Status:
        return self.status(check_ip=True, timeout=timeout)

    def rotate_ip(
        self,
        *,
        down_seconds: Optional[int] = None,
        wait_seconds: Optional[int] = None,
        timeout: Optional[float] = None,
    ) -> RotateResult:
        args = ["rotate-ip", "--json"]
        for name, value in (("down_seconds", down_seconds), ("wait_seconds", wait_seconds)):
            if value is not None and (isinstance(value, bool) or not isinstance(value, int) or value < 1):
                raise ValueError(f"{name} must be a positive integer")
        if down_seconds is not None and down_seconds > 60:
            raise ValueError("down_seconds must not exceed 60")
        if down_seconds is not None:
            args.extend(["--down-seconds", str(down_seconds)])
        if wait_seconds is not None:
            args.extend(["--wait-seconds", str(wait_seconds)])
        if timeout is None and not self._explicit_timeout:
            # CLI's normal radio recovery wait is 75 seconds, before/after
            # probes and Hub control add time. This is a caller wait budget,
            # not a guarantee of completion or a durable operation protocol.
            timeout = max(180.0, float(wait_seconds or 75) + 90.0)
        data = self._run_json(args, timeout=timeout)
        return RotateResult.from_dict(data)

    def logout(self, *, timeout: Optional[float] = None) -> LoginResult:
        data = self._run_json(["logout", "--json"], timeout=timeout)
        return LoginResult.from_dict(data)

    def version(self, *, timeout: Optional[float] = None) -> VersionResult:
        data = self._run_json(["version", "--json"], timeout=timeout)
        return VersionResult.from_dict(data)

    def system_proxy_acquire(self, *, timeout: Optional[float] = None) -> SystemProxyResult:
        data = self._run_json(["system-proxy", "acquire", "--json"], timeout=timeout)
        return SystemProxyResult.from_dict(data, expected="acquired")

    def system_proxy_release(self, lease_id: str, *, timeout: Optional[float] = None) -> SystemProxyResult:
        if not isinstance(lease_id, str) or re.fullmatch(r"[0-9a-f]{32}", lease_id) is None:
            raise ValueError("lease_id must be the 32-character lowercase hex ID returned by acquire")
        data = self._run_json(["system-proxy", "release", "--lease-id", lease_id, "--json"], timeout=timeout)
        result = SystemProxyResult.from_dict(data, expected="released")
        if result.lease_id is not None and result.lease_id != lease_id:
            raise ZHVpnContractError(
                "zhvpn system-proxy release returned a different lease",
                field_path="$.lease_id", schema_id=SCHEMA_ID,
            )
        return result

    def system_proxy_inspect(self, *, timeout: Optional[float] = None) -> SystemProxyResult:
        data = self._run_json(["system-proxy", "inspect", "--json"], timeout=timeout)
        return SystemProxyResult.from_dict(data, expected="inspect")

    def system_proxy_recover(self, *, timeout: Optional[float] = None) -> SystemProxyResult:
        data = self._run_json(["system-proxy", "recover", "--json"], timeout=timeout)
        return SystemProxyResult.from_dict(data, expected="recovered")

    def proxy_url(self, proxy: Optional[str] = None) -> str:
        if proxy is None:
            status = self.status()
            if (
                status.evidence.engine_ready is not True
                or status.evidence.proxy_tcp_reachable is not True
                or status.error is not None or status.error_code is not None
            ):
                raise ZHVpnCommandError(
                    "cannot select the configured proxy without a ready authenticated engine and reachable local proxy",
                    payload={"error_code": "proxy_readiness_unverified"},
                )
            proxy = status.proxy
        if not proxy:
            raise ZHVpnCommandError("zhvpn proxy address is unavailable")
        if "://" in proxy:
            return proxy
        return f"http://{proxy}"

    def proxies(self, proxy: Optional[str] = None) -> Dict[str, str]:
        url = self.proxy_url(proxy)
        return {"http": url, "https": url}

    def request(self, method: str, url: str, **kwargs: Any) -> Any:
        if "proxies" not in kwargs:
            kwargs["proxies"] = self.proxies()
        try:
            import requests
        except ImportError as exc:
            raise RuntimeError("install zongheng-vpn[requests] or use proxies() directly") from exc
        return requests.request(method, url, **kwargs)

    def get(self, url: str, **kwargs: Any) -> Any:
        return self.request("GET", url, **kwargs)

    def post(self, url: str, **kwargs: Any) -> Any:
        return self.request("POST", url, **kwargs)

    def _run_json(
        self,
        args: Sequence[str],
        *,
        timeout: Optional[float] = None,
        check: bool = True,
        input_text: Optional[str] = None,
    ) -> Dict[str, Any]:
        completed = self._run(args, timeout=timeout) if input_text is None else self._run(args, timeout=timeout, input_text=input_text)
        payload = self._parse_json(completed, args)
        try:
            validate_payload("Status" if args and args[0] == "status" else "Result", payload)
            if len(args) > 1 and args[0] == "system-proxy" and payload.get("ok") is True and completed.returncode == 0:
                expected = {"acquire": "acquired", "release": "released", "recover": "recovered", "inspect": "inspect"}.get(args[1])
                if expected is not None:
                    receipt = SystemProxyResult.from_dict(payload, expected=expected)
                    if args[1] == "release" and "--lease-id" in args:
                        requested = args[args.index("--lease-id") + 1]
                        if receipt.lease_id is not None and receipt.lease_id != requested:
                            raise ZHVpnContractError("zhvpn release returned a different lease", field_path="$.lease_id", schema_id=SCHEMA_ID)
            if (
                args and args[0] == "status" and "engine_state" in payload
                and completed.returncode != 0 and not payload.get("error")
            ):
                raise ZHVpnContractError(
                    "zhvpn contract violation: unsuccessful status exit has no diagnostic",
                    field_path="$.error", schema_id=SCHEMA_ID,
                )
        except ZHVpnContractError as exc:
            secrets = [*self._secret_values(payload, args), *([input_text] if input_text else [])]
            raise ZHVpnContractError(
                str(exc),
                field_path=exc.field_path,
                schema_id=exc.schema_id,
                command=self._redact_command(args),
                returncode=completed.returncode,
                stdout=self._safe_output(completed.stdout, payload, secrets),
                stderr=self._safe_text(completed.stderr, secrets),
                payload=self._safe_payload(payload, secrets),
            ) from None
        if check and (completed.returncode != 0 or payload.get("ok") is False):
            raise self._command_error(args, completed, payload, input_text=input_text)
        return self._safe_payload(payload, [*self._secret_values(payload, args), input_text]) if input_text else payload

    def _run(self, args: Sequence[str], *, timeout: Optional[float] = None, input_text: Optional[str] = None) -> subprocess.CompletedProcess:
        full_command = [*self._command, *map(str, args)]
        env = os.environ.copy()
        env.update(self.env)
        effective_timeout = self._validated_timeout(self.timeout if timeout is None else timeout)
        try:
            return subprocess.run(
                full_command,
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                timeout=effective_timeout,
                cwd=self.cwd,
                env=env,
                input=input_text,
            )
        except FileNotFoundError as exc:
            raise ZHVpnExecutableNotFound(f"zhvpn executable not found: {self._command[0]}") from exc
        except subprocess.TimeoutExpired as exc:
            # The original exception embeds the unredacted full command.
            readonly = bool(args) and (args[0] in {"status", "version"} or list(args[:2]) == ["system-proxy", "inspect"])
            raise ZHVpnTimeout(self._redact_command(args), effective_timeout, result_unknown=not readonly) from None

    @staticmethod
    def _validated_timeout(value: float) -> float:
        if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or value <= 0:
            raise ValueError("timeout must be a finite positive number")
        return float(value)

    def _parse_json(self, completed: subprocess.CompletedProcess, args: Sequence[str]) -> Dict[str, Any]:
        line = ""
        for candidate in reversed((completed.stdout or "").splitlines()):
            candidate = candidate.strip()
            if candidate:
                line = candidate
                break
        if not line:
            raise ZHVpnJSONError(
                "zhvpn did not return JSON",
                command=self._redact_command(args),
                returncode=completed.returncode,
                # Invalid/no JSON cannot be safely inspected for private keys.
                # Preserve command/exit diagnostics without echoing raw output.
                stdout="",
                stderr="",
            )
        try:
            payload = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ZHVpnJSONError(
                f"zhvpn returned invalid JSON: {exc}",
                command=self._redact_command(args),
                returncode=completed.returncode,
                stdout="",
                stderr="",
            ) from None
        if not isinstance(payload, dict):
            raise ZHVpnJSONError(
                "zhvpn JSON response must be an object",
                command=self._redact_command(args),
                returncode=completed.returncode,
                stdout="",
                stderr="",
            )
        return payload

    def _command_error(
        self,
        args: Sequence[str],
        completed: subprocess.CompletedProcess,
        payload: Dict[str, Any],
        *,
        input_text: Optional[str] = None,
    ) -> ZHVpnCommandError:
        message = str(payload.get("error") or completed.stderr or completed.stdout or "zhvpn command failed")
        secrets = [*self._secret_values(payload, args), *([input_text] if input_text else [])]
        return ZHVpnCommandError(
            self._safe_text(message, secrets),
            command=self._redact_command(args),
            returncode=completed.returncode,
            stdout=self._safe_output(completed.stdout, payload, secrets),
            stderr=self._safe_text(completed.stderr, secrets),
            payload=self._safe_payload(payload, secrets),
        )

    def _redact_command(self, args: Sequence[str]) -> Sequence[str]:
        redacted = list(self._command)
        previous = ""
        for arg in map(str, args):
            if (previous == "login" and not arg.startswith("--")) or _TOKEN_RE.fullmatch(arg):
                redacted.append("<redacted>")
            else:
                redacted.append(arg)
            previous = arg
        return redacted

    def _safe_text(self, value: Optional[str], secrets: Sequence[str] = ()) -> str:
        result = _TOKEN_RE.sub("ZH-<redacted>", value or "")
        for secret in secrets:
            if secret:
                result = result.replace(secret, "<redacted>")
        return result

    def _safe_payload(self, value: Any, secrets: Sequence[str] = ()) -> Any:
        if isinstance(value, str):
            return self._safe_text(value, secrets)
        if isinstance(value, dict):
            return {
                key: "<redacted>" if key.lower() in PRIVATE_FIELD_NAMES else self._safe_payload(item, secrets)
                for key, item in value.items()
            }
        if isinstance(value, list):
            return [self._safe_payload(item, secrets) for item in value]
        return value

    def _secret_values(self, payload: Any, args: Sequence[str]) -> Sequence[str]:
        secrets = []
        if args and args[0] == "login" and len(args) > 1 and not str(args[1]).startswith("--"):
            secrets.append(str(args[1]))
        def visit(value: Any) -> None:
            if isinstance(value, dict):
                for key, item in value.items():
                    if key.lower() in PRIVATE_FIELD_NAMES and isinstance(item, str):
                        secrets.append(item)
                    else:
                        visit(item)
            elif isinstance(value, list):
                for item in value:
                    visit(item)
        visit(payload)
        return secrets

    def _safe_output(self, output: Optional[str], payload: Dict[str, Any], secrets: Sequence[str] = ()) -> str:
        # A rejected response can contain forbidden private fields. Never copy
        # their original values into exception stdout, even in diagnostic mode.
        return json.dumps(self._safe_payload(payload, secrets), ensure_ascii=False)

    def _resolve_command(
        self,
        exe_path: Optional[PathLike],
        command: Optional[Sequence[PathLike]],
    ) -> Sequence[str]:
        if command is not None:
            if not command:
                raise ValueError("command cannot be empty")
            return [os.fspath(part) for part in command]
        if exe_path is not None:
            return [os.fspath(exe_path)]

        env_exe = os.environ.get("ZHVPN_EXE")
        if env_exe:
            return [env_exe]

        bundled = self._bundled_cli()
        if bundled is not None:
            return [str(bundled)]

        for name in ("zhvpn.exe", "zhvpn"):
            found = shutil.which(name)
            if found:
                return [found]

        for candidate in self._windows_candidates():
            if candidate.exists():
                return [str(candidate)]

        raise ZHVpnExecutableNotFound(
            "could not find bundled zhvpn; run sdk/python/build.ps1, set ZHVPN_EXE, "
            "or pass Client(exe_path=...)"
        )

    def _bundled_cli(self) -> Optional[Path]:
        names = ("zhvpn.exe", "zhvpn")
        base = Path(__file__).resolve().parent / "bin"
        for name in names:
            candidate = base / name
            if candidate.exists():
                self._verify_bundle(candidate, base / "build-manifest.json")
                return candidate
        return None

    @staticmethod
    def _verify_bundle(candidate: Path, manifest_path: Path) -> None:
        # This checks build identity and corruption, not publisher trust. A
        # formal signed wheel/update chain has a separate, uncompleted gate.
        try:
            if candidate.is_symlink() or manifest_path.is_symlink() or not candidate.is_file():
                raise ValueError()
            if manifest_path.stat().st_size > 16384 or candidate.stat().st_size > 128 * 1024 * 1024:
                raise ValueError()
            def unique_pairs(pairs):
                result = {}
                for key, value in pairs:
                    if key in result:
                        raise ValueError()
                    result[key] = value
                return result
            data = json.loads(manifest_path.read_text(encoding="utf-8-sig"), object_pairs_hook=unique_pairs)
            for key, expected in (("schema_version", 1), ("cli_contract_version", 1), ("control_protocol_version", 1), ("sidecar_protocol_version", 2)):
                if type(data.get(key)) is not int or data[key] != expected:
                    raise ValueError()
            host_arch = {"amd64": "amd64", "x86_64": "amd64", "arm64": "arm64", "aarch64": "arm64"}.get(platform.machine().lower())
            if (platform.system() != "Windows" or host_arch is None
                    or data.get("schema_version") != 1 or data.get("product") != "python-sdk"
                    or data.get("target") != "windows-" + host_arch
                    or data.get("cli_contract_version") != 1 or data.get("control_protocol_version") != 1
                    or data.get("sidecar_protocol_version") != 2
                    or not isinstance(data.get("source_commit"), str)
                    or re.fullmatch(r"[a-f0-9]{40}", data["source_commit"]) is None
                    or not isinstance(data.get("sha256"), str)
                    or re.fullmatch(r"[a-f0-9]{64}", data["sha256"]) is None):
                raise ValueError()
            if data.get("development") is True:
                if data.get("signed") is not False or data.get("source_state") not in {"clean", "dirty"} or os.environ.get("ZHVPN_ALLOW_DEVELOPMENT_BUNDLE") != "1":
                    raise ValueError()
            elif data.get("development") is not False or data.get("signed") is not True or data.get("source_state") != "clean":
                raise ValueError()
            digest = hashlib.sha256()
            with candidate.open("rb") as image:
                header = image.read(64)
                if len(header) != 64 or header[:2] != b"MZ":
                    raise ValueError()
                offset = int.from_bytes(header[60:64], "little")
                if not 64 <= offset <= 1024 * 1024:
                    raise ValueError()
                image.seek(offset)
                pe = image.read(6)
                if pe[:4] != b"PE\x00\x00" or int.from_bytes(pe[4:6], "little") != {"amd64": 0x8664, "arm64": 0xAA64}[host_arch]:
                    raise ValueError()
                image.seek(0)
                for chunk in iter(lambda: image.read(65536), b""):
                    digest.update(chunk)
            if digest.hexdigest() != data["sha256"]:
                raise ValueError()
        except (OSError, ValueError, TypeError, AttributeError):
            raise ZHVpnExecutableNotFound("Bundled CLI build identity, host target or SHA256 is unverified; use a verified bundle or an explicitly selected executable.") from None

    def _windows_candidates(self) -> Sequence[Path]:
        names = [
            Path("Programs") / "纵横 VPN" / "zhvpn.exe",
            Path("纵横 VPN") / "zhvpn.exe",
            Path("ZonghengVPN") / "zhvpn.exe",
        ]
        roots = [
            os.environ.get("LOCALAPPDATA"),
            os.environ.get("ProgramFiles"),
            os.environ.get("ProgramFiles(x86)"),
        ]
        candidates = []
        for root in roots:
            if not root:
                continue
            base = Path(root)
            candidates.extend(base / name for name in names)
        return candidates
