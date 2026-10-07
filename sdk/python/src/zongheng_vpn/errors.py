from __future__ import annotations

from typing import Any, Dict, Optional, Sequence


class ZHVpnError(Exception):
    """Base class for SDK errors."""


class ZHVpnExecutableNotFound(ZHVpnError):
    """Raised when the SDK cannot locate the zhvpn CLI."""


class ZHVpnTimeout(ZHVpnError):
    """The local wait expired; a mutating command may already have acted."""

    def __init__(self, command: Sequence[str], timeout: float, *, result_unknown: bool = False):
        self.command = list(command)
        self.timeout = timeout
        self.result_unknown = result_unknown
        self.error_code = "command_outcome_unknown" if result_unknown else "command_timeout"
        suffix = "; outcome is unknown; do not automatically repeat the operation" if result_unknown else ""
        super().__init__(f"zhvpn command timed out after {timeout}s: {' '.join(self.command)}{suffix}")


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
        self.error_code = self.payload.get("error_code")
        super().__init__(message)


class ZHVpnJSONError(ZHVpnCommandError):
    """Raised when a --json CLI command does not return valid JSON."""


class ZHVpnContractError(ZHVpnJSONError):
    """Valid JSON violates a supported public DTO or evidence invariant."""

    def __init__(self, message: str, *, field_path: str, schema_id: str, **kwargs: Any):
        self.field_path = field_path
        self.schema_id = schema_id
        super().__init__(message, **kwargs)
