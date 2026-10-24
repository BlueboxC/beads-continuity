# DOX.md — Graph domain contracts

## Purpose

Own the experimental public graph vocabulary and invariants.

## Ownership

Graph domain contracts.

## Local Contracts

Root and parent DOX contracts remain binding.

- Canonical Bead/Link identities, immutable version tokens and installed Type descriptors are separate from ordinary task dependencies.
- Carried attribution uses writer-supplied/unknown basis; it is not authentication.
- Preview3 preparation exposes explicit NewPublicInScopeRef/ParsePublicRef/NewPublicBead/NewPublicLink identity helpers. Existing constructors retain schema8 rooted validation and descriptor bytes; the new helpers do not allocate, relabel or migrate existing identities. Preserve current Resource records and the fork's Go1.27-compatible UTF-8 assertions.

## Work Guidance

Preserve contributor provenance and MIT attribution in PROVENANCE.md. Reuse existing Dolt storage adapters.

## Verification

Run graphops tests and graphstore/BDP conformance consumers.

## Child DOX Index
