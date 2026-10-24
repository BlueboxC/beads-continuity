# DOX.md — Graph Preview storage

## Purpose

Own opt-in Graph Preview persistence over existing Dolt engines.

## Ownership

Graph Preview storage.

## Local Contracts

Root and parent DOX contracts remain binding.

- Init creates a fresh schema11/generation13 database only. The fixed Preview3 Memory catalog and concrete native families coexist with three frozen schema8 informational Types. OpenExisting validates exact binding and format without DDL, descriptor replacement or migration; upstream generation12 is not this fork format.
- Native dependency kinds retain their authoritative row, direction, ID, metadata, thread and attribution. `dep_type`, `nativeId` and `threadId` are protected facts; historical composition and deletion recovery retain them. Metadata edits use the native writer, integrity/readiness checks and one final retained source mapping. Native Issue batch creation records every initial version in the same transaction. Graph updates retain notes/template/assignee fences; ordinary native writers remain unchanged.
- Native history prerequisites are DDL for fresh graphs or explicit isolated adoption candidates, outside ordinary migration numbering. Opaque graph revisions remain distinct from native ordinals. Retyping, endpoint pins, historical composition and terminal recovery preserve current protected membership, guards and rollback; they do not grant execution authority.
- ContinuityMemories stores authoritative human bodies as canonical retained Memories with exact forkContinuityKey metadata and private key pointers. Derived code/catalogs and observed activity remain config projections in the same Dolt database. Equal-value Remember does not mint history; guarded batches, pointer publication and coordination commit together. Reserved metadata cannot be rebound. Managed deletion removes its pointer; managed recovery uses Remember and a new identity.
- Complete ordinary snapshots retain 2,048 live Resources/16 MiB, continuity selections 65,536 rows/64 MiB and total owned Links 1,000 per source. All ordinary writes compare pre/post acquisition dimensions atomically; each oversized dimension may shrink or remain unchanged; other dimensions retain their caps, including bounded deletion bookkeeping. Explicit conversion can preserve larger historical datasets without admitting normal growth.
- SnapshotMemories lends only callback-scoped synchronous Recall/List from one SQL snapshot, with cumulative selected acquisition budgets and post-callback refusal. Binding validation is cached only within that read-only transaction, never across transactions or for staged writes. Every selected canonical/retained body remains validated; oversized selections refuse before transfer.
- ReadPrimeContinuityWithActivity selects non-code context and the current index manifest, plus the newest three handoffs with full event/invalid counts. Missing manifests retain bounded legacy code reads. ReadProjectContinuity is the explicit fork compound profile: graph and selected current code each retain 65,536 rows/64 MiB; activity streams all envelopes but retains at most 1,000 validated 12 KiB events. Ordinary snapshots and general List semantics do not change. Retained generations and older observations remain stored.
- Native conversion and schema8 conversion operate only on separately restored candidates outside the bound workspace. Planning fingerprints table/view definitions, source roots and branch/tag commits; missing human pointers, unsupported source formats, stale plans and dirty non-main branches refuse before conversion. CheckpointConversion uses a native full backup and independent restore/readback, preserving the source.
- Schema8 conversion archives all original graph revisions, frozen descriptors and source preimages, then appends schema11 current boundaries and identity mappings. ReadOriginalVersion returns exact original bytes and Type; ReadVersion preserves its original representation. Historical composition may use checked original content with current protected identity/membership; no old bytes, acceptance or evidence scope are renewed. Cross-format comparisons with changed immutable identity refuse.
- VerifySchema8Candidate validates the complete original revision inventory, all human bodies, current records and retained Dolt references. Unaffected config/native tables remain unchanged; original native rows/version bookkeeping are preserved in source-format preimages. Conversion faults leave only a discardable candidate; recovery restores its full native backup into a fresh destination. No implicit publication or independent graph-clone merge is qualified.

## Work Guidance

Support direct embedded and ordinary SQL-server modes. Refuse unsupported proxy or activation routes rather than falling back to ordinary storage. Independent graph-clone merge is unqualified.

## Verification

Run real embedded lifecycle, history, rollback, guard and reopen tests. The synthetic schema8 fixture and conversion tests cover original bytes, historical composition, partial conversion refusal and source preservation. Retain bulk-snapshot/individual-read parity and native dependency metadata, no-op, scheduling and rollback regressions across provider changes. Set BEADS_GRAPH_TEST_SERVER_PORT for isolated ordinary-server concurrency qualification.

## Child DOX Index
