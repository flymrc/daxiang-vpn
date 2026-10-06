"""Validate the supported CLI schema without a runtime package dependency.

The schema subset interpreted here is generated from shared/contracts. Unknown
compatible fields are retained, but never used to strengthen health evidence.
"""
from __future__ import annotations

import ipaddress
import re
from typing import Any, Dict, Mapping

from ._contract_generated import (
    CONTRACT_VERSION,
    CONTROL_PROTOCOL_VERSION,
    DEFINITIONS,
    EVIDENCE_SEMANTICS,
    EngineIdentityDTO,
    PRIVATE_FIELD_NAMES,
    ResultDTO,
    SCHEMA_ID,
    StatusDTO,
)
from .errors import ZHVpnContractError


def _check_schema_supported(schema: Mapping[str, Any]) -> None:
    """Fail visibly if future generation uses a constraint we cannot enforce."""
    supported = {
        "type", "description", "properties", "required", "additionalProperties",
        "const", "enum", "minimum", "minLength", "pattern", "format",
        "allOf", "anyOf", "not", "if", "then",
    }
    unsupported = set(schema).difference(supported)
    if unsupported or schema.get("additionalProperties", True) is not True:
        raise ValueError("generated CLI schema uses unsupported validation constraints")
    if schema.get("format") not in {None, "ipv4", "ipv6"}:
        raise ValueError("generated CLI schema uses unsupported format")
    for child in schema.get("properties", {}).values():
        _check_schema_supported(child)
    for keyword in ("allOf", "anyOf"):
        for child in schema.get(keyword, ()):
            _check_schema_supported(child)
    for keyword in ("not", "if", "then"):
        if keyword in schema:
            _check_schema_supported(schema[keyword])


for _definition in DEFINITIONS.values():
    _check_schema_supported(_definition)


def _failure(path: str, message: str) -> ZHVpnContractError:
    # Include field location, not untrusted values or credentials.
    return ZHVpnContractError(
        f"zhvpn contract violation at {path}: {message}",
        field_path=path,
        schema_id=SCHEMA_ID,
    )


def _matches(schema: Mapping[str, Any], value: Any, path: str) -> bool:
    try:
        _validate(schema, value, path)
        return True
    except ZHVpnContractError:
        return False


def _same(left: Any, right: Any) -> bool:
    # Python's True == 1 must not satisfy a JSON integer/boolean constant.
    if isinstance(left, bool) != isinstance(right, bool):
        return False
    return left == right


def _validate(schema: Mapping[str, Any], value: Any, path: str) -> None:
    kind = schema.get("type")
    if kind == "object" and not isinstance(value, dict):
        raise _failure(path, "expected object")
    if kind == "boolean" and not isinstance(value, bool):
        raise _failure(path, "expected boolean")
    if kind == "string" and not isinstance(value, str):
        raise _failure(path, "expected string")
    if kind == "integer" and (
        isinstance(value, bool)
        or not isinstance(value, (int, float))
        or isinstance(value, float) and not value.is_integer()
    ):
        raise _failure(path, "expected integer")
    if "const" in schema and not _same(value, schema["const"]):
        raise _failure(path, "unsupported constant/version or contradictory state")
    if "enum" in schema and not any(_same(value, option) for option in schema["enum"]):
        raise _failure(path, "unsupported state")
    if "minimum" in schema and value < schema["minimum"]:
        raise _failure(path, "integer below minimum")
    if "minLength" in schema and (not isinstance(value, str) or len(value) < schema["minLength"]):
        raise _failure(path, "expected nonempty string")
    if "pattern" in schema and re.fullmatch(schema["pattern"], value) is None:
        raise _failure(path, "invalid identifier format")
    if "format" in schema:
        try:
            address = ipaddress.ip_address(value)
        except ValueError:
            raise _failure(path, "invalid IP observation") from None
        if (schema["format"] == "ipv4" and address.version != 4) or (
            schema["format"] == "ipv6" and address.version != 6
        ):
            raise _failure(path, "incorrect IP family")
    if isinstance(value, dict):
        for name in schema.get("required", ()):
            if name not in value:
                raise _failure(f"{path}.{name}", "required field missing")
        for name, child_schema in schema.get("properties", {}).items():
            if name in value:
                _validate(child_schema, value[name], f"{path}.{name}")
    for child_schema in schema.get("allOf", ()):
        _validate(child_schema, value, path)
    if "anyOf" in schema and not any(_matches(option, value, path) for option in schema["anyOf"]):
        raise _failure(path, "required compatible alternative missing")
    if "not" in schema and _matches(schema["not"], value, path):
        raise _failure(path, "forbidden or contradictory fields")
    if "if" in schema and _matches(schema["if"], value, path):
        _validate(schema.get("then", {}), value, path)


def validate_payload(kind: str, payload: Dict[str, Any]) -> None:
    """Validate Status, Result or EngineIdentity against supported v1 semantics.

    Missing contract_version is accepted for existing CLI releases. Present
    incompatible versions and malformed known fields fail with a field path.
    """
    if kind not in DEFINITIONS:
        raise ValueError(f"unknown public CLI DTO: {kind}")
    if isinstance(payload, dict):
        for name in PRIVATE_FIELD_NAMES:
            if name in payload:
                raise _failure(f"$.{name}", "private storage field is not public JSON")
    _validate(DEFINITIONS[kind], payload, "$")


__all__ = [
    "CONTRACT_VERSION",
    "CONTROL_PROTOCOL_VERSION",
    "EVIDENCE_SEMANTICS",
    "EngineIdentityDTO",
    "ResultDTO",
    "SCHEMA_ID",
    "StatusDTO",
    "validate_payload",
]
