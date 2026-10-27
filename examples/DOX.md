# DOX.md — Usage examples

## Purpose

Own usage examples within the fork.

## Ownership

Runnable integration and workflow examples.

## Local Contracts

Root DOX.md and the parent chain remain binding.

- Both independent Go example modules explicitly mirror the parent module’s Dolt, SQL-analyzer and local MySQL-driver replacements; Go replacements are not inherited through dependencies. Keep their checksums aligned and validate the effective module graph.

## Work Guidance

Examples must state prerequisites and use disposable data for demonstrations.

## Verification

Formula primitive fixtures and their registered smoke cases travel together. Run `./scripts/test.sh -run TestFormulaPrimitiveExamples ./cmd/bd` after changing them; the gate-typed fixture pins preparation-task and companion-gate direction.

Run `scripts/build-examples.sh` after dependency changes to type-check both example modules against their recorded dependency graphs, then run each module’s `go test ./...`. Discovery fixtures use embedded Dolt metadata/directories rather than removed SQLite files.

## Child DOX Index
