# Preview3 integration and preservation contract

This integration supports isolated qualification and a separately authorized
installation/publication phase. Production provider source is donnabox/beads PR291
`0c4dc1f0d68a371e56b3e461b968e828e4ea254c`; compatible native seams were checked
against `79450c2d67ccab326b20b07cc3d66179fa148ec0`. The fork baseline is
`173568d48e16e992b19c3ec21a227ca95347573b`.

## Format and runtime boundary

The candidate uses schema11/generation13. The fork adds the three frozen schema8
informational Types and protected native dependency ID/thread facts to the
Preview3 catalog. Upstream generation12 is a different contract. No ordinary
open, discovery, hook or maintenance route converts an existing workspace.

Keep the ordinary v1.3.1 native API, verified fork corrections, qualified Dolt
engine/analyzer/driver tuple, telemetry removal, supervised knowledge, indexing,
activity, Codex recovery and viewer. Graph-only retained/version authorization
must not become ordinary native policy. Neither conversion nor a historical
restore re-verifies learning or grants project execution authority.

## Preservation dimensions

Check identities, canonical bytes and references, not counts alone:

- native tasks, completed states, fields, scheduling and dependency facts;
- exact human keys/bodies, accepted and pending envelopes, reviews, metadata,
  evidence scopes and attribution;
- original graph revisions, deletion evidence, frozen Types and owned sets;
- Dolt branch/tag/commit references and staged roots, with source working roots
  retained in the checkpoint and only documented candidate writes;
- every unaffected table/config row, current code/catalog generations and all
  retained derived blobs and activity envelopes;
- workspace identity, format, binary/engine provenance and explicit public-ID
  mappings at the format boundary.

Schema8 conversion keeps original revisions under their original tokens in a
separate exact archive, preserves source-format preimages and appends new current
boundaries. `ReadOriginalVersion` returns original canonical bytes, SHA-256 and
Type; `ReadVersion` returns the original typed representation. Terminal tokens
remain terminal. Checked original content can feed guarded historical
composition while current protected identity/membership remains authoritative.
A comparison across changed immutable identity/Type refuses; it does not silently
reinterpret the original representation.

Native adoption preserves original Dolt history and tables, then seeds an
explicit graph boundary. Unsupported pre-existing native retained-history
schemas refuse rather than being truncated or relabelled. Ordinary JSONL
interchange is not a full-history conversion or recovery format.

## Continuity and capacity

Human bodies use canonical retained Memories with private keyed pointers.
Derived catalogs/code and observed activity remain in the existing config plane;
they are not promoted to human learning. Prime selects non-code context and the
current code manifest, plus three handoffs from a full validated activity scan.
The project profile selects the complete current code generation and graph,
plus at most 1,000 recent validated events. Stored history is not deleted.

Ordinary snapshots retain 2,048 live Resources/16 MiB; selected continuity reads
retain 65,536 rows/64 MiB. The explicit compound project profile separately caps
both graph and current-code acquisition at 65,536 rows/64 MiB. Event iteration
has a 12 KiB per-envelope transfer cap and full total/invalid counts. These are
acquisition budgets, not Go heap or SQL-engine memory limits.

Preserving a larger source does not increase ordinary write capacity. Existing
oversized state may shrink or remain unchanged, but no oversized dimension may grow.
General full-plane List can refuse while selected current-context reads succeed.
Any later cleanup/capacity policy requires its own reviewed plan; conversion
never prunes retained generations to make an evidence claim pass.

## Copy-only rehearsal

Capture coherent copies under existing workspace/physical-root gates. Pause only
operator-owned readers when necessary and restore their prior state. Compare
source/copy file hashes before releasing the capture gate. All subsequent engine
opens, conversions, fault injection and recovery use owned copies. Use the
Projects disk-budget launcher, finite space/time bounds and inventoried cleanup.

For a copied embedded schema8 engine and its copied original metadata:

```sh
bd --sandbox graph-convert schema8-copy \
  --source /private/rehearsal/source-engine \
  --metadata /private/rehearsal/original-metadata.json \
  --out /private/rehearsal/new-candidate
bd --sandbox graph-convert verify \
  --receipt /private/rehearsal/new-candidate/receipt.json
```

The source must be canonical and outside its bound project workspace. Output is
a new private directory outside the source/workspace; it contains a native full
backup, independently restored converted candidate and verification receipt.
No source selector, real database, installed binary, hooks or project code is
changed. Schema8 copy receipts refuse `activate` and `rollback`; restore their
native checkpoint into another fresh isolated destination for recovery.

Planning binds exact table/view definitions, source roots, branch/tag commits,
workspace identity and every human key/body hash. Changed plans, malformed or
missing human pointers, unsupported formats and dirty non-main branches refuse.
Conversion faults may leave additive DDL only in the discardable candidate;
source authority and the full native checkpoint remain intact.

## Qualification and delivery

Require native-to-Preview3 and schema8-to-Preview3 rehearsals on the same fork
source, original-token/current reads, accepted/pending knowledge preservation,
mutation guards/rollback, pin/re-pin/retype behavior and interrupted-conversion
refusal. Restore each converted full native backup into a fresh engine/process;
compare every table hash, root, branch/tag/log reference and continuity profile.
Independent old-format reads anchor historical byte comparisons.

The synthetic schema8 fixture under `internal/storage/graphstore/testdata` was
produced with the qualified old provider and contains no project records. Its
regressions cover exact original reads, historical composition, source
preservation and pre-commit failure. Run normal affected-package tests and the
repository test/lint gates; upstream receipts do not qualify the fork tuple.

Keep source reviewable on `codex/preview3-integration-20261024`. Execution state
belongs in operator Beads; project captures, hashes, logs and recovery material
stay private. Installation and real activation are separate work, requiring a
fresh coherent capture and resolution of any capacity/admission limits.

## Explicit operational profile

`--graph-profile continuity-v1` selects capacity for one graph CLI process or
`graphstore.Options.OperationalProfile` for one library open. It is not a setting,
environment fallback or automatic response to imported size. It changes no
persisted format and does not make an old binary claim new capacity. Omission
retains the conservative ordinary policy; unknown profiles and native CLI routes
refuse before opening storage.

The profile permits 16,384 live Resources and 2,097,152 retained config memory rows
(up to 8 GiB including key/row charges). The explicitly selected current graph acquisition is 64 MiB; the ordinary
profile remains at 16 MiB. These are finite logical limits, not disk quotas or heap guarantees.
Selected code/context acquisition remains 65,536 rows/64 MiB. All dimensions are
checked before/after each write within its transaction; an already oversized
dimension can stay or shrink. Human records and retained generations are never
removed to fit the policy. Full-plane List, ordinary CurrentSnapshot, input
limits, owned-Link limits and generic traversal retain their existing ceilings.

Admitted paths are guarded CLI Resource/Issue mutation and discovery, selected
prime/knowledge/code/activity/project-viewer continuity, and ordinary SQL-server
BDP GET/HEAD discovery, exact public Resource/Type, properties, collections,
selectors, incident Links, aggregates, validators and process-local pagination.
A public `links/...` ID can be a Bead, and a `beads/...` ID can be a Link; the
catalog decides kind. Exact installed Type URLs remain reserved. Foreign/missing
IDs and private-locator-only addresses cannot acquire authority.

HTTP writes, history/version URLs, alias resolution, embedded CLI BDP serving,
proxy conversion, hot restore and independent-clone graph merge remain refused.
The profile retains at most 64 MiB/16,384 items per pagination snapshot and
128 MiB across cached snapshots. Every future page is checked below 7 MiB before
issuing a cursor; the 8 MiB response ceiling stays unchanged. It may
refuse a large selection; pagination does not bypass acquisition bounds. No BDP
write or full History capability is advertised.

Before later real activation: take a fresh coherent capture of each workspace,
record the exact selector/binary/engine tuple, verify a matched full native
backup/restore and retained history, qualify writes and selected reads under the
explicit profile, and arrange quiescent writers. Review the measured capacity
headroom and supply the explicit profile to each writer launcher/hook; this phase
does not rewrite existing hooks or silently raise their default capacity. Conversion receipts from this copy-only phase cannot activate or roll
back a real selector; later writes require a new recovery checkpoint. Activation
and installation need their separate authorized work phase.

## Explicit workspace publication

With separate owner authorization, `graph-convert plan` accepts the existing
embedded native or schema8 workspace, exact Scope and optional explicit
`--graph-profile continuity-v1`. The private plan binds that profile, the original
selector/marker, binary and complete database state. Schema8 retains its authority.
`prepare --actor NAME --writers-stopped` checkpoints and independently restores
a complete native backup, converts only the restored candidate and seals it.
It changes neither the live engine nor selector.

`activate --receipt FILE --writers-stopped` uses the existing exclusive gates,
same-filesystem atomic renames and interruption fence. Both formats retain their
original engine and selector; schema8 rollback restores its exact old marker.
Interrupted publication and rollback resume only against matching sealed states.
Rollback refuses all later writes; after activation, pair the new binary with a
fresh native checkpoint for later recovery. Copy-only schema8 receipts remain
ineligible for publication. No hot restore, project execution, hook trust change,
implicit capacity selection or history pruning is added.
