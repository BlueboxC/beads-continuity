# DOX.md — Beads Continuity

## Purpose

Own Beads Continuity, the BlueboxC Beads fork, and its development contracts for persistent project context.

## Ownership

Root owns fork policy, public API packages, license, build/version files, small packaging assets and the Child DOX Index. Indexed children own their subtrees. FORK.md describes the public baseline, implemented extensions and verified corrections; docs/core-concepts/fork-continuity.md owns fork onboarding and capability limits.

## Local Contracts

- Read this file and every indexed DOX.md along the target path before edits. A closer contract cannot weaken this framework.
- Keep the DOX hierarchy current after meaningful changes; update affected indexes and remove stale text.
- Preserve upstream attribution and the MIT license. Keep origin pointed at BlueboxC/beads-continuity and upstream at gastownhall/beads.
- Memory Recall and Forget report row existence independently of its value; imported empty memories are found and can be deleted. Empty new content remains rejected.
- Public memory lists support literal case-sensitive user-key prefix inclusion/exclusion, combined with the existing case-insensitive search; empty selectors preserve full-plane behavior. The optional AtomicMemories capability guards and mutates derived generations in one storage transaction; basic Memories callers remain compatible.
- Optional SnapshotMemories lends synchronous read-only MemoryReader callbacks from one existing transaction. Embedded native graph/maintenance reads only the current index generation, preserving retained blobs, human records and history; unsupported adapters retain their full-read path.
- Use bd for tasks, dependencies and durable facts; record evidence before closing work. Keep private project records out of this public fork.

- Anonymous usage metrics are removed: collectors, machine IDs, queues, upload transports and detached flushers stay absent. Saved settings/environment cannot enable them. Keep legacy metrics commands inert and version diagnostics/managed Dolt version probes local-only. Explicit sync, integrations, downloads and operator-enabled OpenTelemetry retain separate network contracts.

- Public journal rows expose optional actor resolution provenance, not authenticated identity or authority. `journalops.WithActorSource` binds it to a caller actor; absent historical provenance stays absent. Existing issue/audit identity strings and SDK operation signatures remain compatible.

- Public issueops contexts may bind a read-only ExternalResolver. Missing provider results remain blocked; local selection/claim remains transactional but provider observations are not a distributed transaction.

- Graph Preview is a separate explicit workspace format adapted from versioned-beads/beads. Preserve the ordinary v1.3.1 API, continuity planes and project data. New graph workspaces use the existing Dolt engine and the fork continuity adapter; never convert existing projects implicitly. Human knowledge binds canonical retained Memories through private key pointers; derived code/catalogs and observed activity remain separate projections in that same database.

## Work Guidance

- Prefer the simplest complete implementation and existing extension points. Avoid speculative configuration and dependencies.
- The owner authorizes publishing the verified fork corrections and extensions to BlueboxC/beads-continuity. The owner has authorized the open fork PR #2 and correction comments on upstream issues; other PR creation/reopening and merges require separate authorization; keep project data, installation records and private evidence out of public commits.
- Include every verified correction in this fork and its local installed build without waiting for upstream review. Keep private disclosure and publication approval separate from local fixes.
- The fork objective is continuity: recover project direction, existing modules, verified fixes and their evidence. Use one Beads product and its existing Dolt storage boundary.
- Native baseline is upstream v1.3.1 at c1c4b642ac1c08d8c828007a1c2f96e47e43ef7c. Verified corrections preserve its baseline API; continuity extensions reuse the existing Dolt memory boundary. Keep main available for upstream synchronization and publish fork work on codex/project-continuity.
- Root and example Go modules keep a Go 1.26.9 consumer floor and select Go 1.26.10 for builds; the Nix overlay uses the same patched compiler. CI MCP packaging pins uv 0.13.4. Require golang.org/x/net >=0.60.0, gRPC >=1.83.2 and golang.org/x/crypto >=0.56.0; align example locks with the root effective dependency graph. Engine upgrades require isolated old/new native-backup round trips, retained data/history checks and the normal regression/build gates before installation; preserve a matched database/binary recovery set.
- SQL and catalog dependency updates preserve existing view/diff/SAVEPOINT protections and typed missing-database handling. Keep driver, engine and analyzer pins explicit in every independent consumer module; do not equate supplied SQL regressions with general dialect compatibility.
- Qualified engine merge features preserve logical references and conflict evidence through GC/backup. Unsupported key transformations or conflicting virtual bases refuse explicitly; rollback after feature writes requires matched data and engine preimages.
- The embedded engine pins official Dolt 2.5.1 plus the minimal BlueboxC/dolt engine corrections documented in FORK.md. Keep driver, engine and external CLI versions distinct. Carry storage fixes in the engine dependency, preserve contributor attribution, and do not enable multi-branch commit options or add core SQL workarounds.
- Follow AGENTS.md, AGENT_INSTRUCTIONS.md and engdocs/TESTING.md. Keep manual experiments in disposable directories.

## Verification

- Use the checks selected by engdocs/TESTING.md, including git diff --check. Report unavailable tooling instead of claiming a check passed.
- Verify each Child DOX Index points to an existing file; keep every DOX file reachable from this root.

## Child DOX Index

- [cmd/DOX.md](cmd/DOX.md) — Executable entry points.
- [internal/DOX.md](internal/DOX.md) — Internal implementation.
- [backend/DOX.md](backend/DOX.md) — Public backend contracts.
- [docs/DOX.md](docs/DOX.md) — User documentation.
- [engdocs/DOX.md](engdocs/DOX.md) — Engineering guidance.
- [scripts/DOX.md](scripts/DOX.md) — Development and release scripts.
- [integrations/DOX.md](integrations/DOX.md) — External interfaces.
- [plugins/DOX.md](plugins/DOX.md) — Agent plugins.
- [examples/DOX.md](examples/DOX.md) — Usage examples.
- [test/DOX.md](test/DOX.md) — Go contract checks.
- [tests/DOX.md](tests/DOX.md) — Regression and external tests.
- [release-gates/DOX.md](release-gates/DOX.md) — Release qualification.
- [npm-package/DOX.md](npm-package/DOX.md) — npm distribution.
- [.github/DOX.md](.github/DOX.md) — GitHub automation.
- [.beads/DOX.md](.beads/DOX.md) — Project tracking assets.
- [graphops/DOX.md](graphops/DOX.md) — Experimental public graph vocabulary and contributor provenance.
