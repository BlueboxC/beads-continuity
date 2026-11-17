# OpenTelemetry Architecture

## Overview

Beads uses OpenTelemetry (OTel) for structured observability of storage operations, CLI commands, and Dolt version control. Opt-in metrics can use OTLP HTTP. Traces export to the local console on stderr.

OTLP metrics can go to a compatible backend such as VictoriaMetrics. Beads leaves endpoint selection to the operator.

**Best-effort design**: Telemetry initialization errors are returned but do not affect normal `bd` operation. The system remains functional even when telemetry is unavailable.

---

## Implementation Status

### Core Telemetry (Implemented ✅)

| Feature | Status | Notes |
|---------|--------|-------|
| Core OTel initialization | ✅ Implemented | `telemetry.Init()`, providers setup |
| Metrics export (counters) | ✅ Implemented | Storage operations, Dolt operations |
| Metrics export (histograms) | ✅ Implemented | Storage operation durations in fractional milliseconds |
| Console traces | ✅ Implemented | `OTEL_TRACES_EXPORTER=console` writes traces to stderr |
| Storage layer instrumentation | ✅ Implemented | Direct storage and issueops role decorators |
| Command lifecycle tracing | ✅ Implemented | Per-command spans with the parsed command path and changed flag names |
| Dolt version control tracing | ✅ Implemented | Commit, push, pull, merge operations |

### Dolt Backend Telemetry (Implemented ✅)

| Feature | Status | Notes |
|---------|--------|-------|
| SQL query tracing | ✅ Implemented | All Dolt queries wrapped with spans |
| Dolt lock wait timing | ✅ Defined | `bd.db.lock_wait_ms` histogram registered; `.Record()` not yet called |
| Dolt retry counting | ✅ Implemented | `bd.db.retry_count` counter |
| Dolt circuit breaker | ✅ Implemented | `bd.db.circuit_trips`, `bd.db.circuit_rejected` counters |
| Auto-commit tracking | ✅ Implemented | Per-command auto-commit events |
| Working set flush tracking | ✅ Implemented | Flush on shutdown/signal |

### Server Lifecycle Telemetry (Not yet instrumented ❌)

`internal/doltserver/` has no OTel imports. Server lifecycle spans and metrics are roadmap items (see Tier 1 below).

---

## Roadmap

The names, dimensions, and polling intervals below are design proposals. See Implementation Status and the data model for emitted signals.

### Tier 1 — High value, moderate effort

#### Tracker integrations (`internal/linear/`, `internal/jira/`, `internal/gitlab/`)

External API calls are currently a black box. No visibility into latency, rate-limiting, or sync volume.

New metrics:
- `bd_tracker_api_calls_total` (Counter) — by `tracker`, `method`, `status`
- `bd_tracker_api_latency_ms` (Histogram) — by `tracker`, `method`
- `bd_tracker_errors_total` (Counter) — by `tracker`, `error_type`
- `bd_tracker_issues_synced_total` (Counter) — by `tracker`, `direction`

New spans: `tracker.<name>.pull_issues`, `tracker.<name>.push_issue`, `tracker.<name>.resolve_state`

#### Git operations (`internal/git/`)

Git push/pull can dominate wall-clock time but is currently invisible.

New metrics:
- `bd_git_operation_duration_ms` (Histogram) — by `operation`, `status`
- `bd_git_errors_total` (Counter) — by `operation`, `error_type`

New spans: `git.clone`, `git.pull`, `git.push`, `git.commit`, `git.merge`

#### Dolt server lifecycle (`internal/doltserver/`)

Server crashes and restarts are silent. No alerting possible.

New metrics:
- `bd_doltserver_status` (Gauge, 1=running/0=stopped)
- `bd_doltserver_startup_ms` (Histogram)
- `bd_doltserver_restarts_total` (Counter)
- `bd_doltserver_errors_total` (Counter) — by `error_type`

New spans: `doltserver.start`, `doltserver.stop`

---

### Tier 2 — Medium value, low effort

#### Query engine (`internal/query/`)

Distinguishes whether slowness is client-side (parsing/compilation) or DB-side.

New spans: `query.parse`, `query.compile`
New metrics: `bd_query_duration_ms` (Histogram), `bd_query_parse_errors_total` (Counter)

#### Validation engine (`internal/validation/`)

Data integrity errors are currently silent until they surface as user-visible failures.

New spans: `validation.check_dependencies`, `validation.check_schema`
New metrics: `bd_validation_errors_total` (Counter) — by `error_type`

#### Dolt version control (`internal/storage/dolt/versioned.go`)

`versioned.go` has no OTel imports yet. Future spans:
- `History`: Query complete version history for an issue
- `AsOf`: Query state at specific commit or branch
- `Diff`: Cell-level diff between two commits
- `ListBranches`: Enumerate all branches
- `GetCurrentCommit`: Get HEAD commit hash
- `GetConflicts`: Check for merge conflicts

#### Dolt system table polling

Periodic SQL queries against Dolt system tables to surface metrics unavailable via OTLP (Dolt has no native OTel export):

| Metric | Source | Frequency |
|--------|--------|-----------|
| `bd_dolt_commits_per_hour` | `dolt_log` GROUP BY hour | 5 min |
| `bd_dolt_working_set_size` | `dolt_status` COUNT(*) | 1 min |
| `bd_dolt_branch_count` | `dolt_branches` COUNT(*) | 5 min |
| `bd_dolt_conflict_count` | `dolt_conflicts` COUNT(*) | 5 min |

---

### Tier 3 — Low priority / future

- **Command-level sub-spans**: Instrument validation vs. DB vs. render breakdown per command (`bd create`, `bd list`, `bd compact`, etc.)
- **Molecules & recipes**: `molecule.create`, `recipe.execute` spans
- **Hook duration metrics**: Currently only spans (`hook.exec`), no histogram for aggregation
- **OTel test suite**: Expand integration coverage for the proposed signals as they are implemented
- **Lock wait recording**: `bd.db.lock_wait_ms` histogram is registered but `.Record()` is not yet called

---

## Components

### 1. Initialization (`internal/telemetry/telemetry.go`)

The `telemetry.Init()` function sets up OTel providers on process startup and returns only an `error`:

```go
if err := telemetry.Init(ctx, "bd", version); err != nil {
    // Log and continue — telemetry is best-effort
}
defer telemetry.Shutdown(ctx)
```

Set `BD_OTEL_ENABLED=true` or use a supported legacy `BD_OTEL_*` selector to
opt in. `OTEL_SDK_DISABLED=true` disables telemetry. With opt-in active,
`OTEL_TRACES_EXPORTER=console` writes traces to stderr. Metrics use stderr
with the `console` selector and OTLP HTTP with the `otlp` selector and a
configured metrics or shared OTLP endpoint. Both outputs can be selected for
metrics. The console exporters leave stdout available for the command's own
output, including JSON. Beads has no remote trace or log exporter.

**OTLP Compatibility**:
- Uses standard OpenTelemetry Protocol (OTLP) over HTTP
- Protobuf encoding (VictoriaMetrics, Prometheus, and others accept this)
- Compatible with any backend that supports OTLP v1.x+

The resource starts with `service.name`, `service.version`, `host.name`, process
ID, executable name and runtime attributes. Default detection omits raw argv,
executable paths, and account owners. `OTEL_SERVICE_NAME` and
`OTEL_RESOURCE_ATTRIBUTES` are operator-controlled resource overrides.

`bd.command`, `bd.version`, and `bd.args` belong to the command span. `bd.args`
contains the path parsed by Cobra below `bd` and the sorted long names of flags
explicitly set for that command. Flag values and positional arguments are omitted.
`bd.actor` is attached to the command span after actor resolution.

---

### 2. Storage Instrumentation (`internal/telemetry/storage.go`)

`InstrumentedStorage` wraps `storage.DoltStorage` with operation spans,
counters, and duration histograms. Operation counts, durations and errors use
`db.operation` as their sole metric dimension. Span attributes such as result
counts stay on spans. Issue search text and transaction commit messages are
omitted. Failed decorator operations set a span error status without error text.

```go
store = telemetry.WrapStorage(store) // Returns the original store when disabled.
```

**Metric names in code** (OTel SDK notation with dots):
- `bd.storage.operations` → exported as `bd_storage_operations_total` by Prometheus/VM
- `bd.storage.operation.duration` → `bd_storage_operation_duration_ms`
- `bd.storage.errors` → `bd_storage_errors_total`
- `bd.issue.count` → `bd_issue_count`

**Instrumented Storage Operations:**
- Issue CRUD: `CreateIssue`, `GetIssue`, `UpdateIssue`, `CloseIssue`, `DeleteIssue`
- Dependencies: `AddDependency`, `RemoveDependency`, `GetDependencies`
- Labels: `AddLabel`, `RemoveLabel`, `GetLabels`
- Queries: `SearchIssues`, `GetReadyWork`, `GetBlockedIssues`
- Statistics: `GetStatistics` (also emits gauge of issue counts by status)
- Transactions: `RunInTransaction`

---

### 3. Dolt Backend Telemetry (`internal/storage/dolt/store.go`)

Dolt storage layer emits metrics for:
- `bd.db.retry_count`: SQL retries in server mode (recorded in `withRetry` when `attempts > 1`)
- `bd.db.lock_wait_ms`: Histogram registered but `.Record()` not yet called (stub)
- `bd.db.circuit_trips`: Circuit breaker trips to open state (recorded in `withRetry`)
- `bd.db.circuit_rejected`: Requests rejected by open circuit breaker (fail-fast path)
- SQL query spans via `queryContext()`, `execContext()`, `queryRowContext()` wrappers using `doltTracer`
- Dolt version control spans: `dolt.commit`, `dolt.push`, `dolt.pull`, `dolt.merge`, `dolt.branch`, `dolt.checkout`
- Pool gauges aggregate the currently open SQL stores. A final collection with no open store can report the retained close-time statistics.

Lower-level Dolt spans retain truncated `db.statement` and can record generic
error text. AI compaction spans can also record generic error text. The
command-argument, storage-dimension, and hook-output rules apply to their
respective attributes and events.

**SQL Span pattern (`queryContext`):**
```go
func (s *DoltStore) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
    ctx, span := doltTracer.Start(ctx, "dolt.query",
        trace.WithSpanKind(trace.SpanKindClient),
        trace.WithAttributes(append(s.doltSpanAttrs(),
            attribute.String("db.operation", "query"),
            attribute.String("db.statement", spanSQL(query)),
        )...),
    )
    var rows *sql.Rows
    err := s.withRetry(ctx, func() error {
        rows, queryErr = s.db.QueryContext(ctx, query, args...)
        return queryErr
    })
    endSpan(span, wrapLockError(err))
    return rows, err
}
```

---

### 4. Dolt Version Control Telemetry (`internal/storage/dolt/store.go`)

Version control operations emit spans directly in `store.go` via `doltTracer.Start()`. These are **not** in `versioned.go` (which has no OTel imports).

Implemented spans (see the source reference below):
- `dolt.commit` — `CALL DOLT_COMMIT`
- `dolt.push` — `CALL DOLT_PUSH`
- `dolt.pull` — `CALL DOLT_PULL`
- `dolt.merge` — `CALL DOLT_MERGE`
- `dolt.branch` — `CALL DOLT_BRANCH`
- `dolt.checkout` — `CALL DOLT_CHECKOUT`

---

### 5. Hook Telemetry (`internal/hooks/`)

Hooks emit a `hook.exec` span. A `hook.stdout` or `hook.stderr` event for a
nonempty stream carries only its `bytes` count. Hook counters and histograms
remain roadmap items.

---

## Metric Naming Convention

OTel SDK uses dot-notation internally. Prometheus-compatible backends (VictoriaMetrics, Prometheus) export these as underscore-separated names with type suffixes:

| Code name | Exported name |
|-----------|---------------|
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

## Environment Variables

### Beads-Level Variables

| Variable | Set by | Description |
|-----------|----------|-------------|
| `BD_OTEL_ENABLED` | Operator | Set `true` to opt in |
| `BD_OTEL_METRICS_URL` | Operator | Deprecated opt-in and metrics endpoint override |
| `BD_OTEL_LOGS_URL` | Operator | Deprecated opt-in and logs endpoint translation. Compatibility setting for a future log exporter |
| `BD_OTEL_STDOUT` | Operator | Deprecated opt-in that selects console traces and metrics on stderr |

### Context Variables

| Variable | Source | Used By |
|-----------|--------|----------|
| `BEADS_ACTOR` | Git config / env var | Actor identity for audit trails (BD_ACTOR still works as deprecated alias) |
| `BD_NAME` | Environment | Binary name override (for multi-instance setups) |
| `OTEL_RESOURCE_ATTRIBUTES`, `OTEL_SERVICE_NAME` | Operator | Resource overrides for exported spans and metrics |

### Dolt-Specific Variables (See DOLT.md)

| Variable | Purpose |
|-----------|----------|
| `BEADS_DOLT_PASSWORD` | Server mode password |
| `BEADS_DOLT_SERVER_MODE` | Enable server mode |
| `BEADS_DOLT_SERVER_HOST` | Server host (default: 127.0.0.1) |
| `BEADS_DOLT_SERVER_PORT` | Server port (default: 3307, 3308 in shared mode, or derived) |
| `BEADS_DOLT_SERVER_TLS` | Enable TLS for server connections |
| `BEADS_DOLT_SERVER_USER` | MySQL connection user |
| `DOLT_REMOTE_USER` | Push/pull auth user |
| `DOLT_REMOTE_PASSWORD` | Push/pull auth password |

> **Note**: Dolt-specific configuration variables are documented in [DOLT.md](../../../docs/architecture/dolt.md) and are out of scope for OTEL design documentation.

---

## Event Types

### CLI Command Events

| Event | Trigger | Key Attributes |
|-------|---------|----------------|
| `bd.command.<name>` | Each `bd` subcommand execution | `bd.command`, `bd.version`, `bd.args`, `bd.actor` |

### Storage Events

| Event | Trigger | Key Attributes |
|-------|---------|----------------|
| `storage.CreateIssue` | Issue creation | `bd.issue.type`, `bd.actor` |
| `storage.UpdateIssue` | Issue update | `bd.issue.id`, `bd.update.count`, `bd.actor` |
| `storage.GetIssue` | Issue lookup | `bd.issue.id` |
| `storage.SearchIssues` | Issue search | `db.operation`, `bd.result.count` |
| `storage.GetReadyWork` | Ready work query | `bd.result.count` |
| `storage.GetBlockedIssues` | Blocked issues query | `bd.result.count` |
| `storage.RunInTransaction` | Transaction execution | `db.operation` |

### Dolt Events

| Event | Trigger | Key Attributes |
|-------|---------|----------------|
| `dolt.query` | Each SQL query (`queryContext`) | `db.operation`, `db.statement` |
| `dolt.exec` | Each SQL write (`execContext`) | `db.operation`, `db.statement` |
| `dolt.query_row` | Single-row queries (`queryRowContext`) | `db.operation`, `db.statement` |
| `dolt.commit` | DOLT_COMMIT operation | `db.system`, `db.readonly`, `db.server_mode` |
| `dolt.push` | DOLT_PUSH operation | `dolt.branch` |
| `dolt.pull` | DOLT_PULL operation | `dolt.branch` |
| `dolt.merge` | DOLT_MERGE operation | `dolt.merge_branch` |
| `dolt.branch` | DOLT_BRANCH operation | `dolt.branch` |
| `dolt.checkout` | DOLT_CHECKOUT operation | `dolt.branch` |

### Hooks Events

| Event | Trigger | Key Attributes |
|-------|---------|----------------|
| `hook.exec` | Hook execution (span only — no metric counters) | `hook.event`, `hook.path`, `bd.issue_id` |
| `hook.stdout`, `hook.stderr` | Nonempty output stream (span event) | `bytes` |

---

## Monitoring Gaps

### Currently Monitored ✅

| Area | Coverage |
|-------|----------|
| Storage operations | Full (all CRUD, queries, transactions) |
| CLI command lifecycle | Command spans with parsed command path and changed flag names |
| Dolt SQL queries | Full (all queries via queryContext/execContext wrappers) |
| Dolt retry counting | Full (retry counter incremented in withRetry) |
| Dolt version control | Full (commit, push, pull, merge, branch, checkout) |
| AI compaction | Full (bd.ai.* metrics in compact/haiku.go) |
| Connection pool utilization | Open, in-use, idle, and max-open gauges for Beads SQL stores |

### Not Currently Monitored ❌

| Area | Notes | Operational Impact |
|-------|-------|-------------------|
| **Dolt lock wait time** | `bd.db.lock_wait_ms` registered but `.Record()` not called | Lock contention invisible |
| **Dolt server lifecycle** | `internal/doltserver/` has no OTel imports | Server crashes are silent |
| **Hook execution time** | `hook.exec` span exists but no duration histogram | Cannot detect hook regressions |
| **versioned.go operations** | `versioned.go` has no OTel imports | History/AsOf/Diff invisible |
| **Dolt server metrics** | Dolt server-wide metrics are outside Beads' OTel instrumentation | Beads pool gauges cover this process |
| **Working set size** | Uncommitted changes count unknown | Cannot detect batch mode accumulation |
| **Database size growth** | Dolt database size not tracked | Cannot plan capacity or detect bloat |
| **Branch proliferation** | Branch count not exposed | Cannot detect cleanup needed |
| **Remote sync bandwidth** | Bytes transferred not tracked | Cannot monitor network usage or cost |
| **Query execution plans** | EXPLAIN ANALYZE not captured | Cannot identify slow queries |

---

## Queries

### Metrics (Prometheus-style names and labels)

**Total counts by operation:**
```promql
sum by (db_operation) (rate(bd_storage_operations_total[5m]))
sum(rate(bd_db_retry_count_total[5m]))
```

**Latency distributions:**
```promql
histogram_quantile(0.50, sum by (db_operation, le) (rate(bd_storage_operation_duration_ms_bucket[5m])))
histogram_quantile(0.95, sum by (db_operation, le) (rate(bd_storage_operation_duration_ms_bucket[5m])))
histogram_quantile(0.99, sum by (db_operation, le) (rate(bd_storage_operation_duration_ms_bucket[5m])))
```

**Issue counts by status:**
```promql
bd_issue_count{status="open"}
bd_issue_count{status="in_progress"}
bd_issue_count{status="closed"}
bd_issue_count{status="deferred"}
```

---

## Dolt Telemetry Capabilities

### Dolt Internal Metrics

**Important**: Dolt does not provide native OpenTelemetry export. The documentation search confirms there is no Dolt configuration variable or feature to enable OTLP export.

Dolt exposes internal metrics only via:
- `performance_schema` tables (MySQL standard, accessible via SQL queries)
- System tables (`dolt_log`, `dolt_status`, `dolt_diff`, `dolt_branches`, `dolt_conflicts`)

**Beads implementation**:
Beads currently queries Dolt metrics via direct SQL (see `cmd/bd/doctor/perf_dolt.go`) rather than via OTLP. This is intentional — Dolt lacks native OTel support.

### Dolt System Tables for Telemetry

| Table | Purpose |
|--------|-----------|
| `dolt_log` | Commit history (queryable for audit) |
| `dolt_status` | Working set state (uncommitted changes) |
| `dolt_diff` | Cell-level diff between commits |
| `dolt_branches` | Branch metadata |
| `dolt_conflicts` | Merge conflicts (when present) |

### Sample Queries for Dolt Telemetry

**Commit frequency analysis:**
```sql
SELECT
    DATE_FORMAT(commit_date, '%Y-%m') as month,
    COUNT(*) as commits
FROM dolt_log
GROUP BY month
ORDER BY month DESC;
```

**Working set size tracking:**
```sql
SELECT
    COUNT(*) as staged_changes,
    SUM(CASE WHEN staged = 1 THEN 1 ELSE 0 END) as added,
    SUM(CASE WHEN staged = 0 THEN 1 ELSE 0 END) as removed
FROM dolt_status;
```

**Branch proliferation detection:**
```sql
SELECT
    COUNT(*) as branch_count,
    MIN(commit_date) as oldest,
    MAX(commit_date) as newest
FROM dolt_branches;
```

**Conflict analysis:**
```sql
SELECT
    COUNT(*) as conflict_count,
    COUNT(DISTINCT table_name) as tables_affected
FROM dolt_conflicts;
```

---

## Related Documentation

- [OTel Data Model](otel-data-model.md) — Emitted attributes and metric dimensions
- [OBSERVABILITY.md](../../../docs/reference/observability.md) — Quick reference for metrics
- [Dolt Backend](../../../docs/architecture/dolt.md) — Dolt configuration and usage
- [Dolt Concurrency](../dolt-concurrency.md) — Concurrency model and transactions

## Backends Compatible with OTLP

| Backend | Notes |
|---------|-------|
| **VictoriaMetrics** | Metrics backend. Set its OTLP metrics endpoint explicitly |
| **Prometheus** | Supports OTLP via remote_write receiver — open source |
| **Grafana Mimir** | Supports OTLP via write endpoint — open source |
| **Loki** | Requires OTLP bridge (Loki uses different format) — open source |
| **OpenTelemetry Collector** | Universal forwarder to any backend (recommended for production) — open source |

**Production Recommendation**: For production deployments, consider using **OpenTelemetry Collector** as a sidecar. The Collector provides:
- Single agent for all telemetry
- Advanced processing and batching
- Support for multiple backends simultaneously
- Better resource efficiency than per-process exporters

---

## Appendix: Source Reference Audit

The [data model](otel-data-model.md#appendix-source-reference-audit) links emitted
signals to their implementation. The lifecycle entry points are listed below.

| Behavior | Source |
|---|---|
| Providers, opt-in and legacy mapping | `internal/telemetry/telemetry.go`: `Init`, `Enabled`, `translateLegacyEnv`, `selectedMetricExporters` |
| Resource metadata and console writers | `internal/telemetry/telemetry.go`: `buildResource`, `buildTraceProvider`, `buildMetricProvider` |
| Command context and actor resolution | `cmd/bd/command_telemetry.go`: `startCommandSpan`, `commandTelemetryArgs`. See `commandSpan.SetAttributes` in `cmd/bd/main.go` |
| End the command span and flush providers | `cmd/bd/main.go`: `commandSpan.End`, `telemetry.Shutdown` |
| Storage decorator, metric dimensions and error status | `internal/telemetry/storage.go`: `WrapStorage`, `InstrumentedStorage.op`, `done` |
| Dolt process pool registration and snapshots | `internal/storage/dolt/store.go`: `registerPoolGauges`, `poolSet.track`, `poolSet.stats` |
| Hook spans and byte-count events | `internal/hooks/hooks_unix.go` and `internal/hooks/hooks_windows.go`: `runHook`. See `addHookOutputEvents` in `internal/hooks/hooks_otel.go` |
| AI request spans and fractional timings | `internal/compact/haiku.go`: `callWithRetry`. See `analyzeWithAI` in `cmd/bd/find_duplicates.go` |
