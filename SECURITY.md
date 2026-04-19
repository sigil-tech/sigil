# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| latest release | Yes |
| older releases | No |

Sigil is pre-1.0. Only the latest release receives security fixes.

## Reporting a Vulnerability

**Do not open a public issue for security vulnerabilities.**

Email **security@sigilos.io** with:

1. Description of the vulnerability
2. Steps to reproduce
3. Affected version(s)
4. Impact assessment (what an attacker could do)

You will receive an acknowledgment within 48 hours. We aim to provide a fix
or mitigation within 7 days for critical issues.

## Scope

Sigil runs as a user-space daemon with no elevated privileges. The primary
security surface includes:

- **Local SQLite database** (`~/.local/share/sigild/data.db`) — contains
  workflow telemetry. Protected by filesystem permissions.
- **Unix domain socket** (`/tmp/sigild.sock`) — IPC between `sigild` and
  `sigilctl`. No authentication beyond Unix socket permissions.
- **Inference engine** — when configured for cloud routing, sends prompts
  to configured API endpoints over HTTPS.
- **Fleet reporter** — when opted in, sends anonymized aggregate metrics
  to a configured fleet endpoint over HTTPS.
- **Sync agent** — when opted in, streams local SQLite row data (events,
  tasks, suggestions, predictions, patterns) to a configured cloud ingest
  API over HTTPS. Disabled by default.

## TCP Network Listener (optional)

sigild can optionally accept remote connections over TCP+TLS. This is disabled
by default and must be explicitly enabled in the daemon configuration.

### Configuration

```toml
[network]
enabled = true
bind    = "0.0.0.0"
port    = 7773
```

### Threat model

- **Disabled by default.** No TCP port is opened unless `network.enabled = true`.
- **Port.** Default port 7773. Firewall rules are the operator's responsibility.
- **Transport security.** TLS 1.3 (minimum). TLS 1.2 and below are rejected.
  The server presents a self-signed ECDSA P-256 certificate generated at first
  start. The certificate is stored at `$XDG_DATA_HOME/sigil/server-cert.pem`.
- **Authentication model.** Bearer token — one token per remote client identity.
  The client must send `{"method":"auth","payload":{"token":"..."}}` as the
  first message after the TLS handshake. No daemon method is dispatched before
  successful authentication.
- **MITM protection.** Clients verify the server's TLS certificate via SPKI
  fingerprint pinning (`sha256/<base64>` of `SubjectPublicKeyInfo`). The
  fingerprint is baked into the credential file transferred out-of-band.
  Standard CA chain validation is not used.
- **Token storage.** Bearer tokens are stored on disk as SHA-256 hex hashes.
  Plaintext tokens are only visible at credential creation time (output of
  `sigilctl credential add`).
- **Credential revocation.** Immediate and hot — no daemon restart required.
  `sigilctl credential revoke <name>` removes the credential from the in-memory
  store and persists the change to disk.

### Credential lifecycle

1. Run `sigilctl credential add <name>` on the daemon machine.
2. Copy the JSON output to the remote host and save with `chmod 600`.
3. Configure sigil-shell with `"transport":"tcp"` pointing at the credential file.
4. To revoke: run `sigilctl credential revoke <name>` and delete the credential
   file on the remote host.

## Cloud LLM Proxy (`cloud/llm-proxy`)

The LLM proxy is a cloud-hosted API gateway that proxies inference requests
from sigild instances to upstream LLM providers (OpenAI, Anthropic). It is
deployed independently and is not part of the sigild daemon.

### Security surface

- **HTTP listener** — listens on a configurable address (default `:8081`).
  Must be deployed behind a TLS-terminating reverse proxy in production.
- **Authentication** — Bearer token in the `Authorization` header. Requests
  without a valid token are rejected with 401.
- **Tier enforcement** — Free-tier requests are rejected with 403. Only
  Pro and Team tiers are permitted.
- **Rate limiting** — per-tenant sliding-window rate limiter prevents abuse.
- **Upstream credentials** — OpenAI and Anthropic API keys are read from
  environment variables, never logged or persisted.
- **No content logging** — prompt content and response content are never
  stored or logged. Only metadata (model name, status code, latency) is
  recorded for billing purposes.

## VM Sandbox IPC surfaces (spec 028)

### sigild-vz subprocess boundary (macOS — ADR-028a — PENDING Phase 4b)

On macOS, sigild spawns a helper subprocess `sigild-vz` (a Swift binary
built from `sigil-launcher-macos/Sources/SigilVZ/`) to drive the Apple
Virtualization framework. Communication happens over stdin/stdout JSON-line
pipes. The subprocess is not present on Linux.

Security properties:
- `sigild-vz` handles only VM lifecycle commands (`start`, `stop`, `status`,
  `subscribe`, `health`). No user event data flows through this pipe.
- A version handshake at startup validates that the `sigild-vz` protocol
  version matches sigild's expectation. A mismatch causes sigild to terminate
  the subprocess and return `ERR_HYPERVISOR_UNAVAILABLE`.
- The subprocess inherits sigild's user identity (no privilege escalation).
- Crash isolation: a crash in `sigild-vz` does not crash sigild; the daemon
  marks the session as failed and continues.

### QEMU / QMP protocol (Linux — ADR-028b)

On Linux, sigild spawns `qemu-system-x86_64` directly and communicates with
it over a Unix domain socket using the QEMU Machine Protocol (QMP).

Security properties:
- The QMP socket is created in a temporary directory with permissions `0600`
  (owner-only). The socket path is unlinked on both clean teardown and
  SIGKILL paths.
- QMP commands are limited to lifecycle operations (`system_powerdown`, `quit`,
  `query-status`, `qom-get`). No arbitrary command injection is possible
  through sigild's QMP client.
- QEMU runs as the current user. VM disk images are accessed via ephemeral
  overlay files (`qemu-img create -b`), so the base image is never modified.

### vm-events push topic (planned Phase 6)

The `vm-events` topic will relay observer events from inside the VM guest to
connected Kenaz subscribers. All events pass through the `serializeKenazEvent`
redaction pipeline (spec 027 FR-010e) before being placed on the topic buffer.
Subscribers see only redacted metadata; no raw file paths or command strings
from inside the VM are exposed. The topic uses a per-subscriber 256-slot
buffer with drop-on-full semantics.

## Design Principles

- All data is local-first. Nothing leaves the machine without explicit opt-in.
- Unix socket IPC is the default; TCP listener requires opt-in configuration.
- No root/sudo required. Runs as the current user.
- Cloud API keys may be stored in the user's config file (`config.toml`,
  mode 0600) by `sigilctl auth login`, or read from environment variables.
  The daemon reads but never writes API keys. Users can clear stored keys
  by editing `config.toml` directly.
- Fleet telemetry is anonymized and aggregated before transmission.
