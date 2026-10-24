# DOX.md — legacyimport

## Purpose

Own bounded decoding of ordinary Beads export records.

## Ownership

The Go package and its contributor tests.

## Local Contracts

Parent DOX contracts remain binding. Keep validation and decoded records independent of storage; importing current records does not preserve retained revisions or Dolt history.

## Work Guidance

Adapted from donnabox/beads PR291 at 0c4dc1f0d68a371e56b3e461b968e828e4ea254c. These preparation components do not activate Preview3 storage or replace the fork continuity adapter.

## Verification

Run `go test -tags=gms_pure_go ./internal/legacyimport` and affected graphops consumers.

## Child DOX Index

- None.
