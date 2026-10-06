# CLI public JSON contract

`source.go` is the maintenance source for the current CLI `Status`, `Result`
and authenticated `EngineIdentity`. It defines fields, compatibility rules and
evidence boundaries. It does not define private launch/state records, Hub APIs,
GUI proxy ownership or a future durable operation protocol.

Run from the root Go module:

```powershell
go generate ./shared/contracts
go run ./shared/contracts/cmd/contractgen -check
go test ./shared/contracts
python -m unittest discover -s sdk/python/tests -v
```

Generation writes these deterministic projections. Do not edit them directly:

- `cli_v1_generated.go`: Go `Status`, `Result`, `EngineIdentity` DTOs.
- `cli-v1.schema.json`: JSON Schema draft 2020-12, with command-specific `$defs`.
- `cli_v1_generated.ts`: TypeScript `StatusDTO`, `ResultDTO`, `EngineIdentityDTO`.
- `clients/desktop-gui/src/lib/contracts.generated.ts`: identical TypeScript
  projection inside the GUI build root, maintained by the same generator.
- `sdk/python/src/zongheng_vpn/_contract_generated.py`: Python TypedDict DTOs,
  schema validation data and evidence descriptions.

Public emitters set `contract_version=1`. Its absence remains compatible with
existing CLI releases. `control_protocol_version=1` identifies local control
authentication. `protocol_version` on the version command identifies the
existing binary/sidecar identity protocol. They are separate protocols.

The schema permits compatible extra properties and rejects known field type,
identity, version and state contradictions. Extras cannot strengthen evidence
until explicitly supported by the contract and implementation. The shared
allow/reject corpus is `fixtures/cli-v1.json`; the Python consumer validates the
same corpus. Decode errors retain field paths and the CLI diagnostic code.

The evidence tiers remain distinct:

| Observation | Meaning and limit |
| --- | --- |
| `engine_state=ready`, `running=true`, complete control identity | This local instance completed data-plane start and authenticated launcher activation. |
| `proxy_reachable=true` | A TCP connection succeeded to the ready local instance's proxy. HTTP forwarding, WireGuard and egress health remain separate. |
| `ready` with `proxy_reachable=false` | Valid partial health: the instance authenticated but its local port is currently unavailable. |
| `egress` | Configured cached label, not a verified residential route. |
| Optional IP fields | Observations from this command's optional proxy probe. No timestamp or chosen-route authentication is supplied. |
| Missing family/IP/tunnel/timestamp | Unknown. No healthy/unhealthy or freshness verdict may be inferred. |

v1 exposes no `verified_at`, `observed_at`, tunnel health or operation state.
Consumers must not synthesize a verification time from decode time, interpret
an unowned TCP listener as this engine, or reuse old IP observations as current
verification. Python's `Status.evidence` provides a conservative read-only view;
existing `Status.running`, `proxy_reachable` and IP fields retain their meanings.
