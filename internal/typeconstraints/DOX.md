# DOX.md — typeconstraints

## Purpose

Own pure validation of selected Type/Link outcomes.

## Ownership

The Go package and its contributor tests.

## Local Contracts

Parent DOX contracts remain binding. Validate the complete affected graph result; keep runtime storage and authority outside these predicates.

## Work Guidance

Adapted from donnabox/beads PR291 at 0c4dc1f0d68a371e56b3e461b968e828e4ea254c. These preparation components do not activate Preview3 storage or replace the fork continuity adapter.

## Verification

Run `go test -tags=gms_pure_go ./internal/typeconstraints` and affected graphops consumers.

## Child DOX Index

- None.
