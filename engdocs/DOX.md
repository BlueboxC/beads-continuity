# DOX.md — Engineering guidance

## Purpose

Own engineering guidance within the fork.

## Ownership

Charters, testing authority, architecture decisions and maintainer guidance.

## Local Contracts

Root DOX.md and the parent chain remain binding.

## Work Guidance

LINTING.md owns the pinned v2.15.0 Go 1.27-compatible lint workflow. TESTING.md owns test selection and fixture isolation, including shared gittestenv startup resets, including private Dolt metrics/update suppression and command-line suppression of automatic Git maintenance before worktree creation. PROJECT_CHARTER.md defines the upstream core boundary; root FORK.md defines the fork objective.

design/graph-preview-conversion.md owns explicit embedded adoption, preservation, rehearsal, publication/recovery and rollback gates for existing projects; no existing project changes format implicitly.

ERROR_HANDLING.md owns fatal, advisory and cleanup guidance. Review its examples against current functions before renewing freshness; authoritative workspace metadata failures stay fatal and preserve the original selector.

## Verification

Use the affected existing checks selected in engdocs/TESTING.md; verify indexed child contracts.

## Child DOX Index
