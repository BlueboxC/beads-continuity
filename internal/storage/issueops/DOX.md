# DOX.md — Issue persistence operations

## Purpose

Own issue persistence operations within the fork.

## Ownership

Issue lifecycle queries and mutations.

## Local Contracts

Root DOX.md and the parent chain remain binding.

- Journal actor provenance shares the mutation transaction and cursor. Only an exact context actor match inherits the resolution source; other named actors are `provided`, actorless rows remain empty. Historical provenance is never inferred from a read.

- Optional `gates.human.resolvers` is a transaction-local caller-asserted allowlist. Missing key is compatible; empty/malformed policy refuses protected operations. Explicit flag/env/named API actors only; force and disabled journaling never waive it. Share gate close/update/delete/edge/persistence checks with domain/db. This is no human authentication or external execution authority; imports/config/SQL/sync/restore remain caller-controlled administrative access.

- External blocker evaluation is shared with domain/db. Absent/unreadable providers fail closed; required-table and resolver failures propagate. Traverse active parent-child descendants with a visited set, preserving external references or an inherited witness in blocked output.

- The retained Issue recorder is transaction-scoped and called only by Graph Preview adapters after a final accepted mutation. Explicit candidate adoption may seed the first boundary snapshot without replaying create or minting a journal event. Its revision bookkeeping preserves the accepted postimage's row_lock/updated_at and does not duplicate the mutation's journal event. Ordinary native writers and existing journal/security policies remain unchanged; graph-only schema prerequisites live in graphstore.

- Graph-only dependency composition shares native integrity, readiness and journal seams. Recovery refuses occupied pairs and preserves the protected native ID/thread. Native caller semantics remain unchanged.

- SnapshotCreateInputs restores every caller-owned field mutated by create, including nested dependency sources and comment IDs. Keep it aligned with persistence mutations.
- Lease re-arm fences live issue state in the mutation, so a concurrent close/release cannot recreate an orphan lease. A refresh preserves provenance and mutation history.
- Reclaim requires a nonempty live assignee matching the expired lease holder under the existing actor rules. Recheck the observed holder and assignee before deletion/release; an unrelated orphan lease cannot release or journal another claim. Preserve replica and expiry guards.
- Provenance ingest time is explicitly UTC at second precision, independent of occurred_at and server timezone. INSERT IGNORE retries retain the original ingest stamp; existing rows are not rewritten.

- Multi-edge creates read parent ancestry once inside their transaction, after all accepted parent edges. Preserve both dependency planes, hierarchy/cycle refusals, cancellation and upsert-only behavior; ordinary single-edge checks keep their targeted query. This removes repeated ancestry SQL, not all per-edge persistence or cycle checks.

- Dependency readers/counts retain each far end once across durable and optional wisp planes. Durable edge type wins, while dual-resident issue bodies follow existing wisp hydration. Only a typed missing optional wisp table reads as empty; propagate required-table and other read errors. Server iteration shares one read transaction across its joins.

## Work Guidance

Preserve issue semantics across supported storage adapters and shared conformance tests.

## Verification

Use the affected existing checks selected in engdocs/TESTING.md; verify indexed child contracts.

## Child DOX Index
