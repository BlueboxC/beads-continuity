# DOX.md — Development and release scripts

## Purpose

Own development and release scripts within the fork.

## Ownership

Build, CI, verification, packaging and release helpers.

## Local Contracts

Root DOX.md and the parent chain remain binding.

- Nix vendor-hash refresh accepts success only after building the restored or updated real default.nix. Retain the original bytes/mode until verification, restore them on handled failures, keep operator backups untouched and isolate diagnostic logs per run. GNU/BSD sed and Docker mount arguments preserve empty/space-containing arguments. Run one updater per checkout; force-kill recovery remains manual.

- The ordinary test environment seeds its private Dolt root with metrics/version checks disabled and suppresses event flushing. Do not rely on or mutate operator opt-outs to qualify tests.

- `fmt-check` and `fmt` share the Git source inventory: existing regular tracked Go files (including ignored paths) and nonignored untracked Go files. Exclude private ignored artifacts, deleted files and symlinks; inventory/gofmt failures remain fatal. Preserve historical evidence bytes.

## Work Guidance

Generated CLI freshness checks compare generated pages while excluding DOX.md work contracts; DOX reachability is checked separately.

Reuse existing build and verification entry points. Do not run release publication as part of local setup.

## Verification

Use the affected existing checks selected in engdocs/TESTING.md; verify indexed child contracts.

## Child DOX Index
