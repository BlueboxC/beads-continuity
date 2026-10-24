# Beads Continuity

This fork extends [gastownhall/beads](https://github.com/gastownhall/beads) to
preserve project direction, existing modules, decisions and resolved problems
across coding sessions. It builds on upstream **v1.3.1** at
`c1c4b642ac1c08d8c828007a1c2f96e47e43ef7c` and the published bootstrap
`6fd8ea6288a1c9c5fa23dcb22a47183e779ca59c`.

The continuity extensions share Beads' existing **Dolt memory plane**, without
another graph service, vector database or hosted model. Native correctness
repairs include forward blocked-state migrations 0067 and ignored/0027; shipped
predecessors remain unchanged. Embedded backup compatibility uses the qualified
Dolt dependency tuple described below; the standalone server policy remains separate.
Upstream attribution and the MIT license remain intact. Fork additions are
available on `codex/project-continuity`; upstream installation channels do not
contain them. See [installation and workflow](docs/core-concepts/fork-continuity.md).

## Ownership and bootstrap follow-ups

The follow-ups to upstream PRs #7797, #7803, #7798 and #7804 preserve real
storage in redirect-expected folders, refuse externally managed IPv6 local-root
migration (including prepared retries), admit direct factory backends before
legacy metadata migration, and resolve a fresh external clone without stale
local port bookkeeping. Registered backend dispatch, ordinary storage-free
cleanup and authoritative/shared port precedence remain intact. PR #7807's
compatibility distinctions are adapted to this fork's actual 2.6.0 embedded
engine; external CLI/server policy and dependency pins remain unchanged.

The additional #7803 delta at `c9dc31d491fa` is adapted with contributor
regressions: unmanaged endpoints with no port get configuration advice instead
of port-zero probes or an auto-start skip, and `doctor --server` fails.
Host normalization trims surrounding whitespace for dialing and credentials;
the proxy backend's TCP Dial shares IPv6 address construction. Owned/proxied
lifecycle, Unix sockets, engine pins and existing recovery guards are retained.

## Network privacy

This fork removes anonymous command-usage collection and upload, including
machine identifiers, event queues, config bootstrap and detached flushers.
Saved upstream preferences and environment overrides cannot enable it.
`bd metrics on` fails; other metrics commands are read-only compatibility
endpoints. Existing user configuration and old queues remain untouched.

Beads version diagnostics are local. Managed Dolt version probes isolate their
cwd/configuration and disable metrics and update checks. The ordinary test
runner seeds those settings in its private Dolt root. This governs fork-owned
invocations, not independently launched upstream binaries. Explicit sync,
integrations, downloads and operator-enabled OpenTelemetry retain their network
behavior.

Upstream already discloses command-usage metrics in its
[metrics reference](https://github.com/gastownhall/beads/blob/main/docs/cli-reference/metrics.md).
This is an intentional fork policy difference.

## Added capabilities

| Area | Added behavior | Boundary |
| --- | --- | --- |
| Metadata capacity | CLI metadata accepts JSON objects up to 4 MiB, inline or through `@file`, preserving exact values, merge behavior, revision guards and retained versions. Graph status reports this input budget. | Body/property and complete-read budgets remain separate; oversize or invalid inputs refuse before mutation. No schema or database conversion. |
| [#7559](https://github.com/gastownhall/beads/issues/7559) | Settings API refuses local-only credentials before storage; native and UOW SQL config writers also reject reserved integration credential keys. Notion authentication uses local YAML/environment. | Fork correction; existing replicated secrets/history are not automatically migrated or scrubbed |
| [#7560](https://github.com/gastownhall/beads/issues/7560) | Authenticated tracker destinations require local configuration; historical synced URLs and failed endpoint reads refuse connection. Covers direct CLI discovery plus Linear API-key/OAuth clients. | Fork correction; self-hosted paths remain supported and synced rows are not automatically migrated |
| [#7576](https://github.com/gastownhall/beads/issues/7576), [#7577](https://github.com/gastownhall/beads/issues/7577) | Proxied init saves adopted project identity locally in both modes and stops publishing per-clone fingerprints into the shared database. Existing shared tracking rows are retained. | Git-derived fingerprints and the existing local version file remain available; no identity migration or history rewrite |
| [PR #7571](https://github.com/gastownhall/beads/pull/7571) | Write transactions release their pool session when a body panics, preserving the original panic. Adapted from Raymond W. Ko's correction and regression test. | This baseline has the pool helper, not the contributor's newer pinned-connection delete fence |
| [#7569](https://github.com/gastownhall/beads/issues/7569) | Embedded restore rehearses native restore and commit in a disposable engine before replacing the target, rejecting incompatible backups with an actionable error. | Supports native Dolt 2.4.2 backups with the dependency tuple below; additional temporary disk/time cost, no discarded unknown fields or atomic-publication claim |
| [PR #7673](https://github.com/gastownhall/beads/pull/7673) | Strict readonly refuses backup mutators and import, including import previews; restore refuses before exclusive gates. | Adapted from Julian Knutsen and Jim Wordelman; migration-freeze cleanup and fork proxy/Graph Preview routes are retained. |
| [PR #7672](https://github.com/gastownhall/beads/pull/7672) | Strict readonly shared gates open existing files read-only and never create missing files; repeated commands close prior storage before releasing gates. | Adapted from Julian Knutsen, Jim Wordelman and Donna Box. Missing ordinary gates provide no maintenance exclusion; strict proxy/Graph Preview gates still require presence. No newer writer-intent queue is transplanted. |
| [PR #7670](https://github.com/gastownhall/beads/pull/7670) | A scanner error refuses a flat-key YAML update before writing, preserving the complete configuration. | Contributor tests retained. Oversize lines fail explicitly; set-many retains earlier successful keys and is not an atomic batch. |
| Documents and DOX | Selected document catalogs, source hashes and explicit objectives, constraints, decisions, modules and solutions | A scanned document is source material; scanning does not extract or certify a learning. |
| Supervised learning | Source-backed pending proposals, explicit accept/reject reviews, immutable history and pinned activity origins | A supervising human or agent reviews evidence. Existing solutions and historical verification scopes are preserved. |
| Code structure | Incremental structure in 23 languages/formats: Python/Go/JS/TS/Java/C#/Rust/C++, PHP/C, Bash/PowerShell, HTML/CSS, GraphQL/XML, Kotlin/Swift/Dart and SQL/JSON/YAML/TOML; named symbols, source locations, conservative references and reviewed learning links | Static references are not runtime proof. Markup/configuration retain names and hierarchy, not scalar values. Dynamic or ambiguous references remain unresolved. |
| Change review | Conservative inverse static file dependencies, shortest witness paths, associated learnings and candidate tests; unique identical-content move drafts | Dynamic impact and edited/ambiguous moves require manual analysis; a draft never renews prior evidence. |
| Bounded structures | Large file structures split into integrity-checked content-addressed fragments in the same Dolt, within existing row/decoded limits | Interrupted publication preserves the previous complete index. Dense XML reports ASTLimit without partial symbols. Older binaries need their previous complete derived generation restored before downgrade. |
| Derived retention | Read-only prune plan and explicit guarded atomic cleanup in upgraded direct embedded workspaces | Human records and Dolt history remain intact; cleanup is never automatic and does not promise filesystem savings. |
| Native graphs | One interactive task/knowledge/code explorer with typed colors, grouped drag, zoom, filters, source/symbol details, back navigation and separate Fit/Reset | Families are presentation scopes, not ownership or task blockers. Offline exports are snapshots. |
| Live viewer | Read-only graph/history queries, Actualizar grafo, Manual/Automático selection, visible disconnects and preserved layout | Refresh reads saved data; it does not parse code or renew evidence. |
| Derived maintenance | Explicit `maintain once` and `maintain watch` over saved document/code selections | Only approved directory roots discover new files. Human records and task state are not rewritten. |
| Session continuity | Opt-in observed operations, reported handoffs and session markers; bounded prime recovery and compact refresh/fallback | Delivered supported hooks determine coverage. Hooks do not read transcripts or accept learned solutions. |
| Graph Preview | Opt-in typed Beads/Links with four native dependency kinds, a bounded 2,048-Resource inventory, retained versions, guarded mutations, BDP Read and an integrated adapter for learning/code/Codex recovery/native viewer in one Dolt | Fresh schema-8 workspaces; original schema-7 Types remain unchanged. Explicit embedded conversion preserves native history and human envelopes through a full checkpoint and isolated rehearsal; publication resumes sealed transitions, and rollback refuses later writes. Canonical human Memories retain history; derived catalogs/code/activity share the same database. Existing projects retain their format. Embedded capture/maintenance only. See [Graph Preview](docs/reference/graph-preview.md) and [provenance](graphops/PROVENANCE.md). |
| GitLab gates | Optional `gl:pipeline` and `gl:mr` checks through existing authenticated glab; strict states, nested project paths and exact branch/HEAD pipeline discovery | No token storage or automatic provider activation. Fixture/process qualification does not establish live GitLab credentials. |
| Hook diagnostics | Cold-output next-prompt fallback and bounded private compact/failure metadata with typed read-phase/reason and commit build label | Local stdout success does not prove native context admission or universal reliability. |
| Bounded reads | Literal memory-key prefix selection across server, embedded and UOW adapters; selected-symbol provenance | Reads avoid unrelated stored values while retaining the existing case-insensitive text search. |
| Efficient queries and process lifetime | Embedded native graph/maintenance read only the current index generation in one read snapshot; immutable parser fingerprints, per-query source snapshots, bulk graph dependencies, bounded recent-event selection and shared parser/client execution | Freshness checks, total/invalid counts and existing evidence are preserved; no automatic deletion or retry. MCP calls time out after 120 seconds and report an uncertain outcome. |

Detailed references: [knowledge](docs/core-concepts/knowledge.md),
[code index and maintenance](docs/core-concepts/code-index.md),
[Codex hooks and activity](docs/integrations/codex.md), and
[HTTP operation](engdocs/SERVE_RUNBOOK.md).

## Additional correctness fixes

Adapted contributor proposals [#7720](https://github.com/gastownhall/beads/pull/7720),
[#7723](https://github.com/gastownhall/beads/pull/7723),
[#7724](https://github.com/gastownhall/beads/pull/7724),
[#7725](https://github.com/gastownhall/beads/pull/7725),
[#7726](https://github.com/gastownhall/beads/pull/7726),
[#7727](https://github.com/gastownhall/beads/pull/7727),
[#7729](https://github.com/gastownhall/beads/pull/7729),
[#7730](https://github.com/gastownhall/beads/pull/7730),
[#7685](https://github.com/gastownhall/beads/pull/7685) and
[#7705](https://github.com/gastownhall/beads/pull/7705): exact-port credentials,
untracked-server diagnostics, tracked worktree hook isolation, formula paths,
padded variables, fresh-server refusal, infra counts, role-based links and
truthful duplicate totals. Contributor regression cases are retained where the
baseline supports their API; no newer HTTP-connected backend or count flags
are introduced. Graph Preview, proxy routing and prior engine protections remain.

## Build toolchain

The root and both independent Go examples require and select Go 1.27.3 for
Dolt 2.6.0 and its analyzer. The Nix overlay pins the official 1.27.3 source
archive and SHA-256; MCP CI jobs pin uv 0.13.5. All lint lanes and the optional pre-commit hook pin golangci-lint v2.15.0, because v2.10.1 cannot read Go 1.27 export data. The earlier toolchain hardening adapted Julian Knutsen's
[PR #7732](https://github.com/gastownhall/beads/pull/7732), including its
updated Go 1.26 devcontainer. The fork has no Bazel setup to transplant.

## Embedded engine compatibility

The embedded engine keeps `driver/v2 v2.4.1`, starting from coffeegoddd's
[upstream proposal #7254](https://github.com/gastownhall/beads/pull/7254).
It pins Dolt v2.6.0 plus the retained storage corrections, catalog and SQL fixes at
[BlueboxC/dolt `212be23824de`](https://github.com/BlueboxC/dolt/commit/212be23824def28adfbfb25cd89be67ed91349ae),
with the [SQL analyzer correction](https://github.com/BlueboxC/go-mysql-server/commit/1aa8b38f5b78da630065da597096598bd17df318) on base `38a1fb50b434`. The engine remains a Go dependency;
Beads does not implement engine-specific SQL workarounds.

The 2.6.0 merge reuses upstream corrections already present in the carried patch set. Its ORT merge-base resolver retains this fork's explicit refusal of conflicting virtual bases at merge and three-dot diff entry points. Native backups from Dolt 2.4.2 and the preceding embedded tuple pass isolated restore, logical roots/ref/history comparison, subsequent writes/reopen and a second backup readable by both engines. Installation still requires matched data/binary recovery and guarded readback; the driver, project schema and external CLI/server policy do not change.

This tuple adds the bounded catalog fix for Dolt #12122, mixed-case CHECK merge
references (PR #12152), matching remote-upstream cleanup (PR #12153/#11529),
and the remaining supplied #12100 SQL repro corrections with operand-appropriate
MOD metadata (#12126). Contributor tests and attribution live in the engine and
analyzer forks. Ordinary indexed string plans are retained; schema-bearing empty
relations use a correctness-first reordering fallback with no optimizer estimates.
The 23 supplied repro scripts pass their expected result/error controls in normal
and prepared modes. They do not establish general MySQL/JSON-path compatibility.
Native 2.4.2 backup round trips and isolated retained-data/hash/history checks are
required for this exact tuple. Subquery unnesting also retains ordinary negated
filters used by Graph Preview label, lifecycle and ready-work queries.

SQL remote removal now saves repository state before publishing session-map
changes, adapting elianddb's final #12153 correction. Provider/read/save failures
retain the live remote and matching tracking entries. This preserves unrelated
remotes and does not make concurrent or partial repository-file writes atomic.

The engine supports recursive clean virtual merge bases (#12050) and matching
ancestor primary-key projection (#12012), with normal/prepared regression scripts.
Conflicting best ancestors, incompatible key layouts, NULL/colliding ancestor keys
and unsupported specialized indexes refuse explicitly. Branch/working/tag references
remain unchanged by diff queries, although deterministic virtual objects may be
materialized. Conflict-base address traversal preserves those objects through full
GC, native backup/restore and cold reopening. Older engines lack that additional
reachability rule: rollback after these feature writes requires matching data and
binary preimages. This does not implement arbitrary key reconciliation or automatic
resolution of conflicting virtual bases.

The official 2.5.0 base already propagates backup source-root errors
([Dolt #12057](https://github.com/dolthub/dolt/pull/12057)) and closes embedded
branch/binlog resources ([#12075](https://github.com/dolthub/dolt/pull/12075)).
The small dependency fork additionally rejects incompatible primary-key diffs
instead of returning mispaired rows, adapting elianddb's
[proposal #12017](https://github.com/dolthub/dolt/pull/12017), and restricts
unscoped history shortcuts to the selected head's ancestry
([#12104](https://github.com/dolthub/dolt/issues/12104)). Rejection prevents
incorrect output; it does not add arbitrary primary-key-change alignment.

- [#12139](https://github.com/dolthub/dolt/issues/12139) preserves every child-parent diff for both `to_commit` and `from_commit` shared-parent lookups and full history scans, including prepared queries.
- [#12138](https://github.com/dolthub/dolt/issues/12138) snapshots savepoints from session branch state, avoiding unrelated create/drop races while retaining later-used branch rollback.
- [#12141](https://github.com/dolthub/dolt/issues/12141) preserves the column identity of view projections in normal and prepared filters, adapting angelamayxie’s [GMS PR #4044](https://github.com/dolthub/go-mysql-server/pull/4044). The Dolt module, Beads and its two independent Go example modules explicitly replace the analyzer because Go dependency replacements are not transitive. Retain the newer MIN/MAX index-bounds and decimal MOD fixes absent from the proposal branch.

Contributor tests and attribution remain in that engine fork. Its tag checks also preserve 2.5.0's crossed-column protection and historical warning behavior.

The matched GMS version includes angelamayxie's merged
[MIN/MAX fix #4021](https://github.com/dolthub/go-mysql-server/pull/4021).
The empty/populated aggregate regression remains. No multi-branch commit
option is enabled by Beads, and its storage/API/schema boundaries stay intact.

FreeBSD requires the exact `shrisha/file-locks` commit `1006a916595c` from
[PR #6](https://github.com/dolthub/file-locks/pull/6), pinned by `replace` in
all three Go modules. That upstream proposal is still unmerged: it extends the
Unix build tag to the listed BSD targets without changing the lock algorithm.
FreeBSD qualification is cross-compilation, not native execution.

Compatibility qualification restores old embedded and native Dolt 2.4.2 backups,
compares all current tables and reachable history/refs, then writes, reopens,
backs up and restores again. Corrupt backups still refuse before target
replacement. This is not a format-conversion command or automatic downgrade:
retain the matching old binary and full database backup before the first new
engine write, and reconcile later writes before any recovery.

The standalone Dolt CLI/server pin and its historical hard-reset measurements
remain unchanged. Reading a 2.4.2 backup does not qualify that server version;
the unpatched 2.4.2 CLI also reproduces the MIN/MAX regression.

## Public source and private operation

This branch includes the verified fork corrections independently of upstream
review. Upstream pull requests remain subject to separate owner authorization. Project databases, learned project
records, chat/session exports, machine configuration, credentials, backups and
installation evidence remain outside this repository. Source publication does
not authorize publishing `refs/dolt/data`.

Initialize each chosen project explicitly; global setup does not enroll new
projects. Activity capture, derived maintenance and the live viewer have
separate opt-in steps. Use the normal Codex hook trust flow; setup never grants
trust. Neither indexing, source currency nor an accepted learning authorizes
project execution, deployment or reopening completed work.

## Included corrections

The following native fixes adapt upstream contributors' proposed patches while
preserving this fork's existing corrections:

| Report | Included behavior | Source proposal |
| --- | --- | --- |
| [PR #7375](https://github.com/gastownhall/beads/pull/7375) | Exact native delete IDs across single, batch and routed previews/writes. | A3Ackerman, proposal `298abfd8ef5a53ea540353ce9cae74a27ebbe0fe`; adapted to this baseline |
| [PR #7583](https://github.com/gastownhall/beads/pull/7583) | Restore create fields, dependency sources and comment IDs before replay. | julianknutsen, proposal `b429a36d68531dee5519747aad15c2f016c93e7a`; adapted to this baseline |
| [PR #7600](https://github.com/gastownhall/beads/pull/7600) | Shared heartbeat role, live-claim re-arm fence, orphan cleanup and mutation hooks; adapted to Graph-only retained history and existing UOW commit ordering. Scoped CALLs drain all result sets, retaining late errors without replaying committed SQL. | julianknutsen, proposal `42551c5ec7de25a7ab24110681500c3580abe0cd`; result draining follows fixup `810913c4f8f356baff01c0e4dbe9689fc52899e7` |
| Reclaim ownership guard | An expired lease may release only its current matching claim, using heartbeat's existing actor equivalence. Observed holder and assignee are rechecked; unrelated orphan leases preserve the claim and produce no reclaim event. | Fork follow-up; shared native, embedded and UOW regression cases |
| [PR #7584](https://github.com/gastownhall/beads/pull/7584) | Preserve uncertainty after delete/sweep history publication; no replay. | julianknutsen, proposal `639fa01441f12cafa71a56caf416844f923a0f04`; adapted to this baseline |
| [PR #7585](https://github.com/gastownhall/beads/pull/7585) | Deadline-bounded write retry backoff. | julianknutsen, proposal `ee7f5c5053ab5888944ab59590f0057a3b9834f1`; adapted to this baseline |
| [PR #7586](https://github.com/gastownhall/beads/pull/7586) | Retry complete wisp write transactions. | julianknutsen, proposal `e77510f8d75c902b0ac004a6baa293c6b03af45d`; adapted to this baseline |
| [PR #7587](https://github.com/gastownhall/beads/pull/7587) | Explicit replay opt-in for confirmed rolled-back conflicts; restore callback inputs. | julianknutsen, proposal `c035e8d319e1f22cd5a41916903ae2286d528e46`; adapted to this baseline |
| [PR #7609](https://github.com/gastownhall/beads/pull/7609) | Refuse accidental account-home init and scrub inherited Git routing. | gascity-eddie[bot], proposal `229ff86cf87817aa1ec8d0ddc03615d58869fa6a`; adapted to this baseline |
| [PR #7606](https://github.com/gastownhall/beads/pull/7606) | Missing peer credentials alone permit unauthenticated fallback. | gascity-eddie[bot], proposal `7c8bc7a437aecb1a4d3a2cf6f237c64531147760`; adapted to this baseline |
| [PR #7601](https://github.com/gastownhall/beads/pull/7601) | Warn after already-durable routed creation when history publication fails. | gascity-eddie[bot], proposal `312d0dd5a65be839314ae2c0f25995272bdde8a9`; adapted to this baseline |
| [PR #6012](https://github.com/gastownhall/beads/pull/6012) | Explicit second-precision UTC provenance ingest timestamp. | davejorg, proposal `694732c955b08058a2afc80a26cf951e755496aa`; adapted to this baseline |
| [PR #7603](https://github.com/gastownhall/beads/pull/7603) | Bulk ID reads in batches of 200; additionally deduplicate across batches. | brandonmartin, proposal `12846c6a3a28b56c16b1aff5e42091c6bdd78b24`; adapted to this baseline |
| [PR #7591](https://github.com/gastownhall/beads/pull/7591) | Select list dependency records by displayed IDs; additionally bound cyclic pretty-tree traversal. | julianknutsen, proposal `13542b8380f78dfa10e76dfaae70e049035364bf`; adapted to this baseline |
| [PR #7593](https://github.com/gastownhall/beads/pull/7593) | Sweep old closed circuit files once per process. Literal 23-hour retention and 25-hour expiration tests pin the 24-hour policy in both directories. | julianknutsen, proposal `ee0bd655988d38b9ac2a2c5eea23584bc3361607`; independent-age tests adapted from fixup `73caad3aa6d0abbf5bb40806ce8ae5411d9c4d39` |
| [PR #7596](https://github.com/gastownhall/beads/pull/7596) | SHOW TABLES exact case-insensitive table checks; existing column probes unchanged. | julianknutsen, proposal `2b835facfcfdd6e357b79fbdc9982063bbbe837b`; adapted to this baseline |
| [#7597](https://github.com/gastownhall/beads/issues/7597) | Proxied `ship` publishes capabilities with native force, preview and idempotence rules; `duplicate` links and closes atomically through the existing UOW. Success output follows commit. | Refusals preserve data/history; readonly remains enforced and existing proxied supersede is retained. No schema or format change |
| [#7557](https://github.com/gastownhall/beads/issues/7557) | Strict readonly proxy reads require a live authenticated proxy, existing current-schema database and existing shared gates; no bootstrap, migration, version stamp, hook, profile or maintenance writes. Driver-level SQL guards reject DML, DDL, procedures and exports, including prepared/query paths; readonly UOWs cannot commit. | Fork implementation; uses rjgeng’s [#7042](https://github.com/gastownhall/beads/pull/7042) classifier and three regressions for bee-ghosttrack’s [#7033](https://github.com/gastownhall/beads/issues/7033); no new SQL principal or server-wide privilege claim |
| [#7558](https://github.com/gastownhall/beads/issues/7558) | Proxied merge slots share native holder/waiter semantics and transaction retries; supersede links and closes atomically with rollback on refusal. | Fork implementation following the reported downstream need; no schema or lease policy change |
| [PR #7684](https://github.com/gastownhall/beads/pull/7684) | Refuse multiline, flow-parent, orphaned and collateral YAML unsets before writing. Preserve absent-key bytes, EOF, aliases and the fork’s error-only API; match nested roots at the document’s actual indentation. | Contributor implementation at `5aa98ae7e177f605a641e35e62f4bff157485535`, adapted with project/user-file regressions and indented-root follow-up |
| [#7706](https://github.com/gastownhall/beads/issues/7706) | Git-fixture TestMain entry points and the canonical runner clear inherited Git routing/configuration before setup; tests may explicitly set their own environment afterward. | Fork correction following voxist.executor’s reproducer; outer-repository file/index/config/history preservation regression |
| [#7527](https://github.com/gastownhall/beads/issues/7527) | Proxy normal connections are quiet; existing debug enables detailed traces. Active log and one retained tail stay bounded to 4 MiB each at runtime and startup. | Fork correction following A3Ackerman’s report; no change to idle-watcher or backend semantics |
| [#7519](https://github.com/gastownhall/beads/issues/7519) | Closed/started graph-plan nodes carry creation from the same clock read; open nodes retain server stamping. | Harry Miller’s [#7521](https://github.com/gastownhall/beads/pull/7521) at 17c2cc20e6c3be590c8f0fe45240e7544d495f6f, retaining all three status regressions |
| [#7523](https://github.com/gastownhall/beads/issues/7523) | Direct server config and memory writes create scoped version commits; KV set/clear mark embedded writes and use the screened server policy. Batch/off defer; unrelated dirty tables remain uncommitted. | Aaron Lippold’s [#4383](https://github.com/gastownhall/beads/pull/4383), carried and reconciled by Sean Martin Smith in [#7299](https://github.com/gastownhall/beads/pull/7299) at e6e96bb692fcd60e70b25648e74049442f12fe1e; contributor regressions retained and extended for KV and real policy checks |
| [#7531](https://github.com/gastownhall/beads/issues/7531) | Hook uninstall cleans the reactivated install after clearing a managed hooks path. Fresh Git resolution avoids stale caches; foreign sections, backups and custom configuration survive. Failed follow-up resolution reports failure. | Rongjun GENG’s [#7541](https://github.com/gastownhall/beads/pull/7541) at 3a65a7b338938d16c514fd7c7edff225c92ab5bd, adapted to the baseline without importing the unrelated hook-context stack; contributor regression retained with worktree, global fallback and preservation cases |
| [#7539](https://github.com/gastownhall/beads/issues/7539) | Queries preserve Unicode values, combining marks, byte offsets and whole-rune backup; NUL cannot truncate input and numeric tokens use ASCII digits. Quoted invalid bytes remain unchanged; invalid bare bytes fail explicitly. | Harry Miller’s [#7544](https://github.com/gastownhall/beads/pull/7544) at 2e3a5f370235c67f6e8c57a6e8ec74525674e65d, with all seven contributor cases retained and added whitespace, escape, CJK and duration-boundary checks |
| [#7534](https://github.com/gastownhall/beads/issues/7534), [#7524](https://github.com/gastownhall/beads/issues/7524) | Purge confirmation retains the deletion limit and literal filters; prune retains its explicit reference override. Nonpositive days are rejected before multiplication can overflow. Preview and storage safeguards remain. | Rongjun GENG’s [#7540](https://github.com/gastownhall/beads/pull/7540), based on aphexcx’s reports and patches; five selector cases retained, with shell argument round trips and real CLI retention/refusal checks added |
| [#7505](https://github.com/gastownhall/beads/issues/7505) | Advisory Git probes retain their ten-second deadline, bound retained pipes to two seconds and honor CLI cancellation. Prompt helpers are disabled; credential.helper remains supported. Probe failures stay UNKNOWN. | Vishnu Jayavel’s [#7511](https://github.com/gastownhall/beads/pull/7511) patch and two regressions, extended with root cancellation/deadline and CLI qualification |
| [#7496](https://github.com/gastownhall/beads/issues/7496), [#7498](https://github.com/gastownhall/beads/issues/7498), [#7499](https://github.com/gastownhall/beads/issues/7499) | Nix hash refresh retains failed builds, scopes pipefail and requires an independent workflow build. Restore original bytes/mode on errors, isolate backups/logs, verify the real file on no-op paths and preserve quoted BSD/Docker arguments. | Rongjun GENG’s [#7504](https://github.com/gastownhall/beads/pull/7504) at ecc502261e3fb5f8e779945872c47d1ec0a16411, with his Dolt-floor regression retained and added shell/rollback fixtures |
| [#7515](https://github.com/gastownhall/beads/issues/7515) | AI duplicates and issue compaction select the first text block after optional thinking blocks. Truncated replies are rejected; missing/blank summaries cannot compact an issue. Mechanical fallback and transport retries remain. | Harry Miller’s [#7522](https://github.com/gastownhall/beads/pull/7522) loops and stub regressions, extended with empty/truncated response qualification |
| [#7508](https://github.com/gastownhall/beads/issues/7508) | Markdown dependency aliases become blocking edges; explicit `blocks:` reverses direction like `--deps`. Valid custom types remain supported; invalid batches leave no partial issues. Existing edges are not rewritten. | Vishnu Jayavel’s [#7512](https://github.com/gastownhall/beads/pull/7512) parser patch and tests, with fork CLI persistence/readiness qualification |
| [#7502](https://github.com/gastownhall/beads/pull/7502) | History compact rejects stale plans and pending tracked changes. Embedded CLI uses existing exclusive coordination; server/proxy publication refuses because head checks alone leave a reset race. Dry-run and semantic issue compaction remain available; retained authors/dates and ignored planes are preserved. | uschtwill’s head guards and six contributor scenarios, adapted following bee-ghosttrack’s macOS socket review; fork adds backend refusal and existing-gate exclusion |
| [#7451](https://github.com/gastownhall/beads/issues/7451) | External prerequisites constrain ready, blocked, counts and atomic ready-claim before limits, including active issue/wisp descendants. Closed provides capabilities release consumers; missing/offline/moved providers stay blocked. Manual update-claim is unchanged. | Fork correction following chrisnestrud’s external-block reproduction; real embedded CLI and SQL-server selection probes |
| [#7445](https://github.com/gastownhall/beads/issues/7445) | Typed permanent diagnostic for filesystem-denied embedded opens, with stderr JSON code and retryable=false; permission causes and hook categories survive. Logical readonly still requires driver filesystem access. | Fork implementation of dan-1492’s clear-diagnostic alternative; effective macOS deny-write reproduction, without claiming native Windows or a write-free engine |
| [#7289](https://github.com/gastownhall/beads/issues/7289) | Retried single/batch creates regenerate IDs from the new snapshot and preserve concurrent writers. Explicit import IDs retain upsert semantics. | Fork fix; reporter's collision probe extended to tasks, wisps and batches |
| [#7037](https://github.com/gastownhall/beads/issues/7037) | Forward migrations 0067 and ignored/0027 repair false blockers using separate target joins, without modifying shipped migration bytes or user timestamps. | Fork repair following the reporter's OR-free query design |
| [#5963](https://github.com/gastownhall/beads/issues/5963) | Recall/Forget and CLI/HTTP recognize imported empty memory rows as present. New empty content remains refused. Selected-prefix reads and atomic batches remain supported. | [#5964](https://github.com/gastownhall/beads/pull/5964), adapted to preserve bounded UOW selection |
| [#7091](https://github.com/gastownhall/beads/issues/7091) | Empty-parent updates for dotted IDs are refused atomically rather than claiming a detach that legacy readers undo. Explicit nonempty reparenting and nondotted detachment remain supported. | Fork compatibility guard; full dotted-ID detachment is not introduced |
| [#7300](https://github.com/gastownhall/beads/issues/7300) | Ready readers and counts hide children of an indefinitely deferred parent. `IncludeDeferred` and unrelated relationships retain their behavior. | [#7304](https://github.com/gastownhall/beads/pull/7304), adapted to the baseline storage API |
| [#7275](https://github.com/gastownhall/beads/issues/7275) | Long or dotted lowercase read-shaped memory keys cannot overwrite a neighboring memory. Explicit keyed writes keep their semantics. | [#7278](https://github.com/gastownhall/beads/pull/7278) |
| [#7214](https://github.com/gastownhall/beads/issues/7214) | Failed process inspection preserves a live Dolt pid/port; EPERM means alive; stop does not signal an unverifiable or recycled PID. | [#7229](https://github.com/gastownhall/beads/pull/7229) |
| [#7098](https://github.com/gastownhall/beads/issues/7098), [#5972](https://github.com/gastownhall/beads/issues/5972) | Local restore rejects empty/non-backup directories before calling Dolt. Nonempty regular manifest required; full content integrity and atomic restore remain outside this preflight. | [#7192](https://github.com/gastownhall/beads/pull/7192) |
| [#7384](https://github.com/gastownhall/beads/issues/7384), [#7385](https://github.com/gastownhall/beads/issues/7385), [#7387](https://github.com/gastownhall/beads/issues/7387), [#7397](https://github.com/gastownhall/beads/issues/7397) | Audit examples use real event values; fresh-clone sync uses `init --remote`, existing data is preserved, obsolete `--no-db` guidance is removed and release notes describe proxied mode as explicit. | Rongjun GENG (rjgeng), [#7393](https://github.com/gastownhall/beads/pull/7393), adapted with existing-workspace guidance and the release-note correction |
| [#7277](https://github.com/gastownhall/beads/issues/7277) | Opt-in telemetry resource uses explicit PID, executable and runtime detectors without automatic argv or owner collection. Long issue text no longer enlarges every metric resource; environment overrides and local credential-scrubbed command spans remain separate. | Julian Knutsen’s [#7279](https://github.com/gastownhall/beads/pull/7279), retaining and extending its resource regression |
| [#7283](https://github.com/gastownhall/beads/issues/7283) | Direct shared-server migration counts work performed during store open once; its explicit verb skips the auxiliary version-bump open, preserving CLI applied status, write tracking and workspace version stamping. Existing consent and readonly gates remain. | kenny-ish’s [#7380](https://github.com/gastownhall/beads/pull/7380), preserving its regression; real Dolt 2.2.0 qualification |
| [#7295](https://github.com/gastownhall/beads/issues/7295) | Long-lived SQL providers resolve the authenticated proxy on every new physical connection after idle retirement, preserving selected database, credentials/TLS and pool limits. No statement/transaction replay; canceled discovery stops waits and cleans up only its own spawned child. Schema bootstrap stays pinned. | Fork correction following bee-ghosttrack’s reproduction; managed and external provider regressions share a retained-data/recycled-port probe |
| [#7294](https://github.com/gastownhall/beads/issues/7294) | History compaction preserves the boundary date and retained committer/date, author/date, message and empty commits; caller session overrides are restored on success/error. Tracker markers in the retained window still resolve without edits. Discarded history and already re-dated stores are not reconstructed. | uschtwill’s [#7296](https://github.com/gastownhall/beads/pull/7296), adapted following bee-ghosttrack’s committer-variable review; contributor regressions retained with real embedded history coverage |
| [#7431](https://github.com/gastownhall/beads/issues/7431) | Copilot setup manages a marked section in shared instructions: install/check/remove preserve outside bytes and permissions, migrate exact legacy templates and refuse ambiguous markers or non-regular/link destinations. The manifest remains Beads-owned. | raykao’s report; Yi-111-a’s [#7433](https://github.com/gastownhall/beads/pull/7433) implementation and tests, adapted with stricter ranges and mode preservation |
| [#7370](https://github.com/gastownhall/beads/issues/7370) | Opt-in `gates.human.resolvers` requires an explicit listed actor for human-gate closure, status/type/defer/persistence changes, deletion and protected edge removal/refresh. Forced completion of directly gated tasks is covered; batches retain their atomic/best-effort contracts. Missing policy preserves compatibility; actor names remain caller-asserted. | travisbgreen’s resolver-list proposal, enforced in shared transaction seams without executable veto hooks |
| [#7371](https://github.com/gastownhall/beads/issues/7371) | Enabled journal stores actor resolution provenance without renaming actors: CLI flag/env/config/git/user/unknown; direct named callers provided. Historical and actorless sources remain empty; CLI/HTTP/SSE share the optional field. No authentication or gate policy is added. | travisbgreen’s `actor_source` proposal; additive migration for both clone-local schema doors |
| [#7392](https://github.com/gastownhall/beads/issues/7392) | macOS managed startup proves listener ownership before publishing state. Foreign listeners trigger ephemeral-port retry; unknown/partial inspection fails within the readiness deadline. Cleanup reaps only the launched child/group; warning-level logs and Stop protections are preserved. Other platforms keep greeting-based readiness. | bee-ghosttrack, merged [#7257](https://github.com/gastownhall/beads/pull/7257), adapted to this baseline; strict unknown policy follows donnabox’s [draft #121](https://github.com/versioned-beads/beads/pull/121), with mixed-holder rejection and bounded inspectors |
| [#7404](https://github.com/gastownhall/beads/issues/7404) | Jira pull renders nested lists, tables, panels, blockquotes, headings, code and supported inline marks as Markdown. Bounded conversion retains original JSON on malformed/over-limit ADF; v2 strings remain supported. | Fork implementation following yoyomeng2’s reproduction and recursive-walker proposal; both import callers covered |
| [#7395](https://github.com/gastownhall/beads/issues/7395) | Python MCP binds creation titles to `--title`, preserving option-shaped content and the configured actor. | Rongjun GENG (rjgeng), [#7396](https://github.com/gastownhall/beads/pull/7396); contributor regression retained with real CLI round trips |
| [#6873](https://github.com/gastownhall/beads/issues/6873) | Python MCP forwards label replacement and explicit clearing without changing omitted-label behavior. | [#6877](https://github.com/gastownhall/beads/pull/6877) |
| [#7095](https://github.com/gastownhall/beads/issues/7095) | Codex hooks prime the payload workspace and scope Git authority there, including protection against selectors inherited from caller startup. | [#7119](https://github.com/gastownhall/beads/pull/7119), extended after its blocking review |
| [#7334](https://github.com/gastownhall/beads/issues/7334) | Proxy fixtures that do not dial a chosen port bind port zero directly, preserving backend-error counters and explicit occupied-port refusal. | Reporter’s port-zero proposal, verified with a forced port collision |
| [#7349](https://github.com/gastownhall/beads/issues/7349) | Worktree fixture commits and shared runners suppress detached Git maintenance with command-line options, including callers that scrub Git environment configuration. | Reporter’s audit proposal; real Git trace regression |
| [#7327](https://github.com/gastownhall/beads/issues/7327) | Dolt server fixtures disable asynchronous event flushing before creating a temporary HOME, preventing detached metrics processes from racing configuration cleanup. Backend shutdown waiting remains unchanged. | Fork investigation with Dolt 2.2.0; the reported cleanup race reproduced locally |

These adaptations do not merge, approve or reopen the upstream pull requests.

The 21 October follow-up adapts julianknutsen's PRs [#7738](https://github.com/gastownhall/beads/pull/7738), [#7751](https://github.com/gastownhall/beads/pull/7751) and [#7752](https://github.com/gastownhall/beads/pull/7752): doctor discovery preserves metadata, personal migration binds the exact destination and commits it before source deletion, and external-endpoint diagnostics avoid starting an empty shadow database. The migration retains collision/dirty-target refusal and commit-uncertainty protection. Native embedded, server and proxied mode boundaries remain explicit.

The Perl timeout fallback supervises Go children through a bounded TERM/KILL sequence ([#7753](https://github.com/gastownhall/beads/issues/7753)). The owned server-log portion of gascity-eddie's [#7735](https://github.com/gastownhall/beads/pull/7735) rotates before opening the child descriptor; custom log paths remain untouched. The stronger existing runtime proxy-log bound stays in place.

PRs [#7742](https://github.com/gastownhall/beads/pull/7742), [#7750](https://github.com/gastownhall/beads/pull/7750) and [#7743](https://github.com/gastownhall/beads/pull/7743) target a later ordinary-native history activation path absent from this v1.3.1-based fork. They are not imported as feature activation. Existing explicit Graph Preview history retains its independent lifecycle and recorder contracts.

The GitLab gate extension follows vishnujayvel's [#7432](https://github.com/gastownhall/beads/issues/7432)
requirements. Their public synchronization branch does not implement these gate
checks; the fork reuses its existing gate evaluation/closure path and adds a
separate optional glab reader, without importing the unrelated sync redesign.

- Project `.env` imports only documented selector/passive connection keys. Operator
  values, including explicitly empty selectors, win during early and full loading.
  Executable overrides and credential commands remain operator-controlled.
- Database migration validates identifiers and real child directories before
  changing paths. Corrupt authoritative metadata is refused without replacement;
  recovery requires an explicit known-valid project backup.
- Terminal issue rendering removes controls before styling; default DAG titles
  stay on one row. Storage, JSON and exports retain original values.
- Established event streams recheck authorization before delivery and around
  journal reads. Successful revocation closes the stream; failed file reloads
  retain the last-good token set within the documented deadline limits.
- Python MCP comments/notes preserve literal positional text and configured actor
  identity. Option-shaped text cannot become another CLI option.
- Graph Preview accepts the existing `--sandbox` flag on supported commands, retaining readonly guards and skipping native sync/push housekeeping.
- Graph Preview rejects unreadable native/continuity writes atomically, bounds
  SQL acquisition of keyed projections and allows gradual reduction of old
  oversized state while retaining history. Complete snapshots reuse one binding
  validation per transaction. Ordinary storage and existing projects retain their format.
- Root/example builds require Go >=1.27.3 and x/net >=0.60.0, alongside the reviewed gRPC and cryptography minima;
  Python package requirements retain PyJWT >=2.15.0 and dev urllib3 >=2.8.0.

An earlier verified local source cut passed the canonical Go baseline (102 packages),
targeted regressions, native/Windows cross-lint, documentation checks and real
Python-client round trips. Embedded Dolt has separate complete batch inventories.
Focused local-server tests reproduce concurrent data loss and migration false
blockers and verify their repairs; embedded and UOW tests verify memory presence
and parent-update parity. These results do not qualify all server/Docker paths,
native Windows, production or every
native compact cycle, and do not claim all advisory inventory warnings vanished.
The fixture-only repairs for #7334, #7349 and #7327 do not change the shipped
CLI or operator configuration. Dolt cleanup was reproduced without
`DOLT_ROOT_PATH`: one failure in 80 cycles before the repair, then 200 cycles
with the race detector and no failures or skips. This is local macOS arm64
evidence, not a rerun of upstream Bazel or macOS CI.
Private reproduction reports, installation evidence and backups stay local.

## Remaining limits

- Jira ADF pull preserves supported Markdown structure, not all ADF metadata or media. Unsupported containers keep descendant text; invalid/over-limit documents stay as original JSON. Push continues to emit plain paragraphs, so rich-text pull/push round trips are not qualified. No live Jira service was used for this correction.
- Autonomous semantic extraction, embedding/vector retrieval and whole-language
  type analysis are not implemented. Codex supplies and reviews semantic content.
- Journal capture is partial and redaction is best effort. Reported summaries and
  observed exit codes do not prove a repair or successful deployment.
- The live viewer and maintenance require direct embedded workspaces; they do
  not aggregate unrelated projects or provide a writable web interface.
- Unreferenced derived blobs can be pruned explicitly on upgraded direct embedded
  writers. Dolt history is retained; live payload savings do not establish disk savings.
  Server/proxied prune application and mixed older writers are not qualified.
- Compact dispatch, cold-output fallback and bounded diagnostics have handler/process
  coverage. Native context-limit-triggered midturn recovery must be qualified for
  each deployed source/binary cut; historical positive cycles do not establish
  universal reliability or explain previously missing deliveries.

## Batch hydration

Batch hydration (#7712) snapshots parent ancestry once after the accepted parent phase for multi-blocking-edge creates. Existing target, cycle, skip and transaction policies remain; this does not introduce direct table copying or claim a complete set-based importer.

## Development

Keep `main` available for upstream synchronization and scope fork work to
`codex/` branches. Use Beads for durable development tasks, the existing storage
interfaces for persistence, and [engdocs/TESTING.md](engdocs/TESTING.md) for
verification. Follow the DOX hierarchy before edits. Preserve the bundled D3,
TypeScript and Tree-sitter licenses/notices; `bd code --licenses` exposes parser attribution.

## User-level client setup

Adapted @julianknutsen's Beads PRs #7696/#7740/#7741 (Claude), #7739 (Gemini) and #7749 (Mux). Global instruction sections use stable repository-conditional wording and omit project scaffolding. Claude distinguishes user/project/local plugin scope; Gemini keeps legacy saved memories outside its managed section; Mux resolves home and global-directory aliases consistently.

The fork retains its conservative authority profile, outside whitespace, shared Copilot section guards and separate Graph Preview Stop setup. Contributor regressions retain their scope; Gemini/Mux whitespace expectations follow the fork's existing byte-preservation contract. Installation updates the CLI without running setup against real client settings or changing Codex hooks/trust. The engine, schemas and project formats are unchanged.

## Merge publication, tracker baselines and native graph reads

- [#7757](https://github.com/gastownhall/beads/issues/7757): embedded conflict settlement also stages cleanly merged companion tables before publishing the resolution commit. Preserve unrelated tables that were dirty before the merge. Two-clone local-remote reproductions check comments and issue fields; native regressions inspect HEAD, STAGED and WORKING for comments/labels plus unrelated dirty data.
- Adapt owpac's [PR #7755](https://github.com/gastownhall/beads/pull/7755) at c4393e018cff72efbc13ac65f0d49684eda56fcb: a successful pull records the imported representation as the push hash, including creates with a locally assigned ID. Retain the contributor's regression and add dry-run and failure controls. Issue writes and hash bookkeeping remain separate; this is not arbitrary-crash atomicity.
- Native project graphs optionally read memories/current code generation, issues and dependencies in one readonly transaction. This avoids repeated engine/journal-index loading while retaining the existing per-query release and 30-second deadline. Filesystem validation and JSON projection run after the transaction. Graph Preview uses its existing continuity snapshot; other backends retain their prior path. Isolated measurements are workload-specific, and live qualification is recorded privately.
- The embedded Dolt pin additionally carries [#12165](https://github.com/dolthub/dolt/issues/12165), [#12166](https://github.com/dolthub/dolt/issues/12166) and [#12169](https://github.com/dolthub/dolt/issues/12169): absent keyless conflict rows no longer reach secondary key builders, added nullable columns merge against the existing ancestor row, and Full-Text rebuilds use the existing Prolly primary map. Retain genuine conflict controls, exact root comparisons and native backup qualification. Full-Text rebuilding remains complete rather than incremental; no schema, driver or standalone-server policy changes.

Native activity capture also uses one existing current-schema memory transaction for enablement, retry checking, saving and version publication. This removes repeated embedded engine opens per hook while retaining record identities, redaction, hook configuration and refusal behavior. It adds no queue, database, migration or service.

## Context acquisition without retained AST transfers

Native `prime` reads the non-code memory plane and the existing code-summary manifest in one readonly memory snapshot. Retained code generations stay stored and human memories, reviewed knowledge, observed activity, source checks, output limits and required-read failure withholding are preserved. Missing manifests and adapters without snapshot support retain the legacy read. The summary remains advisory; full index validation belongs to code queries. Activity diagnostics select the literal activity namespace before transferring values. No hook trust, timeout, schema, pruning or project execution changes are implied.

## Full-Text filter qualification

Ordinary equality and IS NULL predicates on a Full-Text column reproduced analyzer errors with the official Dolt 2.5.1 engine/analyzer pair and the previous fork tuple. The pinned analyzer now costs a Full-Text lookup only for a MATCH bound to that index. Other AND predicates remain residual, ordinary indexes cannot consume MATCH, and incomplete OR lookups fall back. Four keyed/keyless fixtures with optional same-column ordinary indexes exercise twenty predicates in normal and prepared mode in both the analyzer and native Dolt engines. Existing view, merge, Full-Text rebuild, NULL and duplicate-preservation controls remain. This does not imply a general MySQL-compatibility claim.

### Prioritized upstream adaptations (2026-10-22)

The fork carries the verified behavior from Beads #7763 (existing file permission preservation), #7771 (metadata-less native target adoption), #7766 (passive doctor scanning), #7776/#7777 and #3445 (optional wisp planes and distinct dependencies), #7774 (Claude plugin precedence), #7762 (Mux link preservation) and #7767 (remote endpoint resolution). Contributor attribution and reviewed heads are recorded in the corresponding upstream comments. These are adapted changes rather than a blanket merge of the newer upstream tree.

Shared native discovery refuses Graph/future formats and corrupt nested metadata without migrations. Routed adoption reuses personal-migration prefix and dirty-state checks before metadata publication. Dependency counts use SQL from #7776; server streaming uses the single-read-transaction joins from #7777. Keep native Graph knowledge, retained Issue history, code indexes, supervised learning, separate DOX guidance and operator lifecycle customizations. Graph Claude setup retains its conservative provider refusal and separate Stop reminder, while ordinary setup follows effective plugin precedence. No hook/trust configuration is changed by installing the binary.

Qualification includes focused red/green regressions, native embedded plane-state matrices, full setup/doctor packages, the required complete Go and lint lanes, the updated Dolt tuple and isolated backup/history readbacks. Remote service tests distinguish synthetic listeners from a live external deployment; upstream issue closure remains a maintainer decision.

Credit: @julianknutsen (#7762/#7763/#7766/#7767/#7771/#7774); #7776/#7777 provide overlapping dependency-reader proposals, combined without duplicating their implementations. That batch used BlueboxC/dolt `cdcab16c659e11048a481356b52e28b15824695f`; the current engine pin is listed above, with the analyzer and driver pinned separately.

Reviewed proposal heads: #7763 `bae4b94ae47c` (@julianknutsen), #7771 `71ed342c5c14` (@julianknutsen), #7766 `20266f6db11f` (@julianknutsen), #7774 `8e4cdc032448` (@julianknutsen), #7762 `142b334281e0` (@julianknutsen), #7767 `02323e128482` (@julianknutsen), #7776 `51c79cf1ccb5` (@gascity-eddie[bot]), #7777 `ce45fb53c6de` (@gascity-eddie[bot]).

## Formula, discovery and installer follow-ups

- [#7780](https://github.com/gastownhall/beads/issues/7780): a redirected npm download closes its old stream and reopens the destination without asynchronously unlinking it. Real-file redirection/extraction tests reproduce the prior deletion race; HTTP error rejection remains unchanged.
- Adapt [PR #7760](https://github.com/gastownhall/beads/pull/7760), head 663960e8bff4d89f2ddae059bf048be6c4adf4c1, from gascity-eddie[bot], for #3316. Gate-typed steps are preparation tasks followed by an exit gate; ordinary steps retain entry gates. Dependents wait for both the preparation and companion gate. Retain contributor tests, typed gate fields and fan-in semantics; the persisted-edge test additionally runs on real embedded Dolt.
- Adapt Julian Knutsen's [PR #7765](https://github.com/gastownhall/beads/pull/7765), head a2126f0e38a360bc42fc5d2443c64735d8fa4449. Passive multi-repo discovery checks the literal backend before any migrating constructor, preserves legacy/foreign metadata and retains YAML custom types while explaining database-read refusals.
- Adapt Julian Knutsen's [PR #7770](https://github.com/gastownhall/beads/pull/7770), head f696d24c1df126c725c3fedd4e173dfeb13c2adb. Personal migration refusals omit usage dumps, distinguish shared-root rigs from self-migration and identify clones. Dirty unnamed databases receive a selector-first commit remedy; uncertain commits retain both copies and report their issue IDs. Reuse the fork's native-only destination admission and adoption helper.
- Adapt the lock classification and private-probe portions of [PR #7778](https://github.com/gastownhall/beads/pull/7778), head ece3e1009d5c22857f146b2f6313371ddb007885, from gascity-eddie[bot]. Unsupported locking is distinct from actual contention. This fork continues to refuse both shared and exclusive operations when a required lock is unavailable; readonly and existing-only gates never create probe files. No queued-writer policy or ungated fallback is imported.
- The engine pin additionally carries angelamayxie's [Dolt PR #12171](https://github.com/dolthub/dolt/pull/12171) NULL-safe mask into keyless secondary readers. The existing #12160 KV mapper fix remains; actual reader-map tests qualify this complementary boundary. Driver, analyzer, schema, hooks/trust, project activation and standalone CLI/server policy are unchanged.

## Journal, YAML allocation and MCP query metadata

- Adapt Dolt PR #12176 by reltuk: file-backed journal reads and callbacks run outside the journal lock, with 16 bounded workers rather than a complete copied batch. Close drains registered readers and callbacks; a read after closure fails explicitly. Contributor regressions and cancellation/retention controls are retained. The embedded driver and SQL-analyzer revisions are unchanged.
- Follow up #7684: collateral YAML validation shares exact interned path components before/after instead of formatting each ancestor path for each leaf. Keep the depth guard, alias identities, dotted/nested keys, document boundaries and refusal of collateral changes. The bounded allocation regression measures a workload, not a fixed process-RAM guarantee.
- Adapt PR #7779 by victoraranda: eight MCP discovery/query tools advertise advisory readOnlyHint metadata. Writers and mixed context/admin tools remain unannotated. Retain contributor scenarios using the locked SDK's protocol field, and document the addition in the package changelog. Commands, timeouts, dependencies and CLI/hooks installation mode remain unchanged.


### Working-set visibility (#7783)

Native `bd vc status` now displays staged and unstaged table changes through the
existing storage role, retaining branch/commit JSON fields and adding both lists.
A failed read does not report a clean working set. Existing LWW supersession
notices remain; a later claim can still win a contested close group under the
documented merge policy. This fixes the hidden-change report, not a new
close-wins merge policy or Graph Preview synchronization. Based on andrest's
report in gastownhall/beads #7783.


### Qualified engine follow-up (October 23, 2026)

The pinned dependency includes Dolt #12177 (content-stable archive dictionaries
and on-demand compression dictionaries), #12186 (keyless conflict hashes use
the source version's schema), #12178 (non-unique commit-parent lookup), #12179
(small journals grow buffers and indexes on demand) and the log component of
macneale4's PR #12182 (exclude all common bases). Existing virtual-base diffs,
Full-Text, NULL, history and backup contracts remain. Driver 2.4.1, the matched
SQL analyzer, project formats and external CLI/server policy stay unchanged.
The engine fork documents the regression scope and limits for each correction.

## Repository-scoped tracker matching

Adapt NahumLitvin's [PR #7794](https://github.com/gastownhall/beads/pull/7794) for [#7781](https://github.com/gastownhall/beads/issues/7781), preserving its contributor scenarios. Initialized GitHub fallback matching requires the configured host and owner/repository; API URLs, GitHub Enterprise subpaths, case-insensitive names and repository-local shorthand remain supported. This shipping baseline does not include upstream's relationship resolver, so its scope check is implemented directly in the existing tracker. Old owner/name URLs after a transfer or rename no longer match by number alone.

For [#7795](https://github.com/gastownhall/beads/issues/7795), apply the same separation to GitLab issues, work items and milestones, preserving nested namespaces and self-hosted base paths. Numeric project IDs resolve `path_with_namespace` through one project-metadata read at initialization; an unavailable or incomplete response refuses synchronization. Group pulls can still match other projects by exact URL; project-specific push and IID fallback do not guess another project's identity. No new storage, schema, credentials or activation is introduced.

## Explicit workspace selection

For [#7782](https://github.com/gastownhall/beads/issues/7782), adapt Rongjun GENG's [PR #7792](https://github.com/gastownhall/beads/pull/7792). A linked worktree selects its main checkout's store before any unrelated ancestor, with or without inherited metadata. A conflicting caller `BEADS_DIR` now refuses before rebinding instead of silently choosing a different project. Matching canonical/relative paths and redirects retain their behavior.

## Measured history-compaction reporting

Adapt the actual-count portion of csauer02-personal-user’s [PR #7790](https://github.com/gastownhall/beads/pull/7790). `commits_after` is read from the resulting history, including its initial commit. A failed readback omits the field and warns. Keep this fork’s exclusive embedded lease, clean-set and head guards, and server/proxy rewrite refusal; the stacked online compaction changes are not imported.

### SQL and journal follow-up (October 23, 2026)

The pinned tuple carries GMS #4056’s JOIN predicate preservation, #4047’s distinct GREATEST/LEAST comparison/result types, Dolt #12191’s execution-time AS OF binding, and PR #12194’s release of grown idle journal buffers. Preserve exact decimals, the shared COALESCE helper, selected-revision and NULL errors, fixed journal limits and reader/Close coordination. Repeated large flushes allocate again to reduce idle heap; no general RSS or throughput guarantee is inferred. Driver, schema, project formats, hooks and personalized capacities stay unchanged. Contributor attribution and precise regression limits remain in the dependency forks.

## Conservative Classic Artifacts cleanup

Adapt julianknutsen's [PR #7797](https://github.com/gastownhall/beads/pull/7797), retaining its nested-backend scan/cleanup regressions. Both paths now share positive Dolt selection: an explicit Dolt backend or an existing Dolt repository. Unlike negating the historical-SQLite classifier, ambiguous metadata-less layouts remain untouched even with extra files or symlinks. Existing Graph Preview/future-format guards and migration-free discovery remain. No storage engine, schema, project selection or automatic cleanup policy changes.

## Refusal before legacy metadata migration

Adapt julianknutsen's [PR #7798](https://github.com/gastownhall/beads/pull/7798) to the fork's three public SDK opens and three Dolt constructors. Non-migrating native discovery rejects unsupported formats/backends before legacy config.json migration or gate creation; the loaded selector is rechecked afterward. Remedies name the actual metadata file and failed OpenFromConfig returns an untyped nil. Keep OpenBestAvailable's registered-extension dispatch, Graph Preview's explicit route, and supported legacy migration. Contributor table scenarios retain bytes, modes, unknown fields and the surrounding directory tree.

### Portable remedy values (#7799)

Adapt julianknutsen's [PR #7799](https://github.com/gastownhall/beads/pull/7799): quote portable mirror-removal/addition arguments and use placeholders plus reversible escaped names for values with incompatible shell or terminal interpretation. The fork's routed-target admission already refuses remote targets without emitting a host command; retain that boundary and escape its displayed path. Preserve the contributor's shell/value tables. Windows behavior is table-checked here; no Windows runtime is claimed.

### Preserve setup-directory ownership (#7806)

Adapt julianknutsen's [PR #7806](https://github.com/gastownhall/beads/pull/7806): integration removal uses the existing Lstat-based real-directory guard through a shared helper and attempts cleanup only after removing an owned file. Keep linked roots, unrelated files, empty directories this run did not empty and existing shared-client hooks. Contributor recipe and client regression scenarios are retained. Windows junction behavior needs native Windows qualification.

### Bootstrap endpoint consistency (#7804)

Adapt julianknutsen's [PR #7804](https://github.com/gastownhall/beads/pull/7804): probe, clone, finalization and warmup use the target workspace's configuration and the same host/socket/port/credentials. Reuse the store's init-port resolution. Restore the previous process configuration after each scoped read; concurrent configuration readers remain unsupported. The fork expresses a missing established-project database through its existing blocked-none plan and rechecks before initialization. Preserve explicit init overrides, shared-server ownership, native/Graph routes and credentials precedence. Contributor endpoint, user-layer and enclosing-workspace scenarios are retained with these API adaptations.

### IPv6 endpoint consistency (#7803)

Adapt julianknutsen's [PR #7803](https://github.com/gastownhall/beads/pull/7803) across the fork's existing server, proxy, credential and diagnostic paths. Shared address construction accepts bare or bracketed IPv6; TLS server names stay bare. IPv6 loopback never starts/stops the IPv4-only managed server, adopts its data directory or persists its port file. Preserve explicit init settings, sockets, TLS and IPv4/hostname behavior. New upstream HTTP-client APIs absent from this fork are not introduced; contributor address, TLS, credential and lifecycle cases are adapted to existing interfaces.

### Consistent Claude detection (#7802)

Adapt julianknutsen's [PR #7802](https://github.com/gastownhall/beads/pull/7802): doctor and setup tips reuse setup's exact plugin identity, per-ID settings precedence and loaded hook paths. Explicit path inspection resolves that repository without replacing the current Git cache. Preserve managed/local/project/user layers, personal disablement and shared-hook ownership. This changes detection only; it neither installs Claude nor changes Codex hook definitions or trust. Contributor detection and path-aware cache regression scenarios are retained.

### UTC virtual-base evaluation

The embedded engine additionally adapts macneale4’s [Dolt PR #12198](https://github.com/dolthub/dolt/pull/12198): virtual time defaults evaluate in UTC and restore the caller session setting. The driver/analyzer pins and schema stay unchanged. Retain the fork's conflicting-base refusal, virtual-object retention and matched data/binary recovery.

## Scoped dependency maintenance

Adapt Julian Knutsen's [PR #7814](https://github.com/gastownhall/beads/pull/7814), [PR #7813](https://github.com/gastownhall/beads/pull/7813) and [PR #7812](https://github.com/gastownhall/beads/pull/7812): MCP build jobs use uv 0.13.5, the Python runtime requires opentelemetry-api >=1.45.1, and the OpenAPI generator uses kin-openapi 0.149.0 with jsonschema 6.0.3. Retain FastMCP 3.4.5, the existing CLI/hooks integration and all Dolt/analyzer/driver pins. The Nix Go1.27.3 overlay retains the pinned source, uses Go1.26 to bootstrap, and adapts the pinned vendor opt-in patch to the renamed loader state. Recompute the fixed-output dependency hash and independently build after lock changes.

The three-clone [#7811](https://github.com/gastownhall/beads/issues/7811) label-loss reproduction is already covered by this fork's companion-table settlement correction for [#7757](https://github.com/gastownhall/beads/issues/7757); do not add a second resolution workaround. Qualify the actual binary and report stock-version differences separately.

### Windows Make toolchain reliability

Adapt Julian Knutsen's [PR #7820](https://github.com/gastownhall/beads/pull/7820) while retaining the fork's three-host Windows matrix and Make exercise bodies. Image tools and verified restored packages precede bounded online fallbacks; the main-only saver uses matching content/image/month keys. Retain native checksums, MSYS2 package signatures and the signed Cygwin index/installer checks. Both cached and online native executables must match the pinned SHA-256 before publication, agreeing with the latest upstream follow-up. Native Windows file controls cover that refusal and invalid Cygwin cache layouts without executing the invalid executable fixture.

MSYS2 downloads targets even when the image already has them installed and requires every resolved package file to exist and be nonempty before installation or saving. This prevents an empty seed from being reported as an exact cache hit.

### Pinned CI action maintenance

Adapt upstream #7815 across action families already used by this fork, with tag-to-SHA verification and correct version comments. Preserve workflow permissions, guards, inputs and the Windows host matrix. `github-script` is absent and is not introduced. The updated JavaScript actions run on Node 24; release/deployment credentials and publication remain subject to their existing gates.

### Go module maintenance (#7808)

Adopt Anthropic SDK1.83.0 and the reviewed x/mod0.42.0, x/net0.61.0, x/crypto0.58.0, x/text0.43.0 and x/tools0.51.0 graph, including their required x/sync, x/sys, x/term and x/telemetry versions. Mirror the effective graph into both independent examples. Retain Go1.27.3, the custom Dolt/analyzer replacements, driver2.4.1 and the existing telemetry policy. Qualify SDK response/retry behavior with local HTTP fixtures, preserve generated API bytes and compute the Nix vendor hash from these exact locks. This is dependency maintenance, without schema or project-format migration.

### SQL execution follow-ups (October 24, 2026)

The tuple incorporates GMS PRs #4062, #4058 and #4059 for Dolt #12205: LATERAL scopes retain legal ON predicates; EXIT handlers execute their entire body; and session select limits no longer truncate the input to DISTINCT/window expressions. The handler loop additionally preserves its instruction counter through IF/WHILE branches, with true/false and tail-operation regressions. Existing exact-decimal, Full-Text, storage and merge protections remain. Embedded normal/prepared qualification covers 22 cases, supplemented by the contributor engine suites and the Beads regression suite. Multiple procedure result sets (Dolt #12199) remain outside this correction. Engine upgrades retain matched data/binary recovery and native backup checks; the driver, schemas, capacities, hooks and external CLI/server policy are unchanged.

## Preview3 isolated preparation

This branch prepares integration of [donnabox/beads PR291](https://github.com/donnabox/beads/pull/291), pinned at `0c4dc1f0d68a371e56b3e461b968e828e4ea254c`. It adds pure Type catalogs/constraints, bounded relationship queries, identity helpers and ordinary-import decoding while preserving the existing native/schema8 writers and fork continuity features. It does not provide a schema8-to-schema11 migration, activate Preview3, replace project data, or qualify JSONL as history-preserving recovery. The preservation and rehearsal contract is [Preview3 integration](engdocs/design/preview3-integration.md).
