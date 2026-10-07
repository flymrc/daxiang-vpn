from __future__ import annotations

from dataclasses import dataclass, field
import ipaddress
from typing import Any, Dict, Optional

from .contracts import CONTRACT_VERSION, CONTROL_PROTOCOL_VERSION, SCHEMA_ID, validate_payload
from .errors import ZHVpnContractError


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
    error_code: Optional[str] = None
    contract_version: Optional[int] = None

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "LoginResult":
        validate_payload("Result", data)
        return cls(
            ok=bool(data.get("ok", False)),
            egress=_string(data, "egress"),
            proxy=_string(data, "proxy"),
            error=_string(data, "error"),
            raw=dict(data),
            error_code=_string(data, "error_code"),
            contract_version=data.get("contract_version"),
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
    error_code: Optional[str] = None
    contract_version: Optional[int] = None

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "ActionResult":
        validate_payload("Result", data)
        return cls(
            ok=bool(data.get("ok", False)),
            message=str(data.get("message") or ""),
            egress=_string(data, "egress"),
            proxy=_string(data, "proxy"),
            warning=_string(data, "warning"),
            error=_string(data, "error"),
            raw=dict(data),
            error_code=_string(data, "error_code"),
            contract_version=data.get("contract_version"),
        )


@dataclass(frozen=True)
class StatusEvidence:
    """Evidence available in this response, without inferred health or time.

    None means unknown. A returned IP is an observation, not proof of the
    configured residential route or of a particular WireGuard tunnel.
    """

    engine_ready: Optional[bool]
    proxy_tcp_reachable: Optional[bool]
    tunnel_healthy: Optional[bool] = None
    egress_ip_observed: Optional[bool] = None
    ipv4_observation: Optional[str] = None
    ipv6_observation: Optional[str] = None
    verified_at: Optional[str] = None


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
    error_code: Optional[str] = None
    contract_version: Optional[int] = None
    engine_state: Optional[str] = None
    instance_id: Optional[str] = None
    config_generation: Optional[str] = None
    control_protocol_version: Optional[int] = None
    port_occupied: bool = False
    logging_state: Optional[str] = None
    logging_error_code: Optional[str] = None

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "Status":
        validate_payload("Status", data)
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
            error_code=_string(data, "error_code"),
            contract_version=data.get("contract_version"),
            engine_state=_string(data, "engine_state"),
            instance_id=_string(data, "instance_id"),
            config_generation=_string(data, "config_generation"),
            control_protocol_version=data.get("control_protocol_version"),
            port_occupied=data.get("port_occupied", False),
            logging_state=_string(data, "logging_state"),
            logging_error_code=_string(data, "logging_error_code"),
        )

    @property
    def evidence(self) -> StatusEvidence:
        engine = None
        if self.engine_state in {"stopped", "starting", "stopping", "degraded"}:
            engine = False
        elif (
            self.engine_state == "ready" and self.running and self.instance_id
            and self.config_generation and self.control_protocol_version == CONTROL_PROTOCOL_VERSION
        ):
            engine = True
        return StatusEvidence(
            engine_ready=engine,
            proxy_tcp_reachable=self.proxy_reachable if engine is True else None,
            egress_ip_observed=True if engine is True and self._has_ip_observation() else None,
            ipv4_observation=self.egress_ipv4 if engine is True else None,
            ipv6_observation=self.egress_ipv6 if engine is True else None,
        )

    def _has_ip_observation(self) -> bool:
        for value in (self.egress_ip, self.egress_ipv4, self.egress_ipv6):
            if value:
                try:
                    ipaddress.ip_address(value)
                    return True
                except ValueError:
                    pass
        return False


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
    error_code: Optional[str] = None
    contract_version: Optional[int] = None

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "RotateResult":
        validate_payload("Result", data)
        return cls(
            ok=bool(data.get("ok", False)),
            status=_string(data, "status"),
            message=str(data.get("message") or ""),
            before=_string(data, "before"),
            after=_string(data, "after"),
            egress=_string(data, "egress"),
            error=_string(data, "error"),
            raw=dict(data),
            error_code=_string(data, "error_code"),
            contract_version=data.get("contract_version"),
        )


@dataclass(frozen=True)
class VersionResult:
    ok: bool
    version: Optional[str] = None
    error: Optional[str] = None
    raw: Dict[str, Any] = field(default_factory=dict)
    error_code: Optional[str] = None
    contract_version: Optional[int] = None
    source_commit: Optional[str] = None
    source_state: Optional[str] = None
    go_version: Optional[str] = None

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "VersionResult":
        validate_payload("Result", data)
        return cls(
            ok=bool(data.get("ok", False)),
            version=_string(data, "version"),
            error=_string(data, "error"),
            raw=dict(data),
            error_code=_string(data, "error_code"),
            contract_version=data.get("contract_version"),
            source_commit=data.get("source_commit"),
            source_state=data.get("source_state"),
            go_version=data.get("go_version"),
        )


@dataclass(frozen=True)
class SystemProxyResult:
    """A CLI lease receipt; persisted state does not establish OS or IP health."""

    ok: bool
    system_proxy_state: str
    owned: bool
    noop: bool
    lease_id: Optional[str] = None
    journal_path: Optional[str] = None
    error_code: Optional[str] = None
    raw: Dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_dict(cls, data: Dict[str, Any], *, expected: str) -> "SystemProxyResult":
        validate_payload("Result", data)
        def refuse(field: str) -> None:
            raise ZHVpnContractError(
                "zhvpn system-proxy receipt is missing supported ownership evidence",
                field_path=f"$.{field}", schema_id=SCHEMA_ID,
            )
        if data.get("contract_version") != CONTRACT_VERSION:
            refuse("contract_version")
        state = data.get("system_proxy_state")
        allowed = {"absent", "recorded", "foreign"} if expected == "inspect" else {expected}
        if expected == "recovered":
            allowed.add("absent")
        if state not in allowed:
            refuse("system_proxy_state")
        if not isinstance(data.get("owned"), bool):
            refuse("owned")
        if not isinstance(data.get("noop"), bool):
            refuse("noop")
        if state == "acquired" and (not data["owned"] or not data.get("lease_id")):
            refuse("lease_id")
        if state in {"absent", "foreign"} and data["owned"]:
            refuse("owned")
        if state == "absent" and data.get("lease_id"):
            refuse("lease_id")
        if state == "absent" and not data["noop"]:
            refuse("noop")
        if state in {"recorded", "foreign"} and not data.get("lease_id"):
            refuse("lease_id")
        if state in {"released", "recovered"} and not data["noop"] and (not data["owned"] or not data.get("lease_id")):
            refuse("lease_id")
        return cls(
            ok=data["ok"], system_proxy_state=state, owned=data["owned"], noop=data["noop"],
            lease_id=data.get("lease_id"), journal_path=data.get("journal_path"),
            error_code=data.get("error_code"), raw=dict(data),
        )
