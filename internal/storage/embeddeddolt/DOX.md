# DOX.md — Embedded Dolt adapter

## Purpose

Own embedded dolt adapter within the fork.

## Ownership

In-process Dolt storage and its lifecycle.

## Local Contracts

Root DOX.md and the parent chain remain binding.

- Local backup restore rehearses native restore and commit with the pinned engine in a disposable directory before replacing the destination. Refuse incompatible backups with their underlying error; do not discard unknown fields. Qualified engine upgrades retain this guard, verify older and newer native backups, subsequent writes/reopen and a second backup/restore, and preserve the prior database/binary recovery set. This costs an additional temporary restore and is not atomic publication or protection against source changes between rehearsal and restore. Readonly/closed stores refuse before creating the rehearsal.

- Embedded open permission failures retain their typed cause as OpenPermissionError. Logical readonly forbids Beads mutations but the driver still needs filesystem write access. Do not change engine retries/locks, relax permissions or classify errors by matching text.

- History compact uses the existing mutating pinned-connection open and its exclusive engine lifetime; head/clean-working-set checks precede branch creation and final reset. Direct SQL on the same borrowed engine is outside this exclusive-writer contract. Existing metadata retention and readonly refusal remain.

- Scoped configuration commit methods alias the existing complete-working-set Commit on this embedded adapter; the CLI epilogue remains the normal per-command owner. Server-only staging and user-KV screening are not inferred for embedded stores.

- Heartbeat shares live-claim validation and row-lock re-arm fencing with native/UOW. Refresh changes only the lease; successful re-arm records a mutation and its history commit. Retained Issue versions require the Graph Preview recorder.

- Metadata-less planning adoption lists only populated, non-symlink database markers; ambiguous sets refuse and selected existing databases are never replaced by a new default.

- ReadProjectSnapshot shares one engine acquisition and readonly transaction across current-generation memories, tasks and dependencies. Borrowed readers expire with the callback; cancellation, nil callbacks and callback errors fail without changing logical roots.

## Work Guidance

Respect embedded writer and transaction constraints; use disposable stores for tests. SnapshotMemories uses one existing read transaction and lends only Recall/List within its synchronous callback. Do not call outer store operations or retain the reader beyond the callback. Nil callbacks, cancellation and read/callback errors fail without publishing partial results or writing records. Memories.Apply validates keys before opening a transaction, checks every expected value before mutation and atomically writes/deletes through the existing SQL helpers. Equal values skip writes; return counts only after successful commit. No engine-specific lock, schema change or retry layer.

## Verification

Use the affected existing checks selected in engdocs/TESTING.md; verify indexed child contracts. Engine qualification retains source-root backup failure propagation and repeated open/close lifecycle regressions with the actual pinned dependency. Native engine diff regressions and isolated backup/history comparisons qualify dependency upgrades separately.

## Child DOX Index
