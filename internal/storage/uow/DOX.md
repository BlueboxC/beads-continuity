# DOX.md — Units of work

## Purpose

Own units of work within the fork.

## Ownership

Persistence transaction composition and completion.

## Local Contracts

Root DOX.md and the parent chain remain binding.

- Managed and external SQL providers resolve the authenticated workspace proxy for each new physical pooled connection. Preserve selected database, credentials, TLS and upstream identity; do not replay queries/transactions or disable idle limits. Schema bootstrap keeps its pinned endpoint and existing migration authority.

- Merge-slot create is idempotent and creates its label atomically; check and an unchanged create do not mint history. Acquire/release share native holder/waiter rules and use the existing transaction runner; retry results belong only to the committed attempt. Supersede edge and checked closure must commit together or roll back together.

- NewReadOnlyProxyUOWProvider never bootstraps, migrates or auto-starts; verify selected database, schema and configured project identity before returning. Its concrete preflight access is explicitly accounted for by the notification-bypass guard because it cannot mutate or commit. Each physical connection authenticates existing proxy discovery and retains credentials/TLS/upstream binding. Guard direct, query and prepared SQL before driver dispatch because Dolt READ ONLY alone permits DDL/procedures; allow classified reads plus internal READ ONLY/ROLLBACK controls, and reject UOW commit. This is a Beads connection contract, not a new database user or server-wide privilege policy.

- Scoped heartbeat publication commits SQL before staging named history tables, preserving concurrent working-set writes. Unknown SQL commit poisons the transaction and never replays; a history-publication failure after SQL commit is advisory and cannot replay a durable mutation. Readonly never commits. Refresh adds no history/hook; re-arm emits on_update once.
- Scoped Dolt publication uses schema.DrainCall for every result set, including an empty first set. Later result errors are advisory after SQL commit, stop publication and discard the session; successful draining preserves connection reuse.

- Server-log rotation is explicit per provider and applies only to the default owned path. Preserve context-aware discovery and per-connection authenticated proxy resolution when forwarding this option.

- Both ordinary and strict existing-only proxy providers check the external server version before schema work or returning a provider. Identified versions below 2.6.0 refuse without bootstrap; unknown versions preserve advisory behavior.

## Work Guidance

Verify commit, rollback and failure behavior at the affected transaction boundary.

Memories.List translates user prefixes to the memory namespace and calls ConfigUseCase.GetConfigByPrefix within one read-only unit of work. It never enumerates unrelated config or reserved namespaces to answer a selected list; empty selectors retain the complete memory plane. Remember, Recall and Forget derive presence from the selected key map within the same transaction, including stored empty values.

## Verification

The shared optional AtomicMemories case is explicitly wired and skips with a stated reason when the accessor does not advertise it; no atomic-batch capability is inferred for this adapter.

Use the affected existing checks selected in engdocs/TESTING.md; verify indexed child contracts. TestMemoriesListSelectionBoundsTransferredValues exercises the real config SQL/use-case boundary with an isolated Dolt fixture and counts returned key/value bytes, excluding engine reads and protocol overhead. TestMemoriesContract owns backend semantic conformance.

## Child DOX Index
