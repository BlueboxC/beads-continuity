# Existing-workspace conversion to Graph Preview

Status: explicit embedded conversion implemented by
`graph-convert plan|prepare|verify|activate|rollback`.
`init --graph-mode link` must keep refusing an existing workspace. Work state and qualification belong
in Beads, not this document.

## Preservation contract

Use the existing Dolt database. Adopt existing Issue rows rather than replaying
`create`, so IDs, fields, dates, closed state, labels, metadata, leases and native
events remain unchanged. Preserve all user tables, schema migration numbers,
branches, commits and the working set. Comments remain native comments; do not
turn an old comment or observed session event into a verified solution.

Promote every non-derived keyed memory, including assertions, proposals and
reviews, to one canonical retained Memory. Preserve the exact UTF-8 key and
body, including an imported empty body. Store the existing private continuity
pointer and matching `forkContinuityKey` metadata atomically. Preserve the
original review status, source hashes, evidence and validity. Adoption supplies
conversion provenance; it must not impersonate the original author or renew an
old tested/installed claim. Keep code, document catalogs and observed activity
in their existing derived config plane.

Issue graph history starts with a snapshot of the adopted current state at the
conversion boundary. Preserve earlier native snapshots, events and Dolt history;
do not synthesize opaque graph revisions for old commits or claim earlier
states are available through `bd versions`. State the two history scopes in
the conversion receipt and user documentation.

Preserve dependency rows and their original semantics. `parent-child` affects
scheduling and closure, so it cannot become an informational Related Link.
`discovered-from` and `related` must retain their distinct type, direction,
identity, metadata, thread and attribution. Unknown types, external/wisp
endpoints, missing endpoints, invalid encodings or unsupported native fields
produce a named refusal before mutation. JSON numbers that the graph canonical
contract cannot represent exactly must also refuse, never round or rewrite the
native value. Because native JSON columns can already contain rounded historical
input, adoption also refuses integer metadata at or beyond 2^53 rather than
claiming the old input was exact. No silent coercion or dropped rows.

## Compatibility work before conversion

The fresh schema-8 format projects native `blocks`, `parent-child`,
`discovered-from` and `related`, retaining row identity, metadata, thread and
attribution. Existing schema-7 descriptors stay immutable and are not upgraded
by opening a workspace. Native source-owned snapshots, incident queries,
retained versions and unlink preserve the shared native policy. Adoption must
use this mapping rather than duplicate an ordinary dependency as a generic Link.

The complete-inventory cap is 2,048 live Resources with a 16 MiB current-read
byte ceiling. Closed Issues and supervised proposal/review Memories still count.
Capacity qualification uses a real mixed inventory with all four native kinds
and keyed envelopes, 2,049 refusal, atomic rollback and oversized-state reduction.
Complete reads batch Memory/native-Link acquisition while retaining exact
individual-record validation. This remains an operational preview limit, not
production scalability. Conversion must refuse any projected inventory or byte
budget that does not fit; never remove closed work to make a project fit.

Keep the 65,536-row / 64 MiB continuity budget. An explicit existing code-prune
plan can remove only unreferenced content-addressed blobs from the current
working set, retaining the complete published manifest, file fragments and
Dolt history. Validate every blob hash and the current full index. Perform any
prune first on a recoverable isolated candidate, with upgraded writers and a
current-manifest guard; no automatic maintenance prune. Activity records and
human knowledge are not candidates for deletion.

## Preparation and isolated rehearsal

1. Acquire a consistent readonly inventory per project: source format/binary,
   Dolt branch/HEAD, working set, schema, issue fields/types, dependency types and
   endpoints, comments/events, human-memory hashes, current index/manifest,
   catalog/activity counts, retained history and projected acquisition budgets.
   Raw payloads and project identities remain private. A changed HEAD or working
   set invalidates the apply plan; readonly acquisition never authorizes apply.
2. Verify space for a full Dolt-native checkpoint and an isolated candidate.
   JSONL export alone is insufficient: it omits full table, branch and history
   state. Preserve workspace metadata/selector and the known-compatible binary.
   A filesystem copy requires quiescent writers and a supported capture method;
   verify restoration, not just backup-file existence.
3. Build an identity/adoption manifest bound to those exact fingerprints, the
   final canonical workspace path, operator-selected permanent scope URL and
   new authority. Never silently reuse a copy's path/authority as live identity.
   The private receipt maps each existing ID/key/dependency to its canonical
   resource and records projected size plus any explicit candidate-only prune.
4. Use the isolated candidate for explicit schema adoption and seeding retained
   current snapshots. Ordinary Issue bodies and existing migration numbers stay
   intact. Canonical bodies, pointers, catalogs and their first versions publish
   consistently. Workspace format metadata is activated only after database
   persistence; a crash must restore the original selector or resume the exact
   checked candidate, never present a half-converted workspace as valid.
5. Compare native rows and exact human envelopes, every relation/type/endpoint,
   all old branch/commit references and retained history. Verify the complete
   current index and catalog, ordinary scheduling, code/knowledge queries,
   supervised review, prime recovery, viewer refresh and lifecycle isolation.
   Historical source validity and production authority are unchanged.
6. Inject interruption/conflict/invalid-data/limit failures and exercise rollback
   through a fresh process with the preserved old binary. Repeated apply must
   identify the completed candidate or refuse, never allocate duplicate Memories
   or links. Do not overwrite writes accepted after conversion with an old
   backup; restoration needs a separate reconciliation boundary.

## Activation boundary

Only a qualified explicit conversion operation may activate an existing project.
Before activation, quiesce ordinary/graph writers, lifecycle hooks and viewer
reads; recheck all expected fingerprints, checkpoint integrity, free space and
final identity. Refuse drift instead of refreshing the plan silently. Keep the
original recoverable until fresh-process verification succeeds. Conversion does
not execute project code, reindex sources, accept learning proposals, configure
global Codex hooks, publish Dolt data or authorize project runtime/production.

No live project is converted by publishing this design. Initial rehearsal
supports current embedded native schema, with main as the
only branch carrying uncommitted work; other dirty branches refuse. Shared-server
adoption, moving converted workspaces and independent clone merging remain
unqualified.

Example isolated rehearsal (all paths and scope are explicit):

```sh
bd graph-convert plan --workspace /project/.beads --scope-url https://example.invalid/project/ --out /private/rehearsal-plan.json
bd graph-convert prepare --plan /private/rehearsal-plan.json --out /private/rehearsal --actor OWNER --writers-stopped
bd graph-convert verify --receipt /private/rehearsal/receipt.json
```

Plan and preparation leave the source native. Preparation retains a Dolt-native backup, exact
selector and compatible executable alongside the restored candidate. Seal every
backup file after the successful restore; changed recovery material refuses
activation, verification and rollback. A failed
preparation directory is retained for diagnosis; repeat preparation refuses an
existing output instead of silently healing or allocating duplicate records.
For explicit activation after verification:

```sh
bd graph-convert activate --receipt /private/rehearsal/receipt.json --writers-stopped
bd graph-convert verify --receipt /private/rehearsal/receipt.json
```

All clients, hooks and viewers must be stopped; the flag asserts this and does
not stop them. Graph commands share the existing workspace/physical-root gate;
publication holds it exclusively. Stage the verified engine through an atomic
same-filesystem rename, retain the original engine beside it, then fence the
workspace with `graph-conversion-incomplete` before swapping the active engine
and publishing the exact metadata plus supported marker. Repeat `activate` to
resume an interrupted sealed transition; unknown layouts, source drift and
changed artifacts refuse. The original inactive engine is recovery material,
not another running graph service. Keep the native backup and recovery binary.

`rollback --receipt ... --writers-stopped` restores the exact native selector
and engine only when the graph still matches its rehearsal fingerprint. It
retains the graph engine and refuses new writes instead of erasing them.
Repeat the same rollback to finish an interrupted restoration. Partial
activation must resume activation before rollback can use the retained native
engine. Generic recovery of arbitrary modified graphs remains unsupported.
Publication/rollback are qualified on POSIX; Windows refuses them.

When obsolete code exceeds final acquisition bounds, plan with
`--prune-obsolete-code`. This explicit path admits a native rehearsal plane up
to 2 GiB, reuses the existing guarded code prune only on the restored candidate,
records removed key hashes and checks every other native/configuration field.
It retains the current manifest/fragments and all Dolt history; final graph
admission still enforces 64 MiB. Corrupt or missing current indexes refuse.
Native source rows are never pruned by conversion.

Converted histories begin at adoption. Earlier commits and clean branch tips
remain in the checkpoint and retained native engine; older branches are not
silently converted or admitted as graph writers.
