# Reverse TCP TLS contract

Status: implemented and tested locally; not deployed. The 2026-10-06 production
baseline still uses legacy TCP/yamux with a shared token. This document does not
authorize a listener, firewall, service, supervisor or phone change.

## Protocol and compatibility

`transport: tcp-tls` is an explicit secure transport selection. It uses TLS 1.3
only, ALPN `zhreverse/2`, mutually verified certificates and the existing yamux
CONNECT/FETCH/BENCH/STRIPED_CONNECT data plane. After mutual TLS the phone sends
exactly `ZHREV2\n`; the Hub rechecks current authorization and replies exactly
`OK ZHREV2\n` before yamux. The hello carries no token. Application commands are
bounded to 4096 bytes on this path. Authentication, CA, hostname, role, registry
or ALPN failures close the connection; there is no raw-TCP or QUIC retry.

Legacy `tcp` and experimental `quic` remain separate explicit selections with
their old protocol. Existing production configs do not enable TLS by loading
this source. Existing transport defaults remain unchanged during migration;
the intended production target is `tcp-tls` and legacy retirement needs its own
canary and consumer evidence. Do not run both transports on the same listener.
Future production addresses/ports belong in an approved change sheet, not here.

Secure mode rejects `token`, `token_file`, `insecure_skip_verify` and QUIC
`server_cert_sha256` settings. No self-signed certificate is generated for this
path. A configured trust file is mandatory, and only PEM CA certificates are
accepted. TLS session tickets/resumption are disabled. Standard certificate
chain/expiry verification runs before additional identity checks.

## Certificate identities

Each leaf has exactly one URI SAN and exactly one dedicated EKU:

| Role | URI | EKU |
| --- | --- | --- |
| Hub | `spiffe://zhvpn/hub/<hub-id>` | `serverAuth` |
| Egress | `spiffe://zhvpn/egress/<egress-id>` | `clientAuth` |

IDs contain 1–64 ASCII letters, digits, hyphens or underscores. This URI format
is an identity convention; it does not claim a deployed SPIFFE control plane.
Common names do not identify a peer. The phone requires both its configured Hub
ID and the configured `tls_server_name` DNS/IP SAN, validated by its trust file.
A CA-issued egress certificate is insufficient: the Hub also requires the exact
egress identity and exact leaf DER SHA256 in its authorization registry.

Issue each egress a distinct private key/certificate. The phone's key stays on
that egress; the issuer receives a CSR, never the phone key. The running
transport reads externally provisioned credentials; this slice does not add a
certificate-enrollment API, distribute CA private keys or automatically grant
identities from Hub customer-device records.

## Configuration contract

Required Hub additions:

```yaml
server:
  transport: tcp-tls
  listen: <approved-secure-listener>
  proxy: <approved-phone-proxy-listener>
  resolve: client
  hub_id: <hub-id>
  tls_ca_file: <private-config-dir>/trust.pem
  tls_cert_file: <private-config-dir>/hub-bundle.pem
  tls_key_file: <private-config-dir>/hub-bundle.pem
  egress_registry_file: <private-config-dir>/egress-registry.json
  max_proxy_connections: 96
  max_proxy_connections_per_client: 48
  proxy_idle_timeout: 2m
  proxy_preempt_idle: 10s
```

Required phone additions:

```yaml
client:
  transport: tcp-tls
  server: <approved-secure-hub-address>
  hub_id: <hub-id>
  egress_id: <egress-id>
  tls_server_name: <Hub-certificate-DNS-or-IP-SAN>
  tls_ca_file: <private-config-dir>/trust.pem
  tls_cert_file: <private-config-dir>/phone-bundle.pem
  tls_key_file: <private-config-dir>/phone-bundle.pem
  connections: 2
  reconnect: 3s
  address_family: ipv6
```

The corresponding hyphenated flags are available on `server`/`client`, with
explicit flags overriding YAML. Existing interface-binding/fallback settings
remain available for TCP TLS; an interface fallback still uses TLS, not another
authentication mode. The Hub accepts at most two sessions per registered egress
and 256 total secure sessions. A separate limit permits at most 64 in-progress
TLS handshakes, each with a 10-second total authentication deadline.

For atomic certificate rotation, use the same pathname for `tls_cert_file` and
`tls_key_file`, containing both certificate chain and private-key PEM blocks.
That bundle is read once. Separate certificate/key files remain supported, but
a mismatched intermediate pair fails closed and can drop existing sessions.
Use a service-owned private directory, private-key/bundle mode 0600 and
directory mode 0700 on Linux/Android. Group/other-readable or foreign-owner
private keys, symlinks and hardlinked files are rejected there. Windows protocol
tests run on isolated files; Windows service private-key DACL provisioning is
not certified by this slice. CA/registry/watermark files must be service/root
owned and are rejected if group/other writable on Unix.
On Unix, each secure file's immediate parent must also be service-owned and
private (0700 recommended). Every directory ancestor must be service/root-owned
and prevent group/other namespace replacement; a root-owned sticky directory
such as `/tmp` may contain the private service directory. Parent symlinks are
rejected. This guards lock and watermark names as well as file contents: an
inode with mode 0600 alone does not prevent its parent from replacing it.

## Authorization registry v1

The registry is strict JSON (unknown fields, non-canonical field names,
duplicate members and trailing content are rejected), at most 1 MiB and 128
egresses. JSON property names must exactly match this schema at every depth;
case variants such as `Generation` are rejected in both the registry and
durable watermark. Example identities and
fingerprint below are placeholders, not real credentials:

```json
{
  "schema_version": 1,
  "generation": 1,
  "hub_id": "example-hub",
  "egresses": [{
    "egress_id": "example-phone",
    "state": "active",
    "credentials": [{
      "credential_id": "example-phone-key-1",
      "leaf_sha256": "<64-lowercase-hex-SHA256-of-leaf-DER>",
      "state": "active",
      "not_before": "2026-10-07T00:00:00Z",
      "expires_at": "2026-10-08T00:00:00Z"
    }]
  }]
}
```

Egress and credential states are `active`/`revoked`. Credential validity must be
within the certificate's validity and is half-open `[not_before, expires_at)`.
Each active egress has one or two credentials; a dual-credential validity
intersection cannot exceed 15 minutes. Fingerprints are globally unique and
credential IDs are unique within an egress. Rotation is a new independent key,
a new exact fingerprint and a new credential ID; never change the key behind
an unchanged fingerprint or treat issuance as authorization.

Initialize a new generation-1 registry once, offline:

```sh
zhreverse registry init \
  --egress-registry-file <private-config-dir>/egress-registry.json \
  --hub-id <hub-id>
```

Initialization acquires the registry owner lock and refuses existing durable
state or a non-initial generation. The secure server never implicitly
initializes state. Every accepted registry revision commits its generation and
exact file-byte digest to `egress-registry.json.accepted` before granting
authorization. Later changes require a strictly larger generation; a same
generation must be byte-identical. Lower generations and same-generation
mutations fail closed, including after restart. Missing/corrupt/unwritable
watermark fails closed. The live instance also retains its highest accepted
generation even if files are manually replaced.

One running secure listener owns a given registry via its `.lock` file, held
for its whole lifetime. Locks are released by process death and are never
deleted as part of normal operation. Registry parents are canonicalized; files
must be regular and single-link. Windows requires a local fixed drive and its
lock handle denies delete/rename sharing. Unix uses `flock` plus `O_NOFOLLOW`.
This is a single-host file authority, not a distributed consensus system.

Prepare a complete next JSON file, increment generation, then atomically
replace the registry. Retain revoked identities/credentials for operational
audit until their migration retention rules permit archival; this registry
itself is not a full historical ledger. CA roots may overlap temporarily for
issuer replacement. Apply new roots before new bundles/credentials; shorten
the old credential's expiry to a window of at most 15 minutes, verify the new
phone session, then revoke the old credential and retire old roots. This
requires coordinated phone and Hub provisioning, not a shared-token fallback.
The phone loads its key bundle on reconnect; replacing that file does not
silently replace keys on already authenticated sessions. With both allowed
sessions still using the old credential, a third canary session is rejected.
The change sheet must drain one old session or perform a controlled client
restart before checking the new credential, or explicitly wait for the bounded
old-credential expiry. Transparent live client rekeying is not claimed here.

Back up registry and accepted-state evidence. A supported recovery preserves
the newest accepted generation and merges subsequent revocations; do not
restore both files to an old grant or delete the watermark to bypass a
rejection. Host/root write access remains the trust boundary: rolling back all
trusted state or explicitly reinitializing a deleted registry can defeat local
rollback protection. No old customer WG or RDP peer is modified by this module.

## Existing sessions, health and budgets

The Hub reloads trust, its certificate bundle and authorization every second
and on new handshakes. Before session admission it checks current authorization
again, so a revoke between TLS verification and the hello cannot admit a late
session. CA removal, certificate expiry, credential expiry/revocation, identity
revocation or malformed/missing authorization closes existing TCP sockets and
all yamux streams. Socket closure precedes TLS close-notify and yamux cleanup,
so a non-reading phone cannot defer revocation to the 30-second write timeout.

The phone also rechecks retained Hub/local certificate chains against current
trust and time every second. Secure-session cancellation reaches pending
DNS/dial/FETCH and striped lane waits, as well as established target sockets;
normal idle-preempt still protects active and dialing proxy slots. New client
reconnects reload its credential bundle. Source-level dual-session scheduling,
96/48 proxy limits, 120-second idle reaping and 10-second idle preemption remain
the same; no Hub-VPS target fallback is added.
Secure CONNECT cancellation closes the current target before attempting a
yamux stream FIN, including a target replaced during handshake replay. Secure
target writes have a 30-second deadline and release the relay state lock during
network I/O, so a non-reading target cannot prevent cancellation from closing
its socket. Legacy TCP/QUIC keep their existing relay behavior.

Healthy-local-storage closure target is 5 seconds (initial migration maximum
30 seconds); local tests assert the 5-second stream/session/target budget.
The recheck cadence is not a universal real-time guarantee for a frozen process
or hung filesystem. Real phone/Hub p99/max measurements and external monitoring
must establish the production budget. Configuration reload failure returns
`reverse_authorization_reload_failed` and closes sessions instead of retaining
an old grant.

`/debug/session-health` retains its existing CIDR controls and adds
`reverse_security`: protocol, transport, accepted registry generation, degraded
state/error code, last check, recheck/closure target and rejection/closure
counts. Secure sessions report egress ID, credential ID, leaf fingerprint and
`credential_expires_at_on_admission`; that initial expiry is not the expiry of
a subsequently shortened registry revision. No token, private key or payload
appears in this health object. The endpoint does not prove public egress IP,
phone mobility, supervisor recovery or product-ready performance.

## Local evidence and outstanding acceptance

Run `go test -race ./egress/reverse` and `go vet ./egress/reverse`. The controlled
end-to-end tests execute actual TLS server/client and yamux, a local Hub HTTP
CONNECT listener and a phone-side echo target. They cover wrong CA/Hub name/Hub
URI, wrong-role/expired injected certificates, unregistered egress/leaf,
credential and egress revoke, retained leaf/credential expiry, malformed or
missing authority, delayed hello after revoke, independent credential/CA/Hub
bundle rotation, dual-session limits, registry replay across restart,
hardlink aliases, explicit initialization and plaintext-fallback rejection.
Independent regressions additionally reject TLS 1.2, missing/wrong ALPN and
case-variant authorization fields, and cancel blocked initial/replay target
writes without target cooperation. Unix directory and authority mode tests run
in local WSL, including root-owned sticky and foreign-owner ancestor cases.
The original session scheduling, active/dialing protection and idle-preempt
suite runs alongside them.

This evidence is Windows loopback, local WSL/Linux synthetic tests and
Linux/arm64 compilation, not a real Pixel
7a canary. Remaining production evidence includes issued/registered identities,
private storage and CA provisioning, approved listener/firewall values, one
managed phone supervisor, WiFi/cellular transitions, IPv4/IPv6 egress, prolonged
throughput/resource comparison, reboot/recovery and raw-TCP retirement. Jetstar
is excluded by the user's instruction.

Primary implementation references: Go [`crypto/tls.Config`](https://pkg.go.dev/crypto/tls#Config)
(`RequireAndVerifyClientCert`, `VerifyConnection`, session tickets),
[`x509.VerifyOptions`](https://pkg.go.dev/crypto/x509#VerifyOptions)
(hostname, trust, time, EKU), and
[`tls.Conn.HandshakeContext`](https://pkg.go.dev/crypto/tls#Conn.HandshakeContext).
