# DOX.md — BDP graph reads

## Purpose

Own read-only BDP graph projections and pagination.

## Ownership

BDP graph reads.

## Local Contracts

Root and parent DOX contracts remain binding.

- Validate persisted authority on every read; reject inconsistent payloads and incomplete bounded representations.
- Map stored claimed attribution to public writer-supplied basis. Do not authenticate actors or expose local history ordinals.
- Partial Preview3 descriptors require the store's captured effective ownership sidecar; do not infer inherited owned groups from descriptor spelling. Project captured native endpoint pins and sort by public identity. This is projection compatibility, not qualification of the Preview3 HTTP routes.
- Pagination is process-local and bounded; tokens cannot resume across server restarts.

## Work Guidance

Use the caller-owned graphstore. Do not open, migrate or write storage here.

## Verification

Run graphread tests, schema conformance and HTTP authority/security tests.

## Child DOX Index
