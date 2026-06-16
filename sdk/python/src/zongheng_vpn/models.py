from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Dict, Optional


def _string(data: Dict[str, Any], key: str) -> Optional[str]:
    value = data.get(key)
    if value is None:
        return None
    return str(value)


@dataclass(frozen=True)
class LoginResult:
    ok: bool
    egress: Optional[str] = None
    proxy: Optional[str] = None
    error: Optional[str] = None
    raw: Dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "LoginResult":
        return cls(
            ok=bool(data.get("ok", False)),
            egress=_string(data, "egress"),
            proxy=_string(data, "proxy"),
            error=_string(data, "error"),
            raw=dict(data),
        )


@dataclass(frozen=True)
class ActionResult:
    ok: bool
    message: str = ""
    egress: Optional[str] = None
    proxy: Optional[str] = None
    warning: Optional[str] = None
    error: Optional[str] = None
    raw: Dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "ActionResult":
        return cls(
            ok=bool(data.get("ok", False)),
            message=str(data.get("message") or ""),
            egress=_string(data, "egress"),
            proxy=_string(data, "proxy"),
            warning=_string(data, "warning"),
            error=_string(data, "error"),
            raw=dict(data),
        )


@dataclass(frozen=True)
class Status:
    running: bool
    proxy_reachable: bool
    proxy: Optional[str] = None
    egress: Optional[str] = None
    egress_ip: Optional[str] = None
    egress_ipv4: Optional[str] = None
    egress_ipv6: Optional[str] = None
    error: Optional[str] = None
    raw: Dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "Status":
        return cls(
            running=bool(data.get("running", False)),
            proxy_reachable=bool(data.get("proxy_reachable", False)),
            proxy=_string(data, "proxy"),
            egress=_string(data, "egress"),
            egress_ip=_string(data, "egress_ip"),
            egress_ipv4=_string(data, "egress_ipv4"),
            egress_ipv6=_string(data, "egress_ipv6"),
            error=_string(data, "error"),
            raw=dict(data),
        )


@dataclass(frozen=True)
class RotateResult:
    ok: bool
    status: Optional[str] = None
    message: str = ""
    before: Optional[str] = None
    after: Optional[str] = None
    egress: Optional[str] = None
    error: Optional[str] = None
    raw: Dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "RotateResult":
        return cls(
            ok=bool(data.get("ok", False)),
            status=_string(data, "status"),
            message=str(data.get("message") or ""),
            before=_string(data, "before"),
            after=_string(data, "after"),
            egress=_string(data, "egress"),
            error=_string(data, "error"),
            raw=dict(data),
        )


@dataclass(frozen=True)
class VersionResult:
    ok: bool
    version: Optional[str] = None
    error: Optional[str] = None
    raw: Dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "VersionResult":
        return cls(
            ok=bool(data.get("ok", False)),
            version=_string(data, "version"),
            error=_string(data, "error"),
            raw=dict(data),
        )
