# DOX.md — GitHub automation

## Purpose

Own github automation within the fork.

## Ownership

CI workflows, repository templates and automation helpers.

## Local Contracts

Root DOX.md and the parent chain remain binding.

- The Dependabot vendor-hash workflow requires an unconditional independent `nix build .#default` before reporting a matched hash or committing an update; neither step/job may ignore its failure. Preserve the existing actor, same-repository, permission and non-force-push boundary.

## Work Guidance

Keep required checks aligned with Makefile and scripts. Go setup follows the root module toolchain; pin golangci-lint v2.15.0 for Go 1.27 export-data compatibility in all lint lanes; all MCP package jobs pin uv 0.13.5. Do not enable publication or introduce credentials during fork bootstrap.

## Verification

Use the affected existing checks selected in engdocs/TESTING.md; verify indexed child contracts.

## Child DOX Index
