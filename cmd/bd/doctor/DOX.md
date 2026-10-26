# DOX.md — Diagnostics

## Purpose

Own diagnostics within the fork.

## Ownership

Health checks and repair operations.

## Local Contracts

Root DOX.md and the parent chain remain binding.

- Interactive and non-interactive version diagnostics report installed versions without querying GitHub.

- Repair connections resolve credentials for the exact dialed port. Federation status probes a resolved endpoint when no managed PID is available and reports missing PID without dereferencing nil state. The federation TCP probe applies the same final port resolution as the store, including the legacy BEADS_DOLT_PORT override. Preserve endpoint and project identity checks; socket-only probing remains unsupported.

- Multi-repository type discovery reads legacy metadata without rewriting it before a database exists; native type reads skip graph/future formats and unrecognized backends before any migrating open. Retain YAML fallback and report refused database reads without presenting them as complete type discovery.
- Classic artifact walks inspect each nested workspace without migrating legacy metadata. Skip graph/future/unreadable workspaces during classification and cleanup; preserve redirect metadata and its unknown fields. SQLite cleanup and reporting share positive Dolt selection: explicit Dolt metadata or an existing Dolt repository. Preserve unknown/other backends and ambiguous metadata-less SQLite files, including their WAL/SHM/backups.
- Redirect-expected paths do not establish cleanup ownership: scan and cleanup preserve directories containing database files/sidecars, configured existing database paths, local Dolt roots or foreign backend metadata before any artifact removal.
- A classified redirect stub removes only regular daemon runtime leftovers (`bd.sock` may also be a socket). Keep directories, symlinks and project material. `.local_version` and `last-touched` are disposable only without local configuration and with a redirect discovery actually follows. Scan and cleanup share the same individual-file plan; retained directories are never marked safe to delete. Artifact `--clean --json` sends cleanup progress to stderr and emits one JSON document on stdout.

- Claude plugin and hook checks reuse setup’s exact identity and settings precedence; personal disablement remains effective.
- Server diagnostics format bare/bracketed IPv6 through the shared address builder and retain external-owner advice. An unmanaged endpoint without a resolved port fails with pin-the-port advice instead of an auto-start skip or a port-zero probe; server health marks the result unsuccessful. Owned/proxied health checks retain their existing auto-start skip. Performance diagnostics do not attribute local directory sizes to externally managed IPv6 loopback endpoints.

- Server version diagnostics share the external required floor, provide upgrade/restart advice for recognized old servers and escape unknown version text.

- Doctor runtime reads and TCP fixes share DialConfig precedence and matching port credentials. Fresh-clone and read-only duplicate/FK scans follow the store socket fallback; SQL repair admission and metadata reconciliation refuse a selected socket or unresolved TCP port. Preserve target identity checks, TLS, gateway restrictions and the external version floor. Connection/catalog probes have deadlines.

## Work Guidance

Diagnose before repair; preserve recovery evidence. Use existing doctor operations rather than adding parallel repair commands.

## Verification

Use the affected existing checks selected in engdocs/TESTING.md; verify indexed child contracts.

## Child DOX Index
