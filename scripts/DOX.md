# DOX.md — Development and release scripts

## Purpose

Own development and release scripts within the fork.

## Ownership

Build, CI, verification, packaging and release helpers.

## Local Contracts

Root DOX.md and the parent chain remain binding.

- Nix vendor-hash refresh accepts success only after building the restored or updated real default.nix. Retain the original bytes/mode until verification, restore them on handled failures, keep operator backups untouched and isolate diagnostic logs per run. GNU/BSD sed and Docker mount arguments preserve empty/space-containing arguments. Run one updater per checkout; force-kill recovery remains manual.

- The ordinary test environment clears inherited Git variables before installing its private configuration; fixture setup cannot use an outer repository selector.

- The ordinary test environment seeds its private Dolt root with metrics/version checks disabled and suppresses event flushing. Do not rely on or mutate operator opt-outs to qualify tests.

- `fmt-check` and `fmt` share the Git source inventory: existing regular tracked Go files (including ignored paths) and nonignored untracked Go files. Exclude private ignored artifacts, deleted files and symlinks; inventory/gofmt failures remain fatal. Preserve historical evidence bytes.

- The normal test runner uses a 45-minute per-package hang backstop: the Preview3 graphstore coverage suite exceeds the previous 25-minute CLI allowance. This changes no assertion, acquisition cap or operation deadline. TEST_TIMEOUT remains the explicit diagnostic override.

## Work Guidance

Generated CLI freshness checks compare generated pages while excluding DOX.md work contracts; DOX reachability is checked separately.

Reuse existing build and verification entry points. Do not run release publication as part of local setup.

## Verification

Use the affected existing checks selected in engdocs/TESTING.md; verify indexed child contracts.

## Child DOX Index

- [ci/windows-make-shell/DOX.md](ci/windows-make-shell/DOX.md) — Windows Make toolchain provisioning and cache verification.
