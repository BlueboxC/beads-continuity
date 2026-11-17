---
title: JSON Output Schema Contract
description: The stable JSON output contract for bd --json commands, covering the schema_version envelope, per-command fields, and consumer guidelines.
---

Last reviewed: 2026-11-17

Freshness source: `cmd/bd/output.go`, `cmd/bd/errors.go`, and
`cmd/bd/protocol/json_contract_test.go`.

For native commands using the shared JSON formatter, `BD_JSON_ENVELOPE=1`
wraps the response in a uniform envelope. Commands with dedicated raw or
streaming formats keep their own output contract. The CLI deprecation notice
announces a plan to make the envelope the default in v2.0.

## Migration Guide

### Opt in to the envelope format

```bash
export BD_JSON_ENVELOPE=1
```

### Envelope format (BD_JSON_ENVELOPE=1)

The shared formatter wraps a payload under `data`:

```json
{"schema_version": 1, "data": {"id": "beads-abc"}}
```

The original payload is untouched inside `.data` — no type corruption,
no field injection. Works identically for objects, arrays, and maps.

When a `--limit`-truncated listing runs in envelope mode (currently wired
for `bd ready`), the envelope also carries a `pagination` key:

```json
{"schema_version": 1, "data": [{"id": "beads-abc"}], "pagination": {"returned": 1, "total": 42, "truncated": true}}
```

`total` is omitted when unknown. The `pagination` key is omitted without
confirmed truncation. `bd ready` can print a truncation hint on stderr in
either format.

### Updating consumers

```bash
# Before (legacy):
bd list --json | jq '.[0].id'
bd show beads-abc --json | jq '.[0].title'

# After (envelope):
bd list --json | jq '.data[0].id'
bd show beads-abc --json | jq '.data[0].title'

# Version check (object commands, e.g. create):
bd create "Example" --json | jq '.schema_version'
```

### Timeline

- **Current release**: Legacy format is default. Set `BD_JSON_ENVELOPE=1` to opt in.
  The shared formatter prints a deprecation notice for legacy output if
  stderr is a terminal, at most once per invocation.
- **Planned for v2.0**: Envelope becomes the default.

## Schema Version

Current version: **1**

The `schema_version` field is an integer. A breaking change to required fields,
output structure, or field types calls for a new version.

Additive changes (new optional fields) do NOT bump the version.

## Output Formats

### Envelope mode (BD_JSON_ENVELOPE=1)

The shared formatter emits a uniform envelope:

```json
{
  "schema_version": 1,
  "data": {
    "id": "beads-abc",
    "title": "Example issue",
    "status": "open"
  }
}
```

Arrays are wrapped the same way:

```json
{
  "schema_version": 1,
  "data": [
    {"id": "beads-abc", "title": "First"},
    {"id": "beads-def", "title": "Second"}
  ]
}
```

### Legacy mode (current default)

### Object commands (create, ping, etc.)

Commands that return a single result emit a JSON object with
`schema_version` as a top-level field alongside the data:

```json
{
  "schema_version": 1,
  "id": "beads-abc",
  "title": "Example issue",
  "status": "open",
  "priority": 1,
  "issue_type": "task",
  "created_at": "2026-04-20T12:00:00Z"
}
```

### List commands (list, ready, search, stale, show, close, update, etc.)

The default issue-list payload is a JSON array. It includes the ordinary
`show`, `close`, and `update` results, even when one issue is returned.
Array output carries no top-level `schema_version` field in legacy mode:

```json
[
  {"id": "beads-abc", "title": "First"},
  {"id": "beads-def", "title": "Second"}
]
```

### Error output

Structured errors use stdout or stderr depending on the command and error
path. Some failures still emit text or return only an exit status. A JSON
error in legacy mode can look like this:

```json
{
  "schema_version": 1,
  "error": "issue not found: beads-xyz",
  "code": "not_found"
}
```

`error` is the message. `code` and `hint` (a remediation suggestion) are
optional. Proxied capability refusals include `code` and `mutates`. They
include `reason` when the capability registry supplies one. In envelope mode,
`schema_version` stays at the top level and the error payload moves under
`data`. The usual error exit status is `1`. A migration freeze uses `14`.

## Field Contracts by Command

### bd list --json

With `--skip-labels`, the payload is an object with an `issues` array and
`meta` containing `skip_labels` and `count`. Each issue has `labels: []`.

Required fields per item:
- `id` (string): Issue ID (e.g., "beads-abc")
- `title` (string): Issue title
- `status` (string): open, in_progress, closed, deferred
- `priority` (number): 0-4
- `issue_type` (string): bug, feature, task, epic, chore
- `created_at` (string): RFC3339 timestamp

Optional fields:
- `description`, `owner`, `updated_at`, `closed_at`
- `labels` (string[]): Attached labels
- `dependencies` (object[]): Dependency records
- `dependency_count`, `dependent_count`, `comment_count` (number)
- `parent` (string|null): Parent issue ID

### bd ready --json

Same schema as `bd list --json`. Items are filtered to unblocked issues only.
Each item includes `dependency_count`, `dependent_count`, `comment_count`,
and optional `parent` fields. In envelope mode a `--limit`-truncated result
adds the envelope-level `pagination` key (see the envelope section above).

### bd blocked --json

Returns issues that are blocked by unresolved dependencies.
Each item includes all standard issue fields plus:
- `blocked_by_count` (number): Number of blocking dependencies
- `blocked_by` (string[]): IDs of blocking issues

### bd show --json

Returns an array with one element per resolved issue. In legacy mode the
array is top-level and its items omit `schema_version`. Items have the
standard issue fields plus:
- `description`, `acceptance_criteria` (string, optional)
- `revision` (string): guarded-write optimistic-concurrency token, an opaque
  decimal string compared by equality and echoed back verbatim; always present,
  including a legacy `"0"`
- `dependencies` (object[], optional): Populated dependency records
- `dependency_count`, `dependent_count`, `comment_count` (number)
- `comments` (object[]): Included with `--include-comments` when the thread has comments
- `dependents` (object[]): Included with `--include-dependents` when there are dependents
- `comments_omitted` (boolean, optional): `true` only when `comment_count` is
  nonzero and `comments` was left out of the response (no `--include-comments`).
  Absent when comments were included or when there are none to omit (ga-clgh)

### `import --json`

Returns a summary object when `--json` is active:
- `source` (string): File path or "stdin"
- `created` (number): Issues created
- `updated` (number, optional): Existing issues updated
- `unchanged` (number, optional): Rows identical to local state, untouched
- `skipped` (number): Issues skipped (stale rows + dedup)
- `dedup_skipped` (number, optional): Issues skipped by `--dedup` title match
- `memories` (number, optional): Memory records imported
- `ids` (string[], optional): IDs of created issues
- `updated_issues` (object[], optional): Per-issue summary of what an update changed
- `tie_kept_local_ids` (string[], optional): Equal-`updated_at` rows where local state won
- `stale_skipped_ids` (string[], optional): Rows older than the local issue, skipped
- `skipped_dependencies` (string[], optional): Dependency edges whose target id was absent
- `dry_run` (boolean, optional): Present when `--dry-run` was active

### bd export --json

Outputs JSONL (one JSON object per line), not wrapped in an envelope.
Each line is a self-contained issue or memory record, discriminated by
`_type` (`"issue"` / `"memory"`). Export lines do **not** carry
`schema_version` — that field belongs to the `--json` command envelope,
not to the interchange stream. The interchange's own version marker is the
optional `_schema` header record (`{"_schema":"beads-jsonl/1"}`), which
readers skip.

Issue records carry an optional `wisp_plane` boolean: the explicit
wisps-plane marker. Export stamps it on rows that live in the wisps table
when the row flags alone cannot prove the plane (a `no_history: true` record
is otherwise ambiguous — an unpromoted no-history wisp and a promoted one
look identical). Import routes the storage plane by this marker, never by
`no_history`: a non-ephemeral no-history issue without the marker goes to the
durable issues table. Ephemeral rows carry `ephemeral: true`. The marker is a
fresh key rather than a reuse of the legacy `wisp` boolean so that older
readers, which do not know it, degrade to flag routing instead of importing
marked rows as ephemeral (purge-eligible and export-excluded). The v0.35–
v0.37 `wisp` key — those streams' spelling of `ephemeral` — is still honored
as a read-side legacy alias.

## Consumer Guidelines

1. **Check `schema_version`** on object output. If the version is
   higher than expected, log a warning but attempt to parse anyway
   (additive changes are backward-compatible).

2. **For default issue lists**, parse the legacy response as an array or
   read the array from `data` in envelope mode. Honor command-specific forms.

3. **Ignore unknown fields**. New fields may be added without bumping
   the schema version.

4. **Use `--json` flag**, not `--format json`. The `--json` flag is
   the stable contract; `--format` is for human-readable variants.
