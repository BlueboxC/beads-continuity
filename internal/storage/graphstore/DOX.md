# DOX.md — Graph Preview storage

## Purpose

Own opt-in Graph Preview persistence over existing Dolt engines.

## Ownership

Graph Preview storage.

## Local Contracts

Root and parent DOX contracts remain binding.

- Init creates a fresh database only. Schema 8 installs nine immutable Types for native Issue v3 and all four admitted dependency kinds; schema 7 retains its exact original four/six Types and blocking payloads. OpenExisting validates exact binding and format without DDL, descriptor replacement or migrations.
- Native blocks, parent-child, discovered-from and related preserve the authoritative dependency row, direction, ID, JSON metadata, thread and attribution. Native related remains distinct from independent informational related. Fields are immutable through graph authoring; conflicting repeat intent fails. Native Issue writes retain their existing policy. The graph adapter records once after final mutation, maps the retained native snapshot and owned Links atomically, and preserves no-op/rollback behavior.
- Native history prerequisites are DDL for fresh graphs or explicit isolated adoption candidates, outside ordinary migration numbering. Ordinary writers remain unchanged.
- ContinuityMemories reuses the keyed memory role: human records/proposals/reviews have canonical retained Memory bodies with exact forkContinuityKey metadata and key-to-path pointers, never duplicate full config bodies. Catalog/code/activity rows remain derived or observed data in the same config plane. Guarded batches, pointer publication, retained snapshots and writer coordination share one transaction. Preserve incident-Link deletion refusal, permanent tombstones, UTF-8 and complete-read budgets; corruption is an error. Native Memory deletion removes its managed pointer; native metadata edits cannot rebind that key. Equal values do not mint graph history.
- Every graph write checks live counts, current acquisition bytes and keyed-plane row/byte budgets in its transaction before commit. Reject growth atomically; an already oversized workspace may reduce or preserve each dimension without deleting history. Lists acquire no over-budget config payloads. Ordinary storage remains unchanged.
- Complete snapshots admit at most 2,048 live Resources and 16 MiB current acquisition bytes; continuity remains 65,536 rows/64 MiB and owned Links remain 1,000 in total per source. Bulk Memory/native-Link acquisition and cached owned sets share the read transaction and the individual readers' canonical/retained validators. Binding validates once; no integrity check or complete-result refusal may be bypassed.
- ReadContinuity composes current graph records and continuity projections in one read transaction. Original format publication belongs to the explicit CLI converter; generic public restoration and independent clone merge remain unsupported.
- Explicit conversion planning fingerprints the current native schema, all current table/view definitions, main working/staged roots, branch commits and tags. Refuse non-main dirty branches, populated unsupported native fields, wisps and ambiguous native JSON integers. CheckpointConversion uses native full backup and a separate restored candidate, preserving the source; adoption reuses canonical body/pointer and retained Issue writers, verifies original fields, every unaffected table and every config row except the exact hash-guarded obsolete blobs, and refuses repeat adoption. An explicit native prune plan permits at most 2 GiB before isolated official cleanup; ordinary planning and final graph admission retain the 64 MiB plane limit. No ordinary open adopts a database.
- Serialize graph writes through coordination; opaque graph tokens are distinct from local history ordinals. Preserve bounded reads, canonical bytes and permanent identity/tombstones.

## Work Guidance

Support direct embedded and ordinary SQL-server modes. Refuse unsupported proxy/activation/recovery rather than falling back to ordinary storage. Independent graph-clone merge is unqualified.

## Verification

Run real embedded lifecycle, history, rollback, guard and reopen tests. Set BEADS_GRAPH_TEST_SERVER_PORT for isolated ordinary-server concurrency qualification.

## Child DOX Index
