# Reproducible Desktop D0 probes (2026-10-23)

[日本語](README.ja.md)

These isolated, standard-library-only Python probes preserve the additional evidence for [#2443](https://github.com/duck8823/traceary/issues/2443), under [#2424](https://github.com/duck8823/traceary/issues/2424). They are not product code, an adopted public protocol, or proof of D0 acceptance. Muse is deferred and is not invoked.

## Reproduce

From the repository root, with Python 3.10+:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s docs/adr/evidence/desktop-d0-probes-2026-10-23/resize_ack -v
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s docs/adr/evidence/desktop-d0-probes-2026-10-23/unix_ipc -v
```

The 31 resize tests are pure synthetic fixtures. The 29 Unix-socket tests require macOS, permit creating local AF_UNIX sockets and read peer UID through libc getpeereid; one client is a separate same-UID Python process. They create only owned temporary directories under `/private/tmp`, close/wait their sockets, threads and child, and assert directory cleanup. Do not run them on another OS and report skips as passes. No Desktop bundle, provider login, credential reading, external network, global configuration or privilege changes are involved. Local fixture creation still requires an execution environment that permits local sockets and process creation.

## Resize receipt model

One ledger binds an immutable generation/window/source cohort. Each of up to 200 requests has an explicit ID, target and caller-supplied monotonic deadline. Immediate getters are diagnostic only. A completion capture must have a native-generated capture ID and coherent issue/application/capture/runner-receipt times. Reusing a capture ID or timestamp pair across requests makes both ambiguous/conflicted; late/conflicting evidence never resurrects success. This is correlation logic, not proof that the actual UI backend generated those fields.

The 31 tests include200 interleaved requests at the unchanged 150 ms cadence; their 500 ms deadline is a synthetic fixture, not an adopted native performance threshold. The main session reproduced a capture-reuse false-success path before fixing it; independent Google-family static review confirmed the fix and reported no remaining findings. The reviewer ran no commands. Native UI completion provenance, physical presentation and the native200-resize gate remain unproven.

## Unix-socket feasibility probe

Actual macOS tests cover 0700 directory/0600 socket mode bits, separate-process same-UID communication, kernel peer UID equality, preexisting path refusal and replacement-inode cleanup, fragmented/truncated/oversize/invalid frames, a total 250 ms exchange deadline and an observation-only fixture handler. Fixed run/generation mismatch and control-operation rejection are tested without implementing product operations. The 64 KiB frame bound and 250 ms budget reproduce Proposed ADR values, not current CLI guarantees.

Different-UID rejection is **not** executed: only a deliberately mismatching expected-UID branch is tested. Mode-bit checks are not an ACL audit. This is not a hardened TOCTOU-free implementation, same-UID hostile-process isolation, executable identity, complete sender authorization or a production security certificate. The source is experimental and must not be installed as a resident daemon or public CLI intake. Queue bounds, observation projection, durable history, existing hooks, store leases, PTY/crash/reconnect and native UI behavior are not implemented or certified here.

## Evidence and adoption limits

[Sanitized result metadata](../desktop-d0-probes-2026-10-23.json) binds the probe sources and recorded test scope. The paired ADR remains Proposed; neither Electron nor Wails is adopted. Native IME, accessibility, full-load input, controlled foreground comparisons, sleep/wake/crash, multiple displays, five-host qualification and maintainer adoption remain separate gates. No earlier native failure is edited into a pass. These probes are not substitutes for the full objective.
