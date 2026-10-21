# DOX.md — Beads CLI

## Purpose

Own beads cli within the fork.

## Ownership

Commands, flags, help, output and CLI fixtures.

## Local Contracts

Root DOX.md and the parent chain remain binding.

- Root --version and -V remain store-free before graph metadata, marker or route admission. They do not provision, migrate or read a database, including incomplete/future/corrupt workspaces; data commands retain their admission guards.

- Recall, bare-key remember and forget distinguish missing rows from imported empty values. Presence follows the memory role result; empty rows can be recalled and forgotten.
- Bare lowercase memory keys may contain dots and exceed the automatic 50-character write-key truncation; recall-shaped misses remain read-only. Explicit `--key` writes retain their existing semantics.
- Proxied init persists the adopted project ID in metadata.json in both ordinary and team-server modes. Clone fingerprints remain computed from Git and version stays in .local_version; init never seeds or overwrites per-clone tracking rows in the shared database. Historical rows remain untouched.
- Local backup restore refuses missing/non-directory sources and missing, empty or non-regular manifests before the existing Dolt restore call. The embedded adapter additionally rehearses restore/commit in a disposable engine to reject incompatible storage before replacement. These preflights do not establish full integrity or atomic restore publication.
- Codex recovery uses the event's absolute existing cwd with the original operator environment, clearing inherited path/database selectors so caller startup cannot route the child prime to another project. An uninitialized event workspace emits no context. Missing cwd retains legacy caller behavior; invalid explicit cwd fails without emitting project context. Git observations and authority text name and apply only to the inspected workspace.

- Project `.env` loads only explicit selector and passive Dolt connection keys; executable settings come from the operator environment. Existing values, including empty values, win in both early selection and full loading. Absent selectors retain dotenv routing.
- Legacy embedded database migration validates identifiers and real child directories before any rename or metadata write.
- Malformed metadata.json blocks backend-selecting diagnostics, ordinary init and data commands. Preserve the selector and restore a known-valid project backup before retrying; never infer a fallback database from a parse failure. Store-free version remains available.
- Sanitize stored issue text before terminal measurement, styling or truncation. The default DAG also displays title newlines/tabs as spaces within one row; preserve original values in storage, JSON and exports.
- `knowledge` uses the existing memory role for document provenance and explicit assertions. Prime projects bounded knowledge alongside plain memories; the four context hooks remain read-only and use the same recovery path. Workspace-relative sources follow the selected Beads root, including nested invocations. Task, knowledge and code HTML graphs use the shared native viewer with typed colors, drag/zoom and readable node evidence. `code scan` explicitly updates derived code/markup/schema/configuration structure in the 23 documented languages/formats through the same memory role; query/status detect staleness. Prime/hooks only project a bounded snapshot summary and do not run parsers or refresh code. Knowledge symbol links require a current named symbol and bind its file/DOX.

- Native project graph uses the optional ProjectSnapshotter through the existing readonly store decorators, reading current-generation memories, tasks and dependencies within one engine acquisition. Filesystem validity and rendering follow release of the callback. Keep the 30-second viewer deadline and per-query cleanup; Graph Preview and unsupported adapters retain their existing routes.

- Native project graph and maintenance use the optional memory snapshot to select non-code memories and recall only the verified current index generation, all within one read transaction. This avoids retained-generation enumeration and per-blob engine reopening; retain the current manifest for guarded Save. Unsupported memory adapters keep the complete read fallback; Graph Preview keeps its canonical joined snapshot. Failed reads expose no partial results; no prune, GC, deadline, selection or human-evidence change.

- Project graph dependency reads use the storage bulk API, retaining issue order and all relation kinds. Tracker pull ID generation shares the existing YAML-first prefix policy and preserves preassigned IDs.
- `graph --project` composes a read-only overview of local tasks (all states), explicit knowledge and all indexed code directories. It preserves file/symbol locations, hashes, validity and typed aggregate syntax counts in the same offline snapshot. It never follows foreign workspace routes, scans code, renews assertions or infers task blockers. Direct workspace connection only; `--max-rows` applies to the complete task selection.

- `serve --graph-viewer` selects only the discovered local workspace and exposes the native live overview at `/viewer`. Skip persistent store initialization; each bounded strict-readonly `graph --project --json` query uses this executable, a pinned BEADS_DIR/database with empty path selectors that block dotenv rerouting and no shell, then closes its store. Reject --db, --database and --global; do not run a parser, renew assertions, execute project hooks or publish write APIs. Embedded Dolt is supported in this read-only mode while the normal API refusal remains. Stop with the normal server signals; no automatic startup installation.

- `maintain once|watch` is an explicitly started embedded-workspace extension. Its scoped factory open uses the existing driver and memory role, releases Dolt after each bounded child pass and performs one local derived commit only on changes; skip root import/template/hook/backup/export/push housekeeping. Refuse server/shared/proxied mode and database/global overrides. Reuse the latest saved roots/languages/exclusions; only directory roots discover new files. Missing roots remain selected for later branch/file return. Prepare all derivations before publication; derivation failures retain both previous projections, while each publication retains its last complete snapshot independently; back off to five minutes and stop on normal signals. Never write human assertions/tasks or start from prime/hooks/viewer/boot.

- Agent lifecycle hook subprocesses require a successful memory-plane read before emitting context. Ordinary prime retains advisory diagnostics; hook reads fail without emitting a diagnostic as context. Healthy empty planes, custom workflows and quoted diagnostic text remain valid. Codex consumes a pending session/workspace refresh only after writing nonempty complete context successfully; SessionStart satisfies it too. Failed reads or delivery retain the next-prompt refresh; a failed cold SessionStart queues it too. No storage-specific retry or lockfile repair.

- The four Codex context hooks record their latest local attempt per event/session/workspace plus bounded completed compact/failure attempts in the existing private marker cache. Start/completion, categorical prime/output results, typed failure stage/reason, sanitized commit build label, duration, source/trigger/turn UUID, directory-match flag and context byte count/hash support diagnosis without retaining raw text. Required prime reads carry only allowlisted categories across the child exit status; ordinary prime remains advisory. Untyped failures stay unavailable/unknown; elapsed time alone is not evidence of a storage lock or other engine cause. Build labels identify source cuts, not exact binary hashes. Atomic advisory cache writes never change hook output, pending-refresh behavior or the read-only memory plane. Local stdout success is not native context admission; missing files are not proof of omitted dispatch.

- Large document catalogs publish bounded content-addressed parts before a single manifest. Implicit scan and maintenance refuse a corrupt/incomplete saved catalog rather than defaulting to a new selection; explicit scan roots can replace it. Human evidence remains unchanged.

- `knowledge sources` reads bounded provenance without storing it. `knowledge propose` requires inspected hashes including DOX; `proposals` reports lifecycle/validity/history. `review` is the explicit accept/reject write with reviewer, reason and acceptance scope/evidence; it never rebinds or replaces existing assertions. All use the existing direct/proxied memory role; mutators enforce readonly. Native project/knowledge graphs include proposals and provenance/review edges separately from blockers.

- `knowledge prepare <sources> --session/--activity-event` returns an ephemeral readonly packet: latest 16 selected events with visible omissions, explicit current source/DOX text (64 KiB total, 16 KiB per file, tails reported), existing topic IDs and bounded recovery context. Its editable draft pins the workspace and exact origins/hashes; propose revalidates them and refuses a new draft for an established topic. Codex supplies semantic content, candidates stay pending and hooks/HTTP routes never submit them.
- `knowledge propose --activity-event <id>` explicitly pins up to 16 events from the selected workspace, preserving immutable IDs and observed session/turn provenance. Missing, corrupt or changed origins withhold new acceptance without replacing prior accepted assertions; events alone do not supply source/DOX hashes or verified scope.
- `graph --project` includes a bounded activity snapshot for the native Historial tab through the existing readonly query; no second database, transcript import, model or write API.
- `activity` keeps opt-in observed Codex operations and reported turn/session endings under @activity/ in the existing embedded memory plane. `codex-activity` handles PostToolUse, Stop and SessionEnd independently of the four context hooks. No initialization, raw tool payload/transcript storage, task changes, assertion promotion, remote sync or project execution. Configuration writes honor readonly; capture becomes a no-op under readonly or disabled/missing settings. Bounded recovery quotes up to three reported handoffs as untrusted data.

- code impact is readonly and reports conservative inverse file dependencies, witness paths, candidate tests and established learning/issue references. code relink emits review drafts without updating assertions. code prune defaults to a readonly plan; --apply enforces readonly guards, embedded mode and the optional atomic memory capability. Never enable cleanup from hooks/watchers; retain full Dolt history and refuse mixed older writers.
- SessionStart queues recovery before writing context; failure to write stdout retains next-prompt fallback, while marker-cache failure cannot prevent healthy context delivery. Diagnostics retain the latest event plus at most sixteen completed compact/failure attempts per session/workspace, containing metadata only; native admission remains a separate claim.

- The journal records actor resolution provenance (`flag`, `env`, `config`, `git`, `user`, `unknown`, or `provided`) without changing actor names or authorization. Bind context provenance to the exact actor; system/different-actor rows cannot inherit it. CLI recovery and stdin/TTY do not authenticate a human.

- Human gate resolution follows storage's optional caller-asserted resolver policy; keep CLI and proxied SQL actor provenance intact. Git/user/config defaults and force do not waive it. No new human authentication, executable hook or project activation is implied.

- Explicit `gl:pipeline`/`gl:mr` gates use bounded `glab api` GET subprocesses and its existing authenticated-host/repository discovery; no hostname substring guessing or token storage. Optional `metadata.repo` is a validated nested group/project path inherited by ad-hoc GitLab gates. Only pipeline success or MR merged resolve; failed/canceled pipelines and closed MRs escalate. Missing/invalid fields, mismatched IDs and provider failures remain unresolved. `gate discover --type=gl:pipeline` pins the newest exact current branch/HEAD match; foreign projects require an explicit ID and dry-run never writes. Existing human policy, close path and default GitHub discovery remain.

- `migrate schema` owns its migration and post-migration version reconciliation; skip the auxiliary version-bump open for this verb so opening another store cannot consume its applied count. Other commands retain automatic version reconciliation.

- CLI ready/blocked/count/explain and ready --claim resolve configured external:<project>:<capability> against closed providers carrying provides:<capability>. Provider opens are strict readonly, without auto-start/migration/hooks; use the shared five-second cooperative lookup deadline. Missing/offline/moved providers remain blocked. Explicit update --claim retains its existing manual-claim contract.

- Typed embedded permission failures emit actionable stderr, including JSON code embedded_open.permission_denied and retryable=false. Retain errors.Is permission semantics and hook categories; stdout stays empty on failed open. This diagnoses filesystem restrictions, not a write-free embedded engine.

- Admit graph-mode metadata before ordinary storage opening or maintenance. Route only supported Graph Preview commands; refuse unsupported flags, proxy selectors and incomplete/binding-mismatched workspaces. Opaque revision flags are graph-only on this baseline. Graph status reports canonical and continuity acquisition budgets; failed growth publishes no partial results. Admit knowledge/code/prime/Codex recovery and project viewer through the Graph Preview memory adapter, with existing-only scoped opens and no ordinary housekeeping. Activity capture and derived maintenance also support embedded Graph Preview; shared/server capture and maintenance remain refused. Project overview joins canonical records/typed Links and fork evidence from one read snapshot, and uses the existing interactive viewer. Prime includes ordinary graph Memories as untrusted bounded context alongside reviewed knowledge. Ordinary code/knowledge/activity/prime/viewer workflows remain unchanged. Graph setup affects project-local Claude configuration only when explicitly requested or fresh init does not skip it.

- Supported graph commands accept `--sandbox` with the native auto-push opt-out. Graph routes keep ordinary sync/push housekeeping absent; readonly, migration-freeze, namespace and unsupported-flag guards remain enforced.

- Fresh Graph Preview initialization writes link-preview-v7/schema 8 with nine Types. Markers v5/v6 admit only schema 7 and its original four/six Types; no read upgrades a descriptor or database. Native dep add/link admit blocks, parent-child, discovered-from and related with exact JSON metadata and thread; metadata uses the shared bounded parser without float conversion. Canonical Link selectors require the existing source guard; ordinary workspaces refuse graph-only fields.

- `graph-convert plan|prepare|verify|activate|rollback` owns explicit embedded native conversion. Bind canonical identity, exact binary/selector/database roots, full restore-verified byte-sealed checkpoint and compatible executable; require regular selector/recovery files and a selector under 16 MiB; require space, conversion actor and quiescent-writer assertion. Plan/prepare leave the original native. Optional `--prune-obsolete-code` binds official guarded pruning only on the restored candidate (native rehearsal plane at most 2 GiB); final graph budgets remain unchanged. Activation uses same-filesystem atomic engine renames, retains the original engine, fences incomplete publication with a marker and resumes only exact sealed state. Rollback refuses all later graph writes and preserves both engines/history. POSIX publication only; Windows, shared/proxied conversion and clone merge refuse. No source parsing, project execution, hook configuration or learning renewal. Fresh Graph Preview init establishes coordination gates within its authorized creation. Graph commands and native scoped prime/activity/maintenance hold strict shared workspace/physical-root gates and revalidate admission; conversion publication holds exclusive gates. Codex context dispatch keeps fallback bookkeeping outside the parent gate and its prime subprocess owns storage gating.

- Top-level history compact holds the existing exclusive workspace/physical-root gates; its dry-run and semantic admin compact remain shared. Gate failures are fatal before opening storage. Server/proxy history rewrites refuse without branch/reset/prune/GC writes; preview remains available. Embedded compaction rejects a stale plan and pending tracked changes, retaining history metadata and ignored planes.

- Markdown `create --file` shares dependency aliases and direction with `--deps`: bare targets, `depends-on:` and `blocked-by:` block the new issue; explicit `blocks:` makes the target depend on it. Custom valid types remain supported. Preserve duplicate/empty target validation and atomic batch refusal. This parser change does not repair existing edges or extend HTTP-connected file creation.

- AI duplicate analysis uses the first text response block, skipping thinking/redacted blocks without reading their contents. Missing or unparseable text and max_tokens replies preserve the mechanical candidates; later text blocks are not appended.

- Advisory Git remote-ref probes use the existing signal-aware root context with a ten-second deadline and two-second retained-pipe wait. Disable terminal/askpass prompts (including core.askPass) but retain credential.helper. Errors, cancellation and incomplete output remain UNKNOWN; init and configured-bootstrap safety/local-recovery policies stay unchanged. WaitDelay bounds pipes without guaranteeing descendant termination.

- Usage metrics cannot collect or upload. `metrics`/`off`/`example` report the policy without writing configuration; `on` fails and legacy `send-metrics` does nothing. Existing config and historical queues remain untouched.

- Purge/prune reject nonpositive day counts before duration multiplication. Confirmation commands retain the preview's limit, storage plane, age, pattern and explicit reference override; quote age/pattern as literal POSIX shell arguments. Preview and existing deletion protections remain unchanged.

- Hook uninstall removes Beads sections from the effective directory, resets only selected common-local managed configuration, and cleans a different directory only when a successful reset makes it effective. Resolve Git paths afresh to avoid stale repository caches. Preserve foreign content, backups and user/global/worktree-private paths; failures after reset must not report success.

- Direct configuration and user KV mutations mark real writes for embedded auto-commit. Direct SQL-server config/memory/KV verbs publish scoped config commits when policy is on; batch/off defer to the existing explicit commit. Proxied/Graph routes retain their own transaction ownership. User KV publication refuses observed dirty internal config keys and reports that the mutation remains in the working set.

- Native create --graph timestamps creation from the same clock read when backfilling a closed/started timestamp, so the status cannot predate creation. Open nodes leave creation to the insert/server as before. Existing explicit timestamps and ordinary graph-format boundaries remain unchanged.

- db-proxy-child forwards the existing debug posture to optional connection tracing; ordinary traffic does not emit per-connection log lines. No new service, flag or project activation is introduced.

- Proxied merge-slot create/check/acquire/release use the same holder/waiter rules through one UOW per operation; only committed attempts report acquisition. Proxied supersede resolves both IDs and adds the supersedes edge plus checked closure in one retried transaction; refusal rolls both back. No automatic holder expiry, queue scheduling or project conversion.

- Native and proxied ship share capability selection, closed/force policy and output. Proxied publication reads and adds the provides label in one retried UOW; missing/ambiguous/open refusals, dry-run and already-shipped outcomes publish no commit. Proxied duplicate resolves both IDs, adds its duplicates edge and performs checked closure in one retried UOW; refusal rolls all changes back and success output follows commit. Existing supersede semantics remain unchanged.

- Proxied --readonly/BD_READONLY attaches only to authenticated running proxies and an existing current-schema database. Read config without legacy migration, skip version stamps/hooks/profiling/maintenance and open existing shared gates without creating files; failed gates refuse. SQL reads pass through the guarded provider; mutation commands remain blocked. Missing gates/proxy/schema need an authorized writable initialization first.

- Authenticated GitHub/GitLab/Jira/ADO/Linear endpoints are local-only settings. Direct discovery, validation and sync paths share fail-closed endpoint resolution; old synced URLs require an explicit local choice. Linear API-only construction with a nil store never probes a local database.

- Native `delete` requires exact IDs for previews and writes, including batches and routed targets; read commands retain abbreviation. Graph revision guards remain graph-only.
- `init` refuses the account home before artifact writes unless it is its own Git root or the caller explicitly supplied BEADS_DIR. The latter keeps home-level instruction, ignore, hook and Git artifacts untouched; inherited Git routing variables cannot establish a home Git root. Exit 15 is outside protocol-v0.
- Only create, atomic batch and template clone callbacks explicitly opt into replay after a confirmed rolled-back conflict; restore inputs and discard failed-attempt results. Other transaction callbacks remain at-most-once.
- List selects dependency records for the displayed IDs, preserving parent/filter/watch semantics. Pretty trees render cyclic parent components finitely, each ID once. Routed-create commit warnings identify already-durable creates without reporting a false creation failure.

- Strict readonly refuses backup init/sync/remove/restore and import (including import --dry-run). Backup restore refuses before exclusive gate acquisition; import returns migration-freeze errors through its cleanup path. Backup status/list remain reads. Shared readonly gates create no files; repeated in-process commands close prior storage before releasing its gates. Strict proxied and Graph Preview admission remain existing-only.

- Hook installs from linked worktrees update their own tracked hook copy; retain the configured primary execution location and leave untracked/shared Git directories unchanged. Formula registry lookup precedes file-path fallback in cook/pour/wisp/bond/seed. Padded variable references resolve consistently, including step notes; unknown variables remain literal.
- `link` uses the existing dependency-editor role while retaining Graph Preview/proxy routing, custom type lengths and hierarchy guards. Duplicate output keeps page count plus true total/truncation; limit zero remains unlimited.
- Local server status probes the resolved endpoint before declaring an untracked server down. Explicit start refuses an empty externally managed workspace unless forced; unresolved port diagnostics do not recommend initializing a replacement database.

- Personal migration uses the planning workspace mode and exact database, preserves its events setting, refuses redirects, graph formats, self-targets, collisions and missing named databases, and commits a complete destination before deleting the source. Uncertain destination commits retain both copies; batch/off preserve explicit commit policy.

- Generated Git hook shims keep Perl as a supervising parent, bound TERM-resistant children with KILL and reap them. GNU timeout and existing exit-status policy remain supported.

- Native Codex activity validates settings, retry identity and event persistence within one existing memory transaction/engine acquisition. Preserve project enablement, original events, normal hook trust, budgets and refusal paths; Graph Preview retains its existing route. This reduces contention without disabling capture or adding a queue/service.

## Work Guidance

- Metadata JSON inputs are bounded at 4 MiB for inline and `@file` forms, independently of the 1 MiB graph body/property inputs. Refuse oversize or invalid input before mutation; preserve revision guards and retained versions. Graph status advertises the metadata input budget. Complete graph acquisition retains its separate 16 MiB bound.

Keep existing CLI primitives and machine-readable output stable. Follow AGENTS.md and ../../engdocs/TESTING.md.

Integration credentials, including notion.token, route only to local YAML/environment in single and batch configuration. Notion authentication uses that source even without an open database; its data-source/view metadata remains database-backed.

Memory diagnostics expose literal key-prefix inclusion/exclusion without changing default enumeration. Knowledge reads exclude derived code namespaces; symbol-link validation reads only selected indexed files and applicable provenance, retaining the proposal/review guards.

## Verification

Use the affected existing checks selected in engdocs/TESTING.md; verify indexed child contracts.
Graph Preview server fixtures clean only fresh authority-named databases bound to the temporary workspace and explicitly supplied loopback test port.

## Child DOX Index

- [setup/DOX.md](setup/DOX.md) — Agent setup.
- [doctor/DOX.md](doctor/DOX.md) — Diagnostics.
- [protocol/DOX.md](protocol/DOX.md) — CLI protocol.
