# DOX.md — Internal implementation

## Purpose

Own internal implementation within the fork.

## Ownership

Private packages, templates, services and adapters. Storage has its own contract.

## Local Contracts

Root DOX.md and the parent chain remain binding.

- Dolt server process probes distinguish failed inspection from a confirmed non-Dolt process. Preserve a live pid/port when `ps` fails; EPERM from a liveness probe means alive. Stop refuses to signal an unverifiable or recycled PID and retains state for diagnosis.
- On macOS, a newly launched managed Dolt server is ready only after a greeting and bounded `lsof`/`ps` proof that every listed port holder is the child or its descendant. Unknown ownership waits until the configured readiness deadline and fails without publishing pid/port; a proven foreign holder retries only an ephemeral port. Failed startup kills/reaps only its launched child/group. Other platforms retain greeting-based readiness. Existing-server adoption and Stop inspection contracts remain separate.


- Jira pull preserves supported ADF block/inline structure as Markdown through the shared description converter; v2 strings and raw fallbacks remain supported. Rendering is bounded to 1 MiB input, 2 MiB output, depth 64 and 50,000 nodes/marks; exceeding a bound retains the original JSON. Unknown containers retain descendant text. Push still emits plain paragraph ADF and is not a rich-text round trip.

- HTTP event streams revalidate bearer credentials before delivery and around journal reads, including idle and backlog passes. Successful removal terminates the stream; failed token-file reloads retain the last-good token set.
- Terminal Markdown rendering removes raw controls and numeric entities that decode into controls before Glamour parsing; renderer-generated styles and printable entities remain supported.

- OpenTelemetry remains opt-in and separate from removed anonymous usage metrics. Its default resource collects host, process identity and runtime metadata without argv or process owner; explicit environment attributes still override/extend defaults. Command spans retain credential-scrubbed arguments in local console traces only.

- Recipe SharedPaths explicitly declare section-managed instruction files; other paths retain their existing whole-file ownership semantics.

- AI issue compaction uses the first text response block without reading thinking/redacted contents. Missing/blank text or max_tokens replies fail before compaction; malformed replies do not retry. Existing transport retry and optional audit behavior remain.

- `metrics` owns inert compatibility hooks without collection, transport, queue, machine identifier or process launch. All Dolt version callers reuse the private-cwd/config probe, disabling metrics/update checks without changing operator settings.

- Query lexing decodes UTF-8 while retaining byte offsets and whole-rune backup. Quoted strings preserve consumed bytes, including invalid UTF-8 and unknown escaped bytes; invalid bytes outside quotes are rejected. Numbers and durations use ASCII digits; identifiers allow combining marks after their first character. Literal NUL is rejected, including inside quotes, and is distinct from EOF.

- Metadata-only fallback port discovery reads legacy configuration without migrating it. Diagnostic dialing honors the actual legacy-port precedence. Named runtime ports declare an external lifecycle unless the exact endpoint has a live workspace-owned server; explicit external/shared/remote declarations remain authoritative. This does not change the embedded/proxy storage selector.
- Native target and artifact discovery reads metadata without migration and refuses graph markers, non-native modes and unreadable metadata. Target YAML settings honor the local layer only beside a project config; null/commented-out settings fall through while malformed or unreadable values refuse.

- Strict readonly shared workspace gates open files read-only; absent ordinary gates yield empty handles without creation. Existing-only proxy/Graph Preview gates still refuse absent files. Existing gates exclude maintenance, empty handles release safely, and exclusive callers retain creation.

- YAML unset validates the proposed edit before writing: refuse multiline, orphaned or collateral changes; absent keys are exact no-ops. Preserve EOF, private user-file permissions and whole-document indentation. Public unset APIs retain error-only results. Collateral validation interns exact path components across the before/after trees rather than expanding ancestor strings per leaf; retain the 64-level limit and alias identity.

- Embedded dependency corrections retain explicit main-module pins for both Dolt and its SQL analyzer. View alias identity must survive normal/prepared predicates; preserve existing diff/savepoint behavior and qualify the complete tuple before installation.

- Named infrastructure counts and listings share the existing storage-plane selection; native and UOW counters reload the configured infra vocabulary per named-type request. Bare counts retain durable-only semantics. Formula extraction and substitution agree on padded identifiers and step-note references.

- Shared user-level agent templates apply only in initialized Beads repositories and retain the fork conservative authority profile. Project templates keep their existing wording.

- Successful tracker pulls record the hash of the imported representation with the assigned local ID, after the issue write. Dry runs and failed writes do not advance this push baseline. Cache and issue writes remain separate, rather than crash-atomic.
- Initialized GitHub/GitLab trackers extract fallback identifiers only from their configured origin and repository/project. Keep provider-family recognition separate so foreign references are skipped on push rather than recreated. GitLab numeric project IDs resolve their namespaced path once through the configured API before syncing; failed/missing metadata, mismatching numeric IDs and stale configured paths refuse initialization. Group pulls retain exact-URL matching for other projects, without bare-IID fallback or cross-project push.

- Remote Dolt endpoints discard local bookkeeping ports, preserve authoritative configured ports over the legacy environment spelling, and use strict legacy parsing. Advice names remote hosts and the specific stale port file; only the sole external declaration suggests returning to managed mode.

- Workspace gates classify unsupported locks separately from contention and refuse both shared and exclusive admission. A writable first-busy check may probe one private sibling; readonly/existing-only paths create nothing and retain their busy behavior. Preserve OS lock authority and never delete a gate.

- Path-parameterized discovery stops a linked worktree walk at its repository boundary when its main-checkout fallback is usable. An unrelated parent store cannot win over that fallback; ordinary non-worktree walk-up remains unchanged.

- Backend recovery messages name the existing metadata.json or legacy config.json without migration. Unsupported metadata-driven opens refuse before migrating selectors or creating gates.

- Explicit Git-root discovery must not read or mutate the current-directory cache.
- IPv6 loopback is local for classification but never owned by the IPv4-only managed server; preserve remote credentials and TLS server-name normalization.

- Bootstrap may supply its selected server host before metadata publication to the common port resolver. External remote/IPv6 hosts discard local port-file bookkeeping; shared-server and authoritative source precedence remain unchanged.

- Host normalization trims surrounding whitespace before removing IPv6 brackets, shared by address, TLS-name and credential resolution. Missing ports for unmanaged endpoints produce shared configuration advice instead of a port-zero reachability check; managed lifecycle ownership is unchanged.

## Work Guidance

Tracker adapters share configuration resolution: local integration credentials never query Dolt, including Notion authentication; authenticated tracker endpoints use explicit local YAML/environment. If no local destination exists, a historical nonempty synced endpoint or store failure refuses connection instead of choosing a public default. Ordinary values keep storage-first/environment fallback including store errors. Credential classification also recognizes case and surrounding-space variants without normalizing ordinary stored keys.

Keep orchestration policy outside the issue-tracking core; follow ../engdocs/PROJECT_CHARTER.md.

Use line-local gosec annotations for intentional native directory handles or syscall buffer pointers only after checking descriptor identity or the bounded synchronous buffer contract; do not disable rule categories globally.

## Verification

Use the affected existing checks selected in engdocs/TESTING.md; verify indexed child contracts.

## Child DOX Index

- [gittestenv/DOX.md](gittestenv/DOX.md) — Shared Git fixture startup isolation.

- [storage/DOX.md](storage/DOX.md) — Storage boundary.
- [graphview/DOX.md](graphview/DOX.md) — Shared native offline/live graph viewer.
- [httpapi/DOX.md](httpapi/DOX.md) — HTTP transport and exclusive read-only viewer mode.
- [knowledge/DOX.md](knowledge/DOX.md) — Source-backed project knowledge and offline graph.
- [codeindex/DOX.md](codeindex/DOX.md) — Derived multilingual symbols, static references and index lifecycle.

- [activity/DOX.md](activity/DOX.md) — Bounded observed session/turn journal, distinct from reviewed knowledge.
- [graphpatch/DOX.md](graphpatch/DOX.md) — Bounded ordered graph property patches.

- [legacyimport/DOX.md](legacyimport/DOX.md) — Preview3 preparation: bounded decoding of ordinary Beads export records.
- [memorycatalog/DOX.md](memorycatalog/DOX.md) — Preview3 preparation: the selected Preview3 Memory Type declarations.
- [memoryquery/DOX.md](memoryquery/DOX.md) — Preview3 preparation: bounded typed Memory relationship traversal.
- [typecatalog/DOX.md](typecatalog/DOX.md) — Preview3 preparation: fixed Type-family descriptors and selection.
- [typeconstraints/DOX.md](typeconstraints/DOX.md) — Preview3 preparation: pure validation of selected Type/Link outcomes.
