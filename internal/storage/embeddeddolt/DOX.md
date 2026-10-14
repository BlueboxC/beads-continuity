# DOX.md — Embedded Dolt adapter

## Purpose

Own embedded dolt adapter within the fork.

## Ownership

In-process Dolt storage and its lifecycle.

## Local Contracts

Root DOX.md and the parent chain remain binding.

- Embedded open permission failures retain their typed cause as OpenPermissionError. Logical readonly forbids Beads mutations but the driver still needs filesystem write access. Do not change engine retries/locks, relax permissions or classify errors by matching text.

- History compact uses the existing mutating pinned-connection open and its exclusive engine lifetime; head/clean-working-set checks precede branch creation and final reset. Direct SQL on the same borrowed engine is outside this exclusive-writer contract. Existing metadata retention and readonly refusal remain.

- Scoped configuration commit methods alias the existing complete-working-set Commit on this embedded adapter; the CLI epilogue remains the normal per-command owner. Server-only staging and user-KV screening are not inferred for embedded stores.

## Work Guidance

Respect embedded writer and transaction constraints; use disposable stores for tests. SnapshotMemories uses one existing read transaction and lends only Recall/List within its synchronous callback. Do not call outer store operations or retain the reader beyond the callback. Nil callbacks, cancellation and read/callback errors fail without publishing partial results or writing records. Memories.Apply validates keys before opening a transaction, checks every expected value before mutation and atomically writes/deletes through the existing SQL helpers. Equal values skip writes; return counts only after successful commit. No engine-specific lock, schema change or retry layer.

## Verification

Use the affected existing checks selected in engdocs/TESTING.md; verify indexed child contracts.

## Child DOX Index
