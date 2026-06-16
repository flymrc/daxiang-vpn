from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
from pathlib import Path
from typing import Any, Dict, Mapping, Optional, Sequence, Union

from .errors import (
    ZHVpnCommandError,
    ZHVpnExecutableNotFound,
    ZHVpnJSONError,
    ZHVpnTimeout,
)
from .models import ActionResult, LoginResult, RotateResult, Status, VersionResult

PathLike = Union[str, os.PathLike]

_TOKEN_RE = re.compile(r"ZH-[A-Za-z0-9_-]+")


class Client:
    """Control Zongheng VPN by invoking the local zhvpn CLI."""

    def __init__(
        self,
        exe_path: Optional[PathLike] = None,
        *,
        command: Optional[Sequence[PathLike]] = None,
        timeout: float = 30.0,
        env: Optional[Mapping[str, str]] = None,
        cwd: Optional[PathLike] = None,
    ):
        if exe_path is not None and command is not None:
            raise ValueError("provide either exe_path or command, not both")
        self.timeout = timeout
        self.env = dict(env or {})
        self.cwd = os.fspath(cwd) if cwd is not None else None
        self._command = self._resolve_command(exe_path, command)

    @property
    def command(self) -> Sequence[str]:
        return tuple(self._command)

    def login(self, token: str, *, timeout: Optional[float] = None) -> LoginResult:
        data = self._run_json(["login", token, "--json"], timeout=timeout)
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
        if down_seconds is not None:
            args.extend(["--down-seconds", str(down_seconds)])
        if wait_seconds is not None:
            args.extend(["--wait-seconds", str(wait_seconds)])
        data = self._run_json(args, timeout=timeout)
        return RotateResult.from_dict(data)

    def logout(self, *, timeout: Optional[float] = None) -> LoginResult:
        data = self._run_json(["logout", "--json"], timeout=timeout)
        return LoginResult.from_dict(data)

    def version(self, *, timeout: Optional[float] = None) -> VersionResult:
        data = self._run_json(["version", "--json"], timeout=timeout)
        return VersionResult.from_dict(data)

    def proxy_url(self, proxy: Optional[str] = None) -> str:
        proxy = proxy or self.status().proxy
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
    ) -> Dict[str, Any]:
        completed = self._run(args, timeout=timeout)
        payload = self._parse_json(completed, args)
        if check and (completed.returncode != 0 or payload.get("ok") is False):
            raise self._command_error(args, completed, payload)
        return payload

    def _run(self, args: Sequence[str], *, timeout: Optional[float] = None) -> subprocess.CompletedProcess:
        full_command = [*self._command, *map(str, args)]
        env = os.environ.copy()
        env.update(self.env)
        effective_timeout = self.timeout if timeout is None else timeout
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
            )
        except FileNotFoundError as exc:
            raise ZHVpnExecutableNotFound(f"zhvpn executable not found: {self._command[0]}") from exc
        except subprocess.TimeoutExpired as exc:
            raise ZHVpnTimeout(self._redact_command(args), effective_timeout) from exc

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
                stdout=self._safe_text(completed.stdout),
                stderr=self._safe_text(completed.stderr),
            )
        try:
            payload = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ZHVpnJSONError(
                f"zhvpn returned invalid JSON: {exc}",
                command=self._redact_command(args),
                returncode=completed.returncode,
                stdout=self._safe_text(completed.stdout),
                stderr=self._safe_text(completed.stderr),
            ) from exc
        if not isinstance(payload, dict):
            raise ZHVpnJSONError(
                "zhvpn JSON response must be an object",
                command=self._redact_command(args),
                returncode=completed.returncode,
                stdout=self._safe_text(completed.stdout),
                stderr=self._safe_text(completed.stderr),
            )
        return payload

    def _command_error(
        self,
        args: Sequence[str],
        completed: subprocess.CompletedProcess,
        payload: Dict[str, Any],
    ) -> ZHVpnCommandError:
        message = str(payload.get("error") or completed.stderr or completed.stdout or "zhvpn command failed")
        return ZHVpnCommandError(
            self._safe_text(message),
            command=self._redact_command(args),
            returncode=completed.returncode,
            stdout=self._safe_text(completed.stdout),
            stderr=self._safe_text(completed.stderr),
            payload=self._safe_payload(payload),
        )

    def _redact_command(self, args: Sequence[str]) -> Sequence[str]:
        redacted = list(self._command)
        previous = ""
        for arg in map(str, args):
            if previous == "login" or _TOKEN_RE.fullmatch(arg):
                redacted.append("<redacted>")
            else:
                redacted.append(arg)
            previous = arg
        return redacted

    def _safe_text(self, value: Optional[str]) -> str:
        return _TOKEN_RE.sub("ZH-<redacted>", value or "")

    def _safe_payload(self, value: Any) -> Any:
        if isinstance(value, str):
            return self._safe_text(value)
        if isinstance(value, dict):
            return {key: self._safe_payload(item) for key, item in value.items()}
        if isinstance(value, list):
            return [self._safe_payload(item) for item in value]
        return value

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

        for name in ("zhvpn.exe", "zhvpn"):
            found = shutil.which(name)
            if found:
                return [found]

        for candidate in self._windows_candidates():
            if candidate.exists():
                return [str(candidate)]

        raise ZHVpnExecutableNotFound(
            "could not find zhvpn; set ZHVPN_EXE or pass Client(exe_path=...)"
        )

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
