# Sensitive path detection

[日本語](./sensitive-path.ja.md)

Sensitive-path detection flags command audits that appear to touch high-risk
locations such as dotenv files, SSH keys, cloud credentials, browser profiles,
or key material. It is a **separate claim** from:

- **Secret redaction** — masks values in stored payloads (`application/redaction`)
- **Host capture coverage** — whether the hook trail is complete enough to trust

## What a match means

A match means Traceary observed **sensitive intent or path text** in the audit
material. It does **not** by itself prove the OS opened the file.

| Evidence | Meaning |
|---|---|
| `command_text_only` | Shell/command text matched (e.g. `cat .env`). Intent observed; file access not proven. |
| `structured_file_tool` | Host file tool payload included a path (e.g. Read/Write). Stronger path claim. |
| `unresolved_path` | Pattern matched but path could not be resolved cleanly. |

Coverage (`complete` / `partial` / `unobservable`) describes payload quality
(truncation, empty body, command-only rows), not sensitivity.

## Default pattern classes

Implemented in `application/sensitivepath`:

- `dotenv` — `.env`, `.env.*`
- `ssh_key` — `~/.ssh`, `id_rsa` / `id_ed25519` basenames
- `cloud_creds` — `~/.aws`, credentials / service-account JSON names
- `browser_profile` — Chrome / Firefox / Brave profile and cookie paths
- `key_material` — `*.pem` / `*.key` / `*.p12`, keychain paths
- `custom` — exact substrings supplied as extra patterns (same spirit as
  `extra_redact_patterns`; configuration wiring may expand later)

## CLI

```sh
# list only matching command audits (compute-on-read on event bodies)
traceary list --sensitive --kind audit --limit 20

# full audit detail includes a `sensitive` object on command_audit JSON
traceary show <event-id> --json
```

## Doctor

`traceary doctor` includes `sensitive-access-audit`, which summarizes recent
matches and warns when matches are intent-only or have weak coverage. Passive is
passive: no blocking, no deny/ask policy.

## Non-goals

- Real-time hook allow/deny
- OS-level proof of open when the host only exposes command text
