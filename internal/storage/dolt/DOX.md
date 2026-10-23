# DOX.md — Server-backed Dolt adapter

## Purpose

Own server-backed dolt adapter within the fork.

## Ownership

SQL-backed Dolt storage and backend-specific operations.

## Local Contracts

Root DOX.md and the parent chain remain binding.

- Write transactions roll back on panic before propagating it, releasing the pool session. Committed writes and ordinary error rollback retain their existing no-replay behavior.

- Retried single and batch creates restore caller fields, inline dependency sources and comment IDs before every transaction attempt. Generated IDs are checked against the new snapshot; explicit import IDs retain upsert semantics. All-wisp batches follow the same rule without Dolt history.

- `ApplySchemaMigrations` includes migrations performed by this store's writable open exactly once, so explicit shared-server migration reports the applied count and reaches CLI version reconciliation. Read-only/gateway opens and consent gates keep their existing behavior.

- Compact refuses before acquiring a connection or issuing SQL: an online server does not provide atomic guarded publication of rewritten history. Existing signature and semantic issue-compaction operations remain.

- Explicit config publication stages config and its custom-type/status projections without sweeping other dirty tables. User KV publication stages config only after checking for dirty non-kv keys; generic Commit keeps excluding config. Publication failures are not replayed, and batch/off context deferral remains effective. This table-level screen is not cross-session row isolation.

- Wisp retries encompass the whole SQL transaction. Delete/sweep history-publication errors retain the SQL-commit uncertainty marker; never replay an unknown outcome.
- Peer credentials fall back only for storage.ErrNotFound; lookup, decryption and authorization failures remain fatal.
- Circuit housekeeping runs at most once per process and expires only closed files older than 24 hours. Explicit doctor maintenance retains its own sweep.
- Closed-file cleanup regressions use literal 23-hour retention and 25-hour expiration examples, independently of the TTL constant, for both legacy and live directories.

- A TCP open without any resolved port reports the missing endpoint separately from a failed connection; socket and explicit-port paths retain existing diagnostics. Do not suggest a fresh database as reconnection. An unreachable named externally managed port identifies its declaring setting and never recommends starting a different local server; preserve owned/shared-server advice and the underlying dial error.

- Dependency readers/counts retain each far end once across durable and optional wisp planes. Durable edge type wins, while dual-resident issue bodies follow existing wisp hydration. Only a typed missing optional wisp table reads as empty; propagate required-table and other read errors. Server iteration shares one read transaction across its joins.

- Metadata constructors preflight the native format and backend without migration, then recheck after loading. Callers that create gates use the same admission first; preserve the registered-backend refusal and existing options.

- Server probe/clone callers reuse the store’s port resolution; preserve explicit init overrides and shared-server provenance.
- IPv6 loopback never auto-starts the managed IPv4 server, adopts its local data directory or persists its port file.

## Work Guidance

Metadata field conversion delegates to shared issueops parsing; YAML numeric types, enum fallback and adapter-specific warning/error modes remain unchanged.

Use shared storage contracts; verify actual durability and transaction behavior at the real backend boundary.

## Verification

The shared optional AtomicMemories case is explicitly wired and skips with a stated reason when the accessor does not advertise it; no atomic-batch capability is inferred for this adapter.

Use the affected existing checks selected in engdocs/TESTING.md; verify indexed child contracts.

## Child DOX Index
