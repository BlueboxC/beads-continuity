# DOX.md — npm distribution

## Purpose

Own npm distribution within the fork.

## Ownership

npm wrapper and platform installation packaging.

## Local Contracts

Root DOX.md and the parent chain remain binding.

## Work Guidance

Preserve binary provenance and platform selection; use package verification when packaging changes. Redirect downloads close the previous stream before reopening the same archive path; do not schedule an unlink that can remove the replacement download.

## Verification

Use the affected existing checks selected in engdocs/TESTING.md; verify indexed child contracts. Run `node --test test/download-redirect.test.js` for redirect publication and extraction regressions.

## Child DOX Index
