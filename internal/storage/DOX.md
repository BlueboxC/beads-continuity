# DOX.md — Storage boundary

## Purpose

Own storage boundary within the fork.

## Ownership

Storage interfaces, backend selection and persistence operations.

## Local Contracts

Root DOX.md and the parent chain remain binding.

- All three config SQL write paths reject locally reserved integration credentials before mutation. Preserve ordinary settings, bootstrap writes, KV/memory namespaces and deletion of historical rows. Raw SQL, replicated rows and retained history are administrative data and are not scrubbed by these write guards.

- Context-aware proxy discovery retains authenticated adoption, upstream matching and owned-child cleanup. Cancellation stops polling/readiness and does not signal an adopted process; existing identity probes retain their 500 ms bound. The legacy API remains a background-context wrapper.

- Empty-parent updates on dotted IDs fail before any patch mutation; legacy implicit ancestry remains supported. Nonempty reparenting and detachment of nondotted IDs retain their contracts. Do not silently remove the explicit edge and claim detachment.
- History compaction requires exclusive writer access across its full sequence. Head checks reject stale plans but are not atomic compare-and-reset. The server adapter and proxied command refuse destructive compaction; embedded compaction holds its existing pinned engine lease, rejects pending tracked changes and preserves ignored planes.
- History compaction dates the squashed base at the boundary and retains each replayed commit’s committer/date and separate author metadata. Snapshot metadata before mutations, restore exact caller session overrides on success/error and surface restoration failures. This preserves markers inside retained history; it does not reconstruct discarded history or repair previously re-dated stores.
- Ready-work readers withhold children connected by `parent-child` when the parent has deferred status or a future `defer_until`. `IncludeDeferred` lifts this selection filter; non-parent relations do not inherit it. Preserve parity between shared issueops and domain/db readers without rewriting stored task state.

- External blocks are evaluated from a caller-bound read-only resolver before ready limits/counts/atomic selection. Unresolved prerequisites and active parent-child descendants stay blocked across issue/wisp planes; do not materialize remote status into the local is_blocked cache.

- The proxy logs lifecycle and backend failures normally; per-connection tracing is opt-in through the existing debug posture. Runtime rotation bounds proxy.log and its single retained tail to 4 MiB each, including oversized startup logs. Log failures report once without replaying traffic or altering backend lifecycle. Only the default owned server.log also rotates at proxy startup before opening its descriptor; custom log paths remain operator-owned and server rotation is not a runtime size bound.

- Merge-slot holder and waiter mutations share a narrow transaction helper across native and UOW adapters. The transaction owner controls commit, conflict retry and rollback; callers never announce an uncommitted acquisition.

- Strict readonly proxy discovery authenticates an already-live endpoint and never spawns, repairs or quarantines. The readonly UOW uses existing current-schema data, read-only SQL transactions and a driver query guard; it cannot commit. SQL set-operation exports are writes, using the shared parser SelectStatement contract from rjgeng’s PR #7042.

- Heartbeater refreshes only an existing live claim; missing-lease re-arm uses a lock/status/holder fence, remints row_lock and records mutation hooks once. Refused orphan cleanup may commit without announcing success. Ordinary refresh adds no event, version, hook or history commit. Retained versions remain Graph Preview-only.
- RunInTransaction callbacks remain at-most-once. Explicit replay requires the confirmed rolled-back conflict marker; uncertain commit outcomes never replay. Retry backoff remains within the existing deadline.

- Embedded merge settlement stages newly merged companion tables before its resolution commit, retaining unrelated pre-existing dirty tables. Abort and explicit strategy guards remain unchanged.
- Optional ProjectSnapshotter lends only memory, issue and dependency reads from one callback-scoped transaction. Failed reads withhold the complete projection; unsupported adapters retain their existing acquisition path.

- Shared server-address construction accepts bare, bracketed and surrounding-space-padded IPv6, including a managed proxy backend's TCP Dial. Preserve socket transport and keep IPv6 loopback outside managed IPv4 lifecycle/data ownership.

- The shared merge-abort helper permits hard-reset fallback only for a known-clean pre-merge working set; preserve dirty or unreadable preconditions. CLI pull callers retain indeterminate-commit refusal and do not replay writes.

## Work Guidance

Memory namespace selectors preserve exact prefix semantics across adapters. All three adapters select namespaces in SQL before transferring values; the unit-of-work adapter uses ConfigUseCase/repository within its existing read transaction. Case-insensitive memory search remains in Go.

Embedded Memories offers an optional read snapshot with a callback-scoped reader for Recall/List. It shares one read transaction and engine acquisition across a complete current-generation read; the borrowed reader exposes no writes and fails after the callback. Other adapters retain their existing reads.

Embedded Memories also offers optional guarded atomic batches. Expected values, equal-value skips and all writes/deletes share one existing transaction; a mismatch or failed write rolls back the batch. The Graph Preview adapter also implements guarded batches in its coordinated transaction. Ordinary server/proxy adapters do not advertise this optional capability or qualify pruning.

Dolt server fixtures suppress asynchronous usage-event flushing before spawning the backend; shutdown waits retain their process contract and temporary configuration homes must not have detached writers.

Proxy fixtures that do not require a chosen data port bind port zero directly; retain the explicit occupied-port refusal check and use the published actual endpoint when dialing.

Keep Dolt engine behavior behind the existing storage boundary. Do not bypass it from CLI or other internal packages.

## Verification

Use the affected existing checks selected in engdocs/TESTING.md; verify indexed child contracts.

## Child DOX Index

- [schema/DOX.md](schema/DOX.md) — Schema and migrations.
- [dolt/DOX.md](dolt/DOX.md) — Server-backed Dolt adapter.
- [embeddeddolt/DOX.md](embeddeddolt/DOX.md) — Embedded Dolt adapter.
- [issueops/DOX.md](issueops/DOX.md) — Issue persistence operations.
- [uow/DOX.md](uow/DOX.md) — Units of work.
- [domain/DOX.md](domain/DOX.md) — Storage domain model.
- [graphstore/DOX.md](graphstore/DOX.md) — Opt-in Graph Preview storage, fresh schema and isolated native adoption.
