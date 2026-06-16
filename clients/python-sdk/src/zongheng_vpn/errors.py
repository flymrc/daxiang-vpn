from __future__ import annotations

from typing import Any, Dict, Optional, Sequence


class ZHVpnError(Exception):
    """Base class for SDK errors."""


class ZHVpnExecutableNotFound(ZHVpnError):
    """Raised when the SDK cannot locate the zhvpn CLI."""


class ZHVpnTimeout(ZHVpnError):
    """Raised when a CLI command times out."""

    def __init__(self, command: Sequence[str], timeout: float):
        self.command = list(command)
        self.timeout = timeout
        super().__init__(f"zhvpn command timed out after {timeout}s: {' '.join(self.command)}")


class ZHVpnCommandError(ZHVpnError):
    """Raised when the CLI exits non-zero or returns ok=false."""

    def __init__(
        self,
        message: str,
        *,
        command: Optional[Sequence[str]] = None,
        returncode: Optional[int] = None,
        stdout: str = "",
        stderr: str = "",
        payload: Optional[Dict[str, Any]] = None,
    ):
        self.command = list(command or [])
        self.returncode = returncode
        self.stdout = stdout
        self.stderr = stderr
        self.payload = dict(payload or {})
        super().__init__(message)


class ZHVpnJSONError(ZHVpnCommandError):
    """Raised when a --json CLI command does not return valid JSON."""

