---
title: Azure DevOps (ADO) Integration Configuration
description: Configuration reference for bd ado sync, which bidirectionally syncs beads issues with Azure DevOps work items
---

Last reviewed: 2026-11-17

Freshness source: `cmd/bd/ado*.go`, `internal/ado/`, `internal/config/yaml_config.go`, and `internal/tracker/`.

Review scope: source and test inspection.

This guide covers all configuration options for the `bd ado sync` command, which synchronizes beads issues with Azure DevOps work items.

**Proxied-server mode:** `bd ado sync`, `bd ado status`, and `bd ado projects`
are not supported when bd is connected to a proxied server (error:
`ado sync is not supported in proxied-server mode`); run them from a
workspace with direct database access.

## Quick Start

```bash
# Set required config
bd config set ado.pat "your-personal-access-token"
bd config set ado.org "your-organization"
bd config set ado.project "your-project"

# Or use environment variables
export AZURE_DEVOPS_PAT="your-personal-access-token"
export AZURE_DEVOPS_ORG="your-organization"
export AZURE_DEVOPS_PROJECT="your-project"

# Sync (bidirectional)
bd ado sync

# Pull only (import from ADO)
bd ado sync --pull-only

# Push only (export to ADO)
bd ado sync --push-only

# Preview without making changes
bd ado sync --dry-run
```

## Connection Configuration

| Config Key | Env Variable | Required | Description |
|---|---|---|---|
| `ado.pat` | `AZURE_DEVOPS_PAT` | Yes | Local Personal Access Token |
| `ado.org` | `AZURE_DEVOPS_ORG` | Conditional¹ | Organization name (e.g., `myorg`) |
| `ado.url` | `AZURE_DEVOPS_URL` | Conditional¹ | Locally configured base URL for Azure DevOps Server |
| `ado.project` | `AZURE_DEVOPS_PROJECT` | Conditional² | Single project name |
| `ado.projects` | `AZURE_DEVOPS_PROJECTS` | Conditional² | Comma-separated project names |

¹ Either `ado.org` or `ado.url` must be set. Use `ado.url` for on-premises Azure DevOps Server.

² At least one project must be configured via `ado.project` or `ado.projects`.

For organization and project settings, the integration reads the synced configuration before falling back to the listed environment variables.

Set `ado.pat` and `ado.url` in local configuration or their listed environment variables. The integration uses the effective local configuration first and ignores historical synced PAT values. A historical synced `ado.url` or a failed synced-endpoint lookup blocks connection when no local URL is selected. Set the intended URL locally before retrying.

### On-Premises ADO Server

For Azure DevOps Server, set a local HTTPS base URL with `ado.url` or `AZURE_DEVOPS_URL`:

```bash
bd config set ado.url "https://tfs.company.com/DefaultCollection"
bd config set ado.project "MyProject"
```

### Multi-Project Sync

Sync across multiple projects in a single command:

```bash
bd config set ado.projects "ProjectA,ProjectB,ProjectC"
```

The first project is used as the primary for URL construction. WIQL queries use `TeamProject IN (...)` for multi-project support.

## Filter Configuration

Filters control which ADO work items are included in sync operations.

| Config Key | CLI Flag | Description | Example |
|---|---|---|---|
| `ado.filter.area_path` | `--area-path` | Area path (uses UNDER) | `Project\Team` |
| `ado.filter.iteration_path` | `--iteration-path` | Sprint/iteration path | `Project\Sprint 1` |
| `ado.filter.types` | `--types` | Work item types (comma-separated) | `Bug,Task,User Story` |
| `ado.filter.states` | `--states` | ADO states (comma-separated) | `New,Active,Resolved` |

CLI flags override config values for that sync run.

**WIQL query example** (generated from filters):

```sql
SELECT [System.Id] FROM WorkItems WHERE
  [System.TeamProject] = 'MyProject'
  AND [System.IsDeleted] = false
  AND [System.AreaPath] UNDER 'Project\Team'
  AND [System.WorkItemType] IN ('Bug', 'Task')
  AND [System.State] IN ('New', 'Active')
  ORDER BY [System.ChangedDate] ASC
```

## Default Mappings

### Priority Mapping

Priority mapping is bidirectional but **lossy for P3/P4**:

| Beads Priority | ADO Priority | Direction | Notes |
|---|---|---|---|
| 0 (Critical) | 1 | ↔ | |
| 1 (High) | 2 | ↔ | |
| 2 (Medium) | 3 | ↔ | Default for unknown values |
| 3 (Low) | 4 | ↔ | |
| 4 (Backlog) | 4 | → | **Lossy**: becomes P3 on pull |

> **Note:** Beads P3 and P4 both map to ADO priority 4. On a fresh pull into an empty database, ADO 4 maps back to beads P3. The original priority is not preserved across a full round-trip for P4 issues.

For Bug-type work items, ADO also requires a Severity field:

| Beads Priority | ADO Severity |
|---|---|
| 0 | 1 - Critical |
| 1 | 2 - High |
| 2 | 3 - Medium |
| 3, 4 | 4 - Low |

### Status Mapping

| Beads Status | Default ADO State | Config Key |
|---|---|---|
| `open` | `New` | `ado.state_map.open` |
| `in_progress` | `Active` | `ado.state_map.in_progress` |
| `blocked` | `Active` + `beads:blocked` tag | `ado.state_map.blocked` |
| `deferred` | `Removed` | `ado.state_map.deferred` |
| `closed` | `Closed` | `ado.state_map.closed` |

**Blocked status:** ADO has no native blocked state. beads maps blocked to `Active` and adds a `beads:blocked` tag. On pull, `Active` + `beads:blocked` tag restores `StatusBlocked`.

Override defaults for your process template:

```bash
# Example: Scrum template
bd config set ado.state_map.open "To Do"
bd config set ado.state_map.in_progress "In Progress"
bd config set ado.state_map.closed "Done"
```

### Type Mapping

| Beads Type | Default ADO Type | Config Key |
|---|---|---|
| `bug` | `Bug` | `ado.type_map.bug` |
| `feature` | `User Story` | `ado.type_map.feature` |
| `task` | `Task` | `ado.type_map.task` |
| `epic` | `Epic` | `ado.type_map.epic` |
| `chore` | `Task` | `ado.type_map.chore` |

Reverse mapping (ADO → beads) also recognizes:
- `Product Backlog Item` → `feature` (Scrum template)
- `Issue` → `task`

Override for your process template:

```bash
# Example: Scrum template
bd config set ado.type_map.feature "Product Backlog Item"
```

## Process Template Configuration

ADO supports multiple process templates with different work item types and state transitions. The defaults assume the **Agile** template. Override mappings for other templates.

### Agile (Default)

No configuration needed. Default mappings work out of the box.

State transitions:
```
Bug:         New → Active → Resolved → Closed
Task:        New → Active → Closed
User Story:  New → Active → Resolved → Closed
Epic:        New → Active → Resolved → Closed
```

### Scrum

```bash
bd config set ado.type_map.feature "Product Backlog Item"
bd config set ado.state_map.open "New"
bd config set ado.state_map.in_progress "Committed"
bd config set ado.state_map.closed "Done"
```

State transitions:
```
Product Backlog Item: New → Approved → Committed → Done
Task:                 To Do → In Progress → Done
Bug:                  New → Approved → Committed → Done
```

### CMMI

```bash
bd config set ado.type_map.feature "Requirement"
bd config set ado.state_map.open "Proposed"
bd config set ado.state_map.in_progress "Active"
bd config set ado.state_map.closed "Closed"
```

State transitions:
```
Requirement: Proposed → Active → Resolved → Closed
Task:        Proposed → Active → Closed
Bug:         Proposed → Active → Resolved → Closed
```

### State Transition Handling

When creating a work item in a non-initial state, beads creates it in the initial state and then tries a direct transition to the target. If Azure DevOps returns HTTP 400, beads tries the built-in path for the work item type when one is available. For an Agile Bug, that path can be `New → Active → Resolved → Closed`. A custom state map changes the target state. A failed transition can leave the created work item in its initial or intermediate state.

## Sync Options

### Direction

| Flag | Description |
|---|---|
| (none) | Bidirectional: pull then push |
| `--pull-only` | Import from ADO only |
| `--push-only` | Export to ADO only |

### Incremental Pull

A bulk incremental pull starts five minutes before the saved sync timestamp to catch late results. Azure DevOps WIQL uses UTC dates for the changed-date filter, so a window that crosses midnight can return rows from the previous day. Conflict checks keep the original timestamp. Existing linked issues skip overlap-only rows that predate the five-minute window or are older than the local issue, unless conflict resolution or newly linked issue hydration permits an update.

### Conflict Resolution

When the same issue has been modified both locally and in ADO:

| Flag | Description |
|---|---|
| `--prefer-newer` | Most recently updated version wins (default) |
| `--prefer-local` | Local beads version always wins |
| `--prefer-ado` | ADO version always wins |

### Additional Flags

| Flag | Description |
|---|---|
| `--dry-run` | Preview sync without making changes |
| `--no-create` | Only update existing items, never create new ones |
| `--bootstrap-match` | Enable heuristic title matching for first sync |
| `--reconcile` | Force reconciliation scan for deleted items (see Reconciliation below) |
| `--issues` | Sync specific issues by bead ID or ADO work item ID |
| `--parent` | Push only a bead and its descendants (push mode only; mutually exclusive with `--issues`) |
| `--project` | Project name(s) for this run, repeatable — overrides `ado.project`/`ado.projects` |
| `--states` | Filter by work item states (comma-separated) |
| `--types` | Filter by work item types (comma-separated) |

### Reconciliation

A reconciliation scan re-checks work items beads already tracks, so items deleted
in ADO (404) or no longer readable (403) are detected instead of lingering. It
does not run on every sync — that would cost an API call per tracked item — so it
runs periodically, and `--reconcile` forces it immediately.

| Config Key | Default | Description |
|---|---|---|
| `ado.reconcile_interval` | `10` | Number of syncs between automatic reconciliation scans |

`ado.syncs_since_reconcile` also appears in config: it is the counter beads keeps
to know when the next scan is due, not a setting to edit.

## PAT Permissions

The Personal Access Token needs these scopes:

| Scope | Access | Required For |
|---|---|---|
| Work Items | Read & Write | Creating and updating work items |

Generate a PAT at: `https://dev.azure.com/{org}/_usersettings/tokens`

## Metadata Preserved

beads stores ADO-specific metadata for round-trip fidelity:

| Metadata Key | Description |
|---|---|
| `ado.rev` | ADO revision number |
| `ado.area_path` | Area path |
| `ado.iteration_path` | Iteration/sprint path |
| `ado.story_points` | Story points estimate |
| `ado.remaining_work` | Remaining work hours |
| `ado.severity` | Bug severity value |

## Description Conversion

- **Push (beads → ADO):** Markdown converted to HTML
- **Pull (ADO → beads):** HTML converted to Markdown

## Tags and Labels

- ADO tags are semicolon-separated; beads labels use arrays
- User labels round-trip through ADO tags
- Internal `beads:*` tags (e.g., `beads:blocked`) are filtered on pull — they don't appear as user labels

## API Limits

| Limit | Value |
|---|---|
| Max batch size | 200 work items per GET request |
| Max response size | 50 MiB |
| Request timeout | 30 seconds |
| Max retries | 3 (GET and WIQL only) |
| Retry backoff | Exponential with jitter, or the delay in seconds from `Retry-After` |

## Troubleshooting

### Common Errors

**`ado.pat not configured: set via 'bd config set ado.pat <token>' or AZURE_DEVOPS_PAT env var`**
```bash
bd config set ado.pat "your-pat-here"
# or
export AZURE_DEVOPS_PAT="your-pat-here"
```

**`ado.org not configured: set via 'bd config set ado.org <org>' or AZURE_DEVOPS_ORG env var`**
```bash
bd config set ado.org "your-org"
# or for on-prem:
bd config set ado.url "https://tfs.company.com/DefaultCollection"
```

**State transition errors (400 Bad Request)**
This usually means the process template doesn't support a direct state change. Check your `ado.state_map.*` config matches your actual process template.

**Type not found errors**
Verify your `ado.type_map.*` config matches the work item types available in your project. Use `--types` filter to restrict which types are synced.

### Debugging

```bash
# Preview what would happen
bd ado sync --dry-run

# Check configuration and PAT presence
bd ado status --json
```

The status command reports configuration readiness and PAT presence. To check API access to the configured organization or server, use `bd ado projects`.
