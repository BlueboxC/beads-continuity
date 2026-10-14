# DOX.md — GitHub automation

## Purpose

Own github automation within the fork.

## Ownership

CI workflows, repository templates and automation helpers.

## Local Contracts

Root DOX.md and the parent chain remain binding.

- The Dependabot vendor-hash workflow requires an unconditional independent `nix build .#default` before reporting a matched hash or committing an update; neither step/job may ignore its failure. Preserve the existing actor, same-repository, permission and non-force-push boundary.

## Work Guidance

Keep required checks aligned with Makefile and scripts. Do not enable publication or introduce credentials during fork bootstrap.

## Verification

Use the affected existing checks selected in engdocs/TESTING.md; verify indexed child contracts.

## Child DOX Index
