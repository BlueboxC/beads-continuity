# DOX.md — Python MCP interface

## Purpose

Own python mcp interface within the fork.

## Ownership

Python package exposing Beads operations to MCP clients.

## Local Contracts

Root DOX.md and the parent chain remain binding.

- Re-resolve filesystem-sensitive workspace paths on each call. Retargeted aliases and newly initialized worktree-local `.beads` roots must select their current owner; worktrees without a local root retain the existing main-repository fallback. Keep the client pool keyed by the resolved root.

- MCP issue updates forward labels through `--set-labels`. Omitted or null preserves existing labels, an empty list clears them, and a nonempty list replaces them.
- Explicit reopen reports a successful no-op as a tool error. Updating to open reopens a closed or configured-done issue before applying additional fields. The reopen reason remains in the reopen operation and closed-issue updates retain their list response. A non-closed issue uses an ordinary update and clears any dated defer. Report partial or uncertain field updates without retrying or changing timeout exit codes.

- All six CLI execution paths share a 120-second communication deadline and child cleanup on timeout/cancellation. POSIX cleanup terminates the owned process group, including pipe-retaining descendants; pipe draining has a five-second backstop. Cancellation propagates; timeout reports code 124 and an unknown operation outcome, with no automatic retry. Preserve JSON/text decoding and workspace routing. Init uses only initialization and actor flags.
- Pass caller-supplied positional operands after `--` and bind option values in a single `--name=value` argument. This applies to issue IDs, comment/note bodies, and dependency endpoints. Issue creation uses named options. Debug output names the command and workspace routing without logging argument values.
- Require Python >=3.11 and FastMCP 4.1.2, with Pydantic 2.13.5 and pydantic-settings 2.15.0. Preserve the existing tool schemas, stdio/CLI routing and fork behaviors. Published runtime requirements include PyJWT >=2.15.1 and opentelemetry-api >=1.45.1. The lock excludes Python 3.10 and dnspython below 2.9.0, and retains joserfc >=1.7.7. The dev group requires urllib3 >=2.8.0.

- The eight query/discovery tools advertise advisory MCP readOnlyHint metadata. Mixed context/admin actions and writers remain unannotated. This metadata does not enforce CLI readonly mode or change workspace initialization.

## Work Guidance

Use the existing package tests and dependency declarations. Test workspaces inherit the owned TMPDIR and retain per-fixture cleanup. Installation into Codex uses the CLI skill and native hooks unless MCP is explicitly requested.

## Verification

Use `make ci-package-mcp` with the canonical candidate binary. Its Ruff, mypy, unit, real CLI/stdio and packaging checks qualify the connector. Comment/note round trips must preserve option-shaped text and actor identity. Verify indexed child contracts.

Run the five worktree regressions by default with separate temporary embedded-Dolt roots. Verify native history sync through `refs/dolt/data`, passive JSONL export, path changes and exact issue isolation; never commit live database directories or ignore failed sync commands in fixtures.

## Child DOX Index
