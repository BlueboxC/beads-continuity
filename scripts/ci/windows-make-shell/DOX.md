# DOX.md — Windows Make toolchains

## Purpose

Provision the existing native, MSYS2 and Cygwin Make-shell CI legs.

## Ownership

Shared tool discovery, cache keys, verified package installation and refusal controls. Workflows remain owned by `.github/DOX.md`.

## Local Contracts

- Prefer the runner image, then verified cached content, then bounded network fallback. Native downloaded/cached Make must match its SHA-256 pin.
- MSYS2 retains the image's sync database and requires trusted package signatures; never refresh or partially upgrade it.
- Cygwin requires the pinned signed installer and exactly one signed nonempty index layout before invoking local installation. Invalid caches hand over to the existing online fallback.
- PR jobs restore only. The saver runs only on this repository's main pushes; keys bind native content, the image's MSYS2 database or the UTC month.
- Keep host isolation, existing Make exercise bodies, fail-fast behavior and the distinction between provisioning skips and product failures.

## Work Guidance

Reuse the contributor helpers and preserve attribution. Keep retries and timeouts finite. Do not add credentials or change application runtime configuration.

## Verification

- Run `./scripts/test.sh ./scripts/...` for workflow wiring and cache ownership.
- On Windows run `pwsh -File scripts/ci/windows-make-shell/test-cache-inputs.ps1`; it uses disposable files and never executes its mismatched executable fixture.
- Exercise all three native Windows CI legs, including offline seed/restore where available.

## Child DOX Index
