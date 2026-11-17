# OpenTelemetry Data Model

Last reviewed for privacy and export behavior: 2026-11-17.

The sources are `internal/telemetry/`, `cmd/bd/command_telemetry.go`,
`internal/storage/dolt/store.go`, `internal/hooks/`, `internal/compact/haiku.go`,
and `cmd/bd/find_duplicates.go`.

OpenTelemetry requires `BD_OTEL_ENABLED=true` or a supported legacy
`BD_OTEL_*` selector. Standard `OTEL_*` settings configure the opt-in, and
`OTEL_SDK_DISABLED=true` overrides it.

Spans use the console exporter when `OTEL_TRACES_EXPORTER=console`. Metrics
support console export and OTLP HTTP export to a configured metrics or shared
OTLP endpoint. Both console exporters write to stderr, leaving stdout available
for command output such as JSON. There is no remote trace or log exporter.

---

## Metric Naming Convention

OTel SDK names use **dot notation** internally. Prometheus-compatible backends (VictoriaMetrics, Prometheus) export these as **underscore-separated** names, appending type suffixes:

| Code name (SDK) | Exported name (Prometheus/VM) |
|-----------------|-------------------------------|
| `bd.storage.operations` | `bd_storage_operations_total` |
| `bd.storage.operation.duration` | `bd_storage_operation_duration_ms` |
| `bd.storage.errors` | `bd_storage_errors_total` |
| `bd.issue.count` | `bd_issue_count` |
| `bd.db.retry_count` | `bd_db_retry_count_total` |
| `bd.db.lock_wait_ms` | `bd_db_lock_wait_ms` |
| `bd.db.circuit_trips` | `bd_db_circuit_trips_total` |
| `bd.db.circuit_rejected` | `bd_db_circuit_rejected_total` |
| `bd.ai.input_tokens` | `bd_ai_input_tokens_total` |
| `bd.ai.output_tokens` | `bd_ai_output_tokens_total` |
| `bd.ai.request.duration` | `bd_ai_request_duration_ms` |

---

## Event Index

| Event | Category | Status |
|-------|----------|--------|
| `bd.command` | CLI | ✅ Implemented |
| `storage.<Method>` | Storage (direct) | ✅ Implemented |
| `storage.<Role>.<Method>` | Storage (issueops surface) | ✅ Implemented |
| `dolt.*` | Dolt Backend | ✅ Implemented |
| `doltserver.*` | Server Lifecycle | 🔲 Roadmap (Tier 1) |
| `hook.exec` | Hooks | ✅ Implemented (span only) |
| `anthropic.messages.new` | AI | ✅ Implemented |

---

## 1. Identity Hierarchy

### 1.1 Instance

The resource uses `service.name`, `service.version`, and SDK detectors for the
host, process ID, executable name, and runtime. Default detection omits raw
argv, executable paths, and account owners. `OTEL_SERVICE_NAME` and
`OTEL_RESOURCE_ATTRIBUTES` are operator-controlled overrides.

### 1.2 Command

The command span has a bounded startup attribute set.

| Attribute | Type | Source |
|---|---|---|
| `bd.command` | string | Subcommand name (`create`, `list`, `show`, etc.) |
| `bd.version` | string | Current bd version string |
| `bd.args` | string | Parsed command path and sorted long names of explicitly set flags |
| `bd.actor` | string | Actor identity — set after actor resolution (may lag span start) |

---

## 2. CLI Command Events

### `bd.command.<name>`

Emitted once per `bd` subcommand execution. Anchors all subsequent events for that command. The span name is `bd.command.` + command name (e.g. `bd.command.create`).

| Attribute | Type | Description |
|---|---|---|
| `bd.command` | string | Subcommand name |
| `bd.version` | string | bd version |
| `bd.args` | string | Parsed command path below `bd` plus sorted long names of explicitly set flags, such as `comments add --file`. Positional arguments, flag values, and raw argv are omitted |
| `bd.actor` | string | Actor identity (set after actor resolution) |

---

## 3. Storage Events

Direct storage and issueops role decorators emit `storage.<Method>` and
`storage.<Role>.<Method>` spans. The `bd.storage.operations`,
`bd.storage.operation.duration`, and `bd.storage.errors` metrics use only
`db.operation` as a dimension. Method-specific span attributes never become
operation labels. Failed operations record an error status without error text
on the storage decorator span.

### `storage.CreateIssue`

Emitted when an issue is created.

| Attribute | Type | Description |
|---|---|---|
| `db.operation` | string | `"CreateIssue"` |
| `bd.issue.type` | string | Issue type (`task`, `epic`, `merge-request`, etc.) |
| `bd.actor` | string | Actor creating the issue |

### `storage.UpdateIssue`

Emitted when an issue is updated.

| Attribute | Type | Description |
|---|---|---|
| `db.operation` | string | `"UpdateIssue"` |
| `bd.issue.id` | string | Issue ID being updated |
| `bd.update.count` | int | Number of fields being updated |
| `bd.actor` | string | Actor updating the issue |

### `storage.GetIssue`

Emitted when an issue is retrieved.

| Attribute | Type | Description |
|---|---|---|
| `db.operation` | string | `"GetIssue"` |
| `bd.issue.id` | string | Issue ID being retrieved |

### `storage.SearchIssues`

Emitted when searching for issues.

| Attribute | Type | Description |
|---|---|---|
| `db.operation` | string | `"SearchIssues"` |
| `bd.result.count` | int | Number of results returned |

The issue search text is omitted from spans and operation metrics.

### `storage.GetReadyWork`

Emitted when querying for ready work.

| Attribute | Type | Description |
|---|---|---|
| `db.operation` | string | `"GetReadyWork"` |
| `bd.result.count` | int | Number of ready issues returned |

### `storage.GetBlockedIssues`

Emitted when querying for blocked issues.

| Attribute | Type | Description |
|---|---|---|
| `db.operation` | string | `"GetBlockedIssues"` |
| `bd.result.count` | int | Number of blocked issues returned |

### `storage.RunInTransaction`

Emitted when executing a transaction.

| Attribute | Type | Description |
|---|---|---|
| `db.operation` | string | `"RunInTransaction"` |

### Naming rule and complete inventory

Every storage span is named `storage.` + the operation name, and carries that
same bare name in the `db.operation` attribute
(`internal/telemetry/storage.go`, `InstrumentedStorage.op`). The sections above
detail the highest-traffic spans; the rest follow the identical shape, so they
are inventoried here rather than repeated.

There are two families.

**Direct storage operations** — `storage.<Method>`, one per storage method
(72 spans):

```
AddDependency AddIssueComment AddLabel CloseIssue CloseIssueChecked
CountDependencies CountDependents CountEvents CountIssueComments CountIssues
CountIssuesByGroup CreateIssue CreateIssues DeleteIssue GetAllConfig
GetAllEventsSince GetBlockedIssues GetConfig GetDependencies
GetDependenciesWithMetadata GetDependencyTree GetDependents
GetDependentsWithMetadata GetEpicsEligibleForClosure GetEvents GetIssue
GetIssueByExternalRef GetIssueComments GetIssueCommentsPage GetIssuesByIDs
GetIssuesByLabel GetLabels GetLocalMetadata GetProvenanceByRef
GetProvenanceEvents GetReadyWork GetReadyWorkWithCounts GetStatistics
IterAllEventsSince IterBlockedIssues IterDependenciesWithMetadata
IterDependentsWithMetadata IterEvents IterIssueComments IterIssues
IterReadyWork IterWisps ListWisps MergeMetadata MergeSlotAcquire
MergeSlotCheck MergeSlotCreate MergeSlotRelease RecordProvenanceEvent
RemoveDependency RemoveLabel ReopenIssue RunInIssueLifecycleTransaction
RunInTransaction SearchIssueIDs SearchIssues SearchIssuesWithCounts SetConfig
SetLocalMetadata SlotClear SlotGet SlotSet UnclaimIssue UnclaimIssueIfAssignee
UpdateIssue UpdateIssueChecked UpdateIssueType
```

**Issueops role operations** — `storage.<Role>.<Method>`, emitted when a caller
goes through the guarded issueops surface instead of calling storage directly
(39 spans):

```
BatchCloser.CloseBatch BatchCreator.CreateBatch
BlockingAnnotator.AnnotateBlocking Bootstrapper.Bootstrap Commenter.AddComment
Counter.Count Counter.CountByGroup CycleDetector.DetectCycles Deleter.Delete
DependencyEditor.AddDependencies DependencyEditor.RemoveDependency
EdgeReader.ReadEdges InitVerifier.VerifyIdentity IssueClaimer.Claim
IssueOperations.Close IssueOperations.Create IssueOperations.Reopen
IssueOperations.Update IssueReader.Get IssueReader.List IssueReader.Ready
IssueRelations.Related Memories.Forget Memories.List Memories.Recall
Memories.Remember Querier.Query ReadyClaimer.ClaimNext ReadyCounter.CountReady
StatsReporter.AssigneeStats StatsReporter.Stats Sweeper.Sweep
TreeWalker.WalkTree VersionReconciler.ReconcileVersion
VersionReconciler.RecordedVersion WorkspaceConfig.GetSetting
WorkspaceConfig.ListSettings WorkspaceConfig.SetSetting
WorkspaceConfig.UnsetSetting
```

The two families do **not** double-count. The role decorators wrap the
*uninstrumented* store (`InstrumentedStorage.IssueLifecycle` calls `Unwrap()`
before wrapping), so one logical operation through the issueops front door emits
its role span only — not a second `storage.<Method>` span beneath it. A create
via issueops is one `storage.IssueOperations.Create`; a create via direct
storage is one `storage.CreateIssue`. Counters and duration histograms are
therefore comparable across both paths.

---

## 4. Dolt Backend Events

**Shared attributes**: every `dolt.*` SQL span carries the fixed attribute set from `doltSpanAttrs()` — `db.system` (string, `"dolt"`), `db.readonly` (bool, whether the store is read-only), and `db.server_mode` (bool, currently always `true`). The tables below list only each span's additional attributes.

The privacy rules for `bd.args`, storage operation dimensions, and hook output
apply to those surfaces. Generic errors can still include text on lower-level
spans, including Dolt's `endSpan` and AI compaction errors. Dolt SQL spans can
also carry a truncated statement. Treat console traces as diagnostic data.

### `dolt.query`

Emitted for each SQL read query via `queryContext()`.

| Attribute | Type | Description |
|---|---|---|
| `db.operation` | string | `"query"` |
| `db.statement` | string | SQL statement (up to 300 bytes before an ellipsis) |

### `dolt.exec`

Emitted for each SQL write statement via `execContext()`.

| Attribute | Type | Description |
|---|---|---|
| `db.operation` | string | `"exec"` |
| `db.statement` | string | SQL statement (up to 300 bytes before an ellipsis) |

### `dolt.query_row`

Emitted for single-row queries via `queryRowContext()`.

| Attribute | Type | Description |
|---|---|---|
| `db.operation` | string | `"query_row"` |
| `db.statement` | string | SQL statement (up to 300 bytes before an ellipsis) |

### `dolt.commit`

Emitted for DOLT_COMMIT operations with the shared `doltSpanAttrs()`. The Dolt
commit span and the storage transaction decorators omit commit messages from
their structured attributes.

### `dolt.push`

Emitted for DOLT_PUSH operations. The span name is `dolt.push`, or `dolt.force_push` for force pushes.

| Attribute | Type | Description |
|---|---|---|
| `dolt.remote` | string | Remote being pushed to |
| `dolt.branch` | string | Branch being pushed |

### `dolt.pull`

Emitted for DOLT_PULL operations.

| Attribute | Type | Description |
|---|---|---|
| `dolt.remote` | string | Remote being pulled from |
| `dolt.branch` | string | Branch being pulled |

### `dolt.merge`

Emitted for DOLT_MERGE operations.

| Attribute | Type | Description |
|---|---|---|
| `dolt.merge_branch` | string | Branch being merged |
| `dolt.conflicts` | int | Conflict count (set when conflicts are detected) |

### `dolt.merge_with_strategy`

Emitted for `bd vc merge --strategy` merges (pinned-connection merge/resolve/commit sequence).

| Attribute | Type | Description |
|---|---|---|
| `dolt.merge_branch` | string | Branch being merged |
| `dolt.merge_strategy` | string | Conflict-resolution strategy |

### `dolt.branch`

Emitted for DOLT_BRANCH operations.

| Attribute | Type | Description |
|---|---|---|
| `dolt.branch` | string | Branch name |

### `dolt.checkout`

Emitted for DOLT_CHECKOUT operations.

| Attribute | Type | Description |
|---|---|---|
| `dolt.branch` | string | Branch being checked out |

---

## 5. Dolt Server Events (Roadmap — not yet implemented)

`internal/doltserver/` has no OTel imports. The events below are planned for Tier 1.

### `doltserver.start` *(planned)*

| Attribute | Type | Description |
|---|---|---|
| `port` | int | Port server is listening on |
| `data_dir` | string | Path to Dolt data directory |
| `pid` | int | Process ID of server |

### `doltserver.stop` *(planned)*

| Attribute | Type | Description |
|---|---|---|
| `pid` | int | Process ID of stopped server |
| `reason` | string | Stop reason (`graceful`, `forced`, `idle_timeout`, `crash`) |

---

## 6. Hooks Events

### `hook.exec`

Emitted for hook execution. **Span only** — no metric counters or histograms exist for hooks. Duration aggregation is a Tier 3 roadmap item.

| Attribute | Type | Description |
|---|---|---|
| `hook.event` | string | Event type (`create`, `update`, `close`, `delete`, etc.) |
| `hook.path` | string | Absolute path to hook script |
| `bd.issue_id` | string | ID of triggering issue |

For each nonempty stream, a `hook.stdout` or `hook.stderr` span event carries
only the integer `bytes` count. Output text is omitted.

---

## 7. AI Events

Emitted by the compaction engine (`bd compact`) via `internal/compact/haiku.go`, and by duplicate detection (`bd find-duplicates --method ai`) via `cmd/bd/find_duplicates.go`. Both use the Anthropic SDK with Anthropic-compatible credentials from `ANTHROPIC_API_KEY`, `MINIMAX_API_KEY`, or `ai.api_key`.

> **Note**: Only `compact/haiku.go` records to the `bd.ai.*` OTel metric instruments. `find_duplicates.go` records token counts and duration as span attributes only.

### `anthropic.messages.new`

One span per compaction request or duplicate-analysis batch. Compaction can
make multiple attempts within its span. `bd.ai.operation` identifies the caller.

| Attribute | Type | Description |
|---|---|---|
| `bd.ai.model` | string | Model used (e.g. `"claude-haiku-4-5"`) |
| `bd.ai.operation` | string | `"compact"` or `"find_duplicates"` |
| `bd.ai.input_tokens` | int | Input tokens consumed |
| `bd.ai.output_tokens` | int | Output tokens generated |
| `bd.ai.attempts` | int | Number of attempts, including retries (`compact` only) |
| `bd.ai.batch_size` | int | Candidate pairs evaluated (`find_duplicates` only) |
| `bd.ai.duration_ms` | float | Fractional milliseconds (`find_duplicates` only). Compaction records `bd.ai.request.duration` on successful API responses |

**Retry policy** (compact only; `find_duplicates` makes a single unretried call): exponential backoff, up to 4 attempts (1 initial + up to 3 retries, `maxRetries = 3`), on HTTP 429, 5xx, and network timeout errors.

---

## 8. Metrics Reference

Storage operation timings, Dolt connection acquisition and pool wait timings,
and the AI request histogram preserve fractional milliseconds.

| Metric (code name) | Type | Labels | Status |
|--------|------|--------|--------|
| `bd.storage.operations` | Counter | `db.operation` | ✅ Implemented |
| `bd.storage.operation.duration` | Histogram (ms) | `db.operation` | ✅ Implemented |
| `bd.storage.errors` | Counter | `db.operation` | ✅ Implemented |
| `bd.issue.count` | Gauge | `status` | ✅ Implemented |
| `bd.db.retry_count` | Counter | — | ✅ Implemented |
| `bd.db.lock_wait_ms` | Histogram | — | 🔲 Registered, not recorded |
| `bd.db.circuit_trips` | Counter | — | ✅ Implemented |
| `bd.db.circuit_rejected` | Counter | — | ✅ Implemented |
| `bd.db.serialization_errors` | Counter | — | ✅ Implemented |
| `bd.write_retries_total` | Counter | `type` (`serialization` \| `connection`) | ✅ Implemented |
| `bd.db.conn_acquire_ms` | Histogram | — | ✅ Implemented |
| `bd.db.pool_wait_count` | Counter | — | ✅ Implemented |
| `bd.db.pool_wait_ms` | Histogram | — | ✅ Implemented |
| `bd.db.pool_open`, `bd.db.pool_in_use`, `bd.db.pool_idle`, `bd.db.pool_max_open` | Observable gauges | — | ✅ Implemented |
| `bd.claim_verify_lost_total` | Counter | `op` (`claim` \| `unclaim` \| `guarded-update` \| `ready-claim`) | ✅ Implemented |
| `bd.claim_verify_recovered_total` | Counter | `op`, `outcome` | 🔲 Registered, not recorded |
| `bd.ai.input_tokens` | Counter | `bd.ai.model` | ✅ Implemented (compact only) |
| `bd.ai.output_tokens` | Counter | `bd.ai.model` | ✅ Implemented (compact only) |
| `bd.ai.request.duration` | Histogram (ms) | `bd.ai.model` | ✅ Implemented (compact only) |

Pool gauges sum open SQL stores in this process. With none open, they report
close-time snapshots since the last open so the final export of a short-lived
command can still report its pool statistics.

---

## 9. Recommended Indexed Attributes

```
service.name, service.version, host.name, bd.command, bd.version, db.operation,
bd.issue.type, hook.event, bd.ai.model, bd.ai.operation
```

---

## 10. Configuration and Backend

Environment variables, backend compatibility, Dolt system tables, and roadmap are documented in [otel-architecture.md](otel-architecture.md) to avoid duplication.

The exporter settings are `OTEL_TRACES_EXPORTER=console`,
`OTEL_METRICS_EXPORTER` (`otlp`, `console`, or a comma-separated list), and
`OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` or `OTEL_EXPORTER_OTLP_ENDPOINT`.
`OTEL_METRICS_EXPORTER=none` disables metric export. The deprecated
`BD_OTEL_METRICS_URL`, `BD_OTEL_LOGS_URL`, and `BD_OTEL_STDOUT` selectors are
translated with a stderr warning. `BD_OTEL_STDOUT=true` selects both console
exporters. The logs endpoint translation provides compatibility only.

---

## Appendix: Source Reference Audit

| Surface | Source |
|---|---|
| Opt-in, default resource, exporter selection and fractional milliseconds | `internal/telemetry/telemetry.go`: `Enabled`, `buildResource`, `buildTraceProvider`, `buildMetricProvider`, `Milliseconds` |
| `bd.command.<name>`, `bd.command`, `bd.version`, `bd.args` | `cmd/bd/command_telemetry.go`: `startCommandSpan`, `commandSpanAttrs`, `commandTelemetryArgs` |
| Resolved `bd.actor` on the command span | `cmd/bd/main.go`: `commandSpan.SetAttributes` after actor resolution |
| Storage spans, operation metrics and issue status gauge | `internal/telemetry/storage.go`: `WrapStorage`, `InstrumentedStorage.op`, `done`, `GetStatistics` |
| Search result count and storage transaction attribute sets | `internal/telemetry/storage.go`: `SearchIssues`, `RunInTransaction`, `RunInIssueLifecycleTransaction` |
| Dolt counters, pool gauges, and connection durations | `internal/storage/dolt/store.go`: `init`, `poolSet.register`, `poolSet.stats`. See `connAcquireMs` and `poolWaitMs` in `internal/storage/dolt/transaction.go` |
| Dolt query and execution spans, SQL truncation and error text | `internal/storage/dolt/store.go`: `queryContext`, `queryRowContext`, `execContext`, `spanSQL`, `endSpan` |
| Dolt commit, push, pull, branch, checkout and merge spans | `internal/storage/dolt/store.go`: `commitWorkingSet`, `pushToRemote`, `pullFromRemoteUnchecked`, `Branch`, `Checkout`, `Merge`, `MergeWithStrategy` |
| `hook.exec` and the `hook.stdout` and `hook.stderr` byte counts | `internal/hooks/hooks_unix.go`, `hooks_windows.go`, and `hooks_otel.go` (`addHookOutputEvents`) |
| Compaction spans, token counters and AI request histogram | `internal/compact/haiku.go`: `initAIMetrics`, `callWithRetry` |
| Duplicate-analysis span and `bd.ai.duration_ms` | `cmd/bd/find_duplicates.go`: `analyzeWithAI` |
