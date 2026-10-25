package graphstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/legacyimport"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/depid"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/storage/kvkeys"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/utils"
)

// LegacyImportResult makes each old identity's new graph address explicit.
type LegacyImportResult struct {
	IssuePrefix             string            `json:"issue_prefix,omitempty"`
	SourceIssuePrefix       string            `json:"source_issue_prefix,omitempty"`
	IDRoot                  string            `json:"id_root,omitempty"`
	GeneratedIDsProvisional bool              `json:"generated_ids_provisional"`
	Issues                  int               `json:"issues"`
	Memories                int               `json:"memories"`
	Dependencies            int               `json:"dependencies"`
	Comments                int               `json:"comments"`
	IDs                     map[string]string `json:"ids"`
	MemoryIDs               map[string]string `json:"memory_ids"`
	DependencyIDs           map[string]string `json:"dependency_ids"`
	DryRun                  bool              `json:"dry_run"`
	History                 string            `json:"history"`
}

// ImportLegacy installs an ordinary export into an empty graph. Every native
// row, canonical allocation and initial retained snapshot shares one checked
// transaction. It never adopts existing rows or replays a failed transaction.
// Dry-run executes the same engine validation in a rollback-only transaction.
func (s *Store) ImportLegacy(ctx context.Context, input legacyimport.Batch, actor string, dryRun bool) (LegacyImportResult, error) {
	return s.ImportLegacyWithPrefix(ctx, input, actor, dryRun, "")
}

// ImportLegacyWithPrefix supplies configuration missing from legacy JSONL.
// A supplied prefix must match every Issue identity exactly; it never rewrites IDs.
func (s *Store) ImportLegacyWithPrefix(ctx context.Context, input legacyimport.Batch, actor string, dryRun bool, sourcePrefix string) (LegacyImportResult, error) {
	return s.ImportLegacyWithOptions(ctx, input, actor, dryRun, LegacyImportOptions{SourcePrefix: sourcePrefix})
}

// LegacyImportOptions separates missing source configuration from explicit
// destination placement. A root applies only to this package, never future creates.
type LegacyImportOptions struct {
	SourcePrefix string
	IDRoot       string
}

// ImportLegacyWithOptions preserves explicit source identities beneath an optional
// root. New Link IDs come from the shared allocator; dry-run never reserves them.
func (s *Store) ImportLegacyWithOptions(ctx context.Context, input legacyimport.Batch, actor string, dryRun bool, options LegacyImportOptions) (LegacyImportResult, error) {
	prepared, err := prepareLegacyImportWithPrefix(input, actor, options.SourcePrefix)
	if err != nil {
		return LegacyImportResult{}, err
	}
	mounted, err := mountLegacyImport(prepared, options.IDRoot)
	if err != nil {
		return LegacyImportResult{}, err
	}
	batch := mounted.Batch
	nativeTypes, err := prepareLegacyImportNativeTypes(mounted, s.ScopeURL())
	if err != nil {
		return LegacyImportResult{}, err
	}
	memories, err := s.prepareLegacyImportMemories(mounted, actor)
	if err != nil {
		return LegacyImportResult{}, err
	}
	result := LegacyImportResult{SourceIssuePrefix: prepared.prefix, IDRoot: options.IDRoot, Issues: len(batch.Issues), Memories: len(batch.Memories),
		IDs: map[string]string{}, MemoryIDs: map[string]string{}, DependencyIDs: map[string]string{}, DryRun: dryRun,
		History: "current data only; new initial graph versions; source history is not present in legacy export"}
	err = s.withTxPolicy(ctx, txPolicy{mayWrite: true, commit: !dryRun, validateTypes: true}, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		if err := requireEmptyLegacyTarget(ctx, tx); err != nil {
			return err
		}
		if err := s.touchCoordination(ctx, tx); err != nil {
			return err
		}
		// A destination root is placement for this import only. Unrooted import
		// keeps historical source-prefix adoption; rooted import keeps the
		// destination's configured future-create prefix, including in the result.
		if err := tx.QueryRowContext(ctx, "SELECT value FROM config WHERE `key`='issue_prefix'").Scan(&result.IssuePrefix); err != nil {
			return legacyTargetInspectionError(ctx, "config.issue_prefix", err)
		}
		if len(batch.Issues) > 0 {
			if options.IDRoot == "" {
				if _, err := tx.ExecContext(ctx, "UPDATE config SET value=? WHERE `key`='issue_prefix'", prepared.prefix); err != nil {
					return err
				}
				result.IssuePrefix = prepared.prefix
			}
			if err := s.afterStage("import-prefix"); err != nil {
				return err
			}
		}
		infra, err := issueops.ResolveInfraTypesStrictInTx(ctx, tx)
		if err != nil {
			return fmt.Errorf("resolve infrastructure Issue types: %w", err)
		}
		for _, issue := range batch.Issues {
			if infra[string(issue.IssueType.Normalize())] {
				return fmt.Errorf("%w: infrastructure Issue %s is unsupported", storage.ErrValidation, mounted.sourceIssues[issue.ID])
			}
		}
		// Bind every prospective native Type before checking ANY incoming
		// Resource ID: a later record's Type can collide with an earlier key.
		for _, id := range nativeTypes.bindings {
			if err := s.ensureNativeTypeInTx(ctx, tx, id); err != nil {
				return err
			}
		}
		for _, issue := range batch.Issues {
			if err := checkLegacyImportPublicIDInTx(ctx, tx, "Issue", mounted.sourceIssues[issue.ID], issue.ID); err != nil {
				return err
			}
		}
		for _, memory := range batch.Memories {
			if err := checkLegacyImportPublicIDInTx(ctx, tx, "Memory key", memory.Key, mounted.memoryIDs[memory.Key]); err != nil {
				return err
			}
		}
		unscope := issueops.ScopeVersionedHistoryTransaction(tx, true)
		defer unscope()
		if len(batch.Issues) > 0 {
			_, err := issueops.CreateIssuesInTxWithResult(ctx, tx, batch.Issues, actor, storage.BatchCreateOptions{CreateOnly: true, SkipPrefixValidation: true})
			if err != nil {
				if errors.Is(err, domain.ErrDependencyCycle) {
					return fmt.Errorf("%w: legacy dependencies contain a cycle", storage.ErrValidation)
				}
				var hierarchy *domain.DependencyHierarchyConflictError
				if errors.As(err, &hierarchy) {
					return fmt.Errorf("%w: legacy dependency hierarchy conflict: %w", storage.ErrValidation, err)
				}
				return err
			}
			if err := s.afterStage("import-native"); err != nil {
				return err
			}
		}
		issuePaths := make(map[string]string, len(batch.Issues))
		for _, issue := range batch.Issues {
			path, err := generatedIssuePath(ctx, tx, issue.ID)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO graph_preview_catalog (path,resource_kind,type_url,revision,allocation_state,backing,backing_key) VALUES (?,'bead',?,?,'live','issue',?)`, path, nativeTypes.issues[issue.ID], strings.Repeat("0", 32), issue.ID); err != nil {
				return err
			}
			issuePaths[issue.ID] = path
			publicURL, err := graph.CanonicalPublicResourceURL(s.ScopeURL(), issue.ID)
			if err != nil {
				return err
			}
			result.IDs[mounted.sourceIssues[issue.ID]] = publicURL
			result.Comments += len(issue.Comments)
		}
		if err := s.afterStage("import-issues"); err != nil {
			return err
		}
		// Reserve ALL explicit incoming Memory identities before asking the
		// generator for Link IDs, so generated names cannot steal a later key.
		for _, memory := range memories {
			accepted, err := s.createPreparedMemoryInTx(ctx, tx, memory.request, memory.record, memory.properties)
			if err != nil {
				return err
			}
			path, err := s.publicResourcePathInTx(ctx, tx, accepted.ID, graph.KindBead)
			if err != nil {
				return err
			}
			stored, err := s.showMemoryInTx(ctx, tx, path)
			if err != nil {
				return err
			}
			if stored.ID != accepted.ID || stored.PublicID != memory.request.PublicID || stored.Properties.Title != memory.record.Properties.Title || stored.Properties.Body != memory.record.Properties.Body {
				return fmt.Errorf("%w: Memory %q identity/title/value cannot be preserved exactly", storage.ErrValidation, memory.sourceKey)
			}
			result.MemoryIDs[memory.sourceKey] = accepted.ID
		}
		for _, issue := range batch.Issues {
			for _, dep := range issue.Dependencies {
				key := depid.New(issue.ID, dep.DependsOnID)
				publicID, err := selectGeneratedPublicIDInTx(ctx, tx, options.IDRoot)
				if err != nil {
					return err
				}
				path, err := preferredPrivateResourcePath(graph.KindLink, publicID)
				if err != nil {
					return err
				}
				path, err = choosePrivateResourcePathInTx(ctx, tx, path, true, graph.KindLink)
				if err != nil {
					return err
				}
				revision, err := freshToken()
				if err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO graph_preview_catalog (path,resource_kind,type_url,revision,allocation_state,backing,backing_key,public_id) VALUES (?,'link',?,?,'live','dependency',?,?)`, path, nativeTypes.dependencies[dep.Type], revision, key, publicID); err != nil {
					return err
				}
				link, err := s.currentLinkForCreationInTx(ctx, tx, path)
				if err != nil {
					return err
				}
				raw, err := canonicalJSON(link)
				if err != nil {
					return err
				}
				if err := insertPreviewVersionInTx(ctx, tx, path, revision, raw, link.Attribution.Actor); err != nil {
					return err
				}
				pair := mounted.sourceIssues[issue.ID] + " -> " + mounted.sourceIssues[dep.DependsOnID]
				result.DependencyIDs[pair] = link.ID
				result.Dependencies++
			}
		}
		result.GeneratedIDsProvisional = dryRun && result.Dependencies > 0
		if err := s.afterStage("import-links"); err != nil {
			return err
		}
		for _, issue := range batch.Issues {
			path := issuePaths[issue.ID]
			if err := s.recordIssueMappingContextInTx(ctx, tx, path, issue.ID, issueRevisionContext{}, actor); err != nil {
				return err
			}
			stored, err := issueops.HydrateIssueOperationResult(ctx, tx, issue.ID, true)
			if err != nil {
				return err
			}
			if err := verifyLegacyIssue(issue, stored); err != nil {
				return err
			}
			if _, err := s.showIssueInTx(ctx, tx, path); err != nil {
				return err
			}
		}
		if err := s.afterStage("import-retained"); err != nil {
			return err
		}
		if err := s.checkCurrentReadBytes(ctx, tx); err != nil {
			return fmt.Errorf("validate imported current-read representation: %w", err)
		}
		return nil
	})
	if err != nil {
		return LegacyImportResult{}, err
	}
	return result, nil
}

func checkLegacyImportPublicIDInTx(ctx context.Context, tx *sql.Tx, kind, source, id string) error {
	exists, err := publicResourceIDExistsInTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("%w: legacy %s %q maps to reserved public ID %q", storage.ErrValidation, kind, source, id)
	}
	return nil
}

func requireEmptyLegacyTarget(ctx context.Context, tx *sql.Tx) error {
	// Include reserved/deleted allocations and unmapped native rows: deleting
	// everything or bypassing graph writers never makes a workspace fresh again.
	tables := []string{"graph_preview_catalog", "graph_preview_payloads", "graph_preview_links", "issue_versions", "graph_preview_versions", "graph_preview_issue_versions", "bd_events_journal", "leases", "issue_counter", "epoch_minted_addresses", "interactions"}
	// Init installs both main and ignored migration lanes, including the wisp
	// counters. Missing tables fail closed; import never repairs the schema.
	// Reuse the native ownership inventory so orphan history and counters cannot
	// attach themselves to newly imported identities.
	// Binding/Types, migration cursors, ordinary workflow/routing settings and
	// repository/clone bookkeeping (metadata/local_metadata) are configuration,
	// not resource allocations. They remain permitted and are not a proof of an
	// unused workspace. See engdocs/evidence/legacy-import-preview3-preparation-2026-10-18/freshness-inventory.md.
	tables = append(tables, issueops.DeleteCascadeTables(false)...)
	tables = append(tables, issueops.DeleteCascadeTables(true)...)
	seen := make(map[string]bool, len(tables))
	for _, table := range tables {
		if seen[table] {
			continue
		}
		seen[table] = true
		var count int
		//nolint:gosec // G201: fixed graph tables and native deletion inventory.
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			return legacyTargetInspectionError(ctx, table, err)
		}
		if count != 0 {
			return fmt.Errorf("%w: legacy import requires an empty fresh graph workspace; %s is occupied (repeat imports do not update or duplicate data)", storage.ErrValidation, table)
		}
	}
	// The journal sequence is seeded at (0,0), so row presence alone is normal.
	// A consumed sequence still records prior history after journal deletion.
	var usedSequence int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM bd_events_seq WHERE id<>0 OR next_seq<>0").Scan(&usedSequence); err != nil {
		return legacyTargetInspectionError(ctx, "bd_events_seq", err)
	}
	if usedSequence != 0 {
		return fmt.Errorf("%w: legacy import requires an empty fresh graph workspace; bd_events_seq is occupied (prior journal sequence allocation)", storage.ErrValidation)
	}
	// The epoch singleton may be absent or lazily seeded at epoch 1. A bump
	// or its durable markers records prior use even after resource deletion.
	var changedEpoch int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM store_epoch WHERE id<>1 OR epoch<>1 OR bumped_at IS NOT NULL OR bumped_reason IS NOT NULL").Scan(&changedEpoch); err != nil {
		return legacyTargetInspectionError(ctx, "store_epoch", err)
	}
	if changedEpoch != 0 {
		return fmt.Errorf("%w: legacy import requires an empty fresh graph workspace; store_epoch is occupied (prior epoch transition)", storage.ErrValidation)
	}
	// Classic Memories are config-backed live data, including empty values.
	// Infrastructure configuration alone does not make a destination occupied.
	var memories int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM config WHERE LEFT(`key`,?)=?", utf8.RuneCountInString(kvkeys.MemoryConfigKeyPrefix), kvkeys.MemoryConfigKeyPrefix).Scan(&memories); err != nil {
		return legacyTargetInspectionError(ctx, "config", err)
	}
	if memories != 0 {
		return fmt.Errorf("%w: legacy import requires an empty fresh graph workspace; %d legacy KV Memories are present in config under %q; use a newly initialized empty graph workspace", storage.ErrValidation, memories, kvkeys.MemoryConfigKeyPrefix)
	}
	return nil
}

// An interrupted inspection establishes no verdict about the destination.
// Preserve all causes without advising schema repair for caller cancellation.
func legacyTargetInspectionError(ctx context.Context, plane string, cause error) error {
	contextErr := ctx.Err()
	if contextErr != nil && !errors.Is(cause, contextErr) {
		return fmt.Errorf("inspect legacy import target %s: %w (%w)", plane, cause, contextErr)
	}
	if contextErr != nil || errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return fmt.Errorf("inspect legacy import target %s: %w", plane, cause)
	}
	if plane == "config.issue_prefix" {
		return fmt.Errorf("%w: fresh workspace Issue prefix configuration is missing or unreadable: %w", ErrInvalidStore, cause)
	}
	return fmt.Errorf("%w: cannot establish workspace freshness from %s; use a fully initialized fresh graph workspace (import does not repair schema): %w", ErrInvalidStore, plane, cause)
}

// LegacyImportCommentLimit bounds native comment persistence independently of
// the Bead/Link ceiling because comments are outside retained Issue payloads.
const LegacyImportCommentLimit = 1000

type preparedLegacyImport struct {
	legacyimport.Batch
	prefix string
}

func prepareLegacyImport(input legacyimport.Batch, actor string) (legacyimport.Batch, error) {
	prepared, err := prepareLegacyImportWithPrefix(input, actor, "")
	return prepared.Batch, err
}

func prepareLegacyImportWithPrefix(input legacyimport.Batch, actor, sourcePrefix string) (preparedLegacyImport, error) {
	invalid := func(format string, args ...any) (preparedLegacyImport, error) {
		return preparedLegacyImport{}, fmt.Errorf("%w: "+format, append([]any{storage.ErrValidation}, args...)...)
	}
	if actor == "" || !utf8.ValidString(actor) {
		return invalid("legacy import requires a UTF-8 actor")
	}
	if !legacyImportStringsValid(reflect.ValueOf(input)) {
		return invalid("legacy Issue and Memory strings must be UTF-8")
	}
	for _, issue := range input.Issues {
		if issue == nil {
			continue
		}
		// RawMessage bypasses the string walk. Validate the original metadata
		// bytes before cloning or canonicalization can obscure their origin.
		if !utf8.Valid(issue.Metadata) {
			return invalid("Issue %q metadata must be UTF-8", issue.ID)
		}
		for _, field := range []struct {
			name string
			set  bool
		}{
			{"WispPlaneOverride", issue.WispPlaneOverride != nil},
			{"IDPrefix", issue.IDPrefix != ""},
			{"PrefixOverride", issue.PrefixOverride != ""},
			{"SourceRepo", issue.SourceRepo != ""},
			{"IsLitePartial", issue.IsLitePartial},
		} {
			if field.set {
				return invalid("Issue %q sets unsupported in-memory field %s", issue.ID, field.name)
			}
		}
	}
	// encoding/json replaces invalid UTF-8 strings. Check keys/values before
	// cloning so a direct caller cannot accidentally import replacement text.
	for _, memory := range input.Memories {
		if memory.Key == "" || !utf8.ValidString(memory.Key) || !utf8.ValidString(memory.Value) {
			return invalid("memory requires a nonempty UTF-8 key and UTF-8 value")
		}
	}
	// Isolate mutable native-import normalization from the caller's values.
	raw, err := json.Marshal(input)
	if err != nil {
		return invalid("legacy input: %v", err)
	}
	if len(raw) > legacyimport.MaxBytes {
		return preparedLegacyImport{}, fmt.Errorf("%w: legacy import exceeds %d bytes", ErrLimitExceeded, legacyimport.MaxBytes)
	}
	var batch legacyimport.Batch
	if err := json.Unmarshal(raw, &batch); err != nil {
		return invalid("legacy input: %v", err)
	}
	if len(batch.Issues)+len(batch.Memories) == 0 {
		return invalid("legacy import requires at least one Issue or Memory; source is empty")
	}
	comments := 0
	ids := map[string]bool{}
	commentIDs := map[string]bool{}
	resources := len(batch.Issues) + len(batch.Memories)
	for _, issue := range batch.Issues {
		if issue == nil || issue.ID == "" {
			return invalid("each exported Issue requires an ID")
		}
		if err := validatePath("beads/" + issue.ID); err != nil {
			return invalid("Issue ID %q: %v", issue.ID, err)
		}
		if ids[issue.ID] {
			return invalid("duplicate Issue ID %q", issue.ID)
		}
		issuePrefix := legacyImportPrefix(issue.ID)
		if issuePrefix == "" {
			return invalid("Issue %s requires a nonempty ordinary ID prefix", issue.ID)
		}
		ids[issue.ID] = true
		if len(issue.BondedFrom) != 0 || issue.SourceFormula != "" || issue.SourceLocation != "" {
			return invalid("Issue %s has unsupported bonded_from, source_formula or source_location lineage", issue.ID)
		}
		if issue.Ephemeral || issue.NoHistory || issue.StorageClass != "" || issue.LeaseExpiresAt != nil || issue.HeartbeatAt != nil || issue.LeaseGrantedNode != "" {
			return invalid("Issue %s has unsupported ephemeral, no-history, explicit storage-class or live lease data", issue.ID)
		}
		if issue.Status == "tombstone" {
			return invalid("tombstone %s is unsupported", issue.ID)
		}
		// Labels are a set in native storage. Normalize the isolated copy so
		// duplicate source spellings cannot be mistaken for storage loss.
		seenLabels := make(map[string]bool, len(issue.Labels))
		labels := issue.Labels[:0]
		for _, label := range issue.Labels {
			if !seenLabels[label] {
				labels = append(labels, label)
				seenLabels[label] = true
			}
		}
		issue.Labels = labels
		issue.SetDefaults()
		now := time.Now().UTC().Truncate(time.Second)
		if issue.CreatedAt.IsZero() {
			issue.CreatedAt = now
		}
		if issue.UpdatedAt.IsZero() {
			issue.UpdatedAt = now
		}
		metadata, err := commonMetadata(issue.Metadata)
		if err != nil {
			return invalid("Issue %s metadata: %v", issue.ID, err)
		}
		issue.Metadata = metadata
		if err := validateIssueCreateFields(issue); err != nil {
			return invalid("Issue %s: %v", issue.ID, err)
		}
		if err := issueops.PrepareIssueForInsert(issue, nil, nil); err != nil {
			return invalid("Issue %s: %v", issue.ID, err)
		}
		pairs := map[string]bool{}
		for _, dep := range issue.Dependencies {
			if dep == nil {
				return invalid("Issue %s has a null dependency", issue.ID)
			}
			if dep.IssueID == "" {
				dep.IssueID = issue.ID
			}
			if types.ExtractPrefix(dep.IssueID) != types.ExtractPrefix(dep.DependsOnID) || issueops.IsExternalDepTarget(dep.IssueID, dep.DependsOnID) {
				return invalid("dependency %s -> %s uses unsupported external or cross-prefix routing", dep.IssueID, dep.DependsOnID)
			}
			if dep.Metadata != "" {
				canonical, err := commonMetadata(json.RawMessage(dep.Metadata))
				if err != nil || string(canonical) != "{}" {
					return invalid("dependency metadata for %s must be an empty object", issue.ID)
				}
				dep.Metadata = "{}"
			}
			if !dep.Type.IsValid() {
				return invalid("dependency kind for %s must be nonempty and at most %d bytes", issue.ID, types.MaxDependencyTypeLen)
			}
			if dep.IssueID != issue.ID || dep.DependsOnID == issue.ID || dep.ThreadID != "" {
				return invalid("Issue %s supports only local dependencies without edge metadata or threads", issue.ID)
			}
			if pairs[dep.DependsOnID] {
				return invalid("duplicate dependency %s -> %s", issue.ID, dep.DependsOnID)
			}
			pairs[dep.DependsOnID] = true
			resources++
		}
		if len(issue.Dependencies) > PreviewOwnedLinkLimit {
			return preparedLegacyImport{}, fmt.Errorf("%w: Issue %s exceeds owned Link limit", ErrLimitExceeded, issue.ID)
		}
		comments += len(issue.Comments)
		if comments > LegacyImportCommentLimit {
			return preparedLegacyImport{}, fmt.Errorf("%w: legacy import exceeds %d comments", ErrLimitExceeded, LegacyImportCommentLimit)
		}
		for _, comment := range issue.Comments {
			if comment == nil {
				return invalid("Issue %s has a null comment", issue.ID)
			}
			if comment.IssueID != "" && comment.IssueID != issue.ID {
				return invalid("comment issue_id differs from %s", issue.ID)
			}
			comment.IssueID = issue.ID
			if comment.ID != "" && commentIDs[comment.ID] {
				return invalid("duplicate comment ID %q", comment.ID)
			}
			if comment.ID != "" {
				commentIDs[comment.ID] = true
			}
		}
	}
	prefix, err := resolveLegacyImportPrefix(batch.Issues, sourcePrefix)
	if err != nil {
		return preparedLegacyImport{}, err
	}
	for _, issue := range batch.Issues {
		for _, dep := range issue.Dependencies {
			if !ids[dep.DependsOnID] {
				return invalid("dependency %s -> %s does not name an Issue in this import", issue.ID, dep.DependsOnID)
			}
		}
	}
	memoryKeys := map[string]bool{}
	for _, memory := range batch.Memories {
		if memoryKeys[memory.Key] {
			return invalid("duplicate memory key %q", memory.Key)
		}
		memoryKeys[memory.Key] = true
	}
	if resources > PreviewSnapshotLimit {
		return preparedLegacyImport{}, fmt.Errorf("%w: legacy import exceeds preview limit of %d Beads and Links", ErrLimitExceeded, PreviewSnapshotLimit)
	}
	return preparedLegacyImport{Batch: batch, prefix: prefix}, nil
}

// Ordinary JSONL does not carry configured prefixes. Use the same inference
// as ordinary ID handling, including multi-part numeric/hash prefixes. A bare
// prefix with no ID suffix is never an exported Issue identity.
func legacyImportPrefix(id string) string {
	if strings.HasSuffix(id, "-") {
		return ""
	}
	return utils.ExtractIssuePrefix(id)
}

// Resolve once from the complete batch. A numeric/hash candidate can resolve
// all-letter suffixes under that same prefix. With only ambiguous custom/hash
// spellings, configuration is absent from JSONL and must be supplied explicitly.
func resolveLegacyImportPrefix(issues []*types.Issue, supplied string) (string, error) {
	invalid := func(message string) (string, error) {
		return "", fmt.Errorf("%w: %s", storage.ErrValidation, message)
	}
	if len(issues) == 0 {
		if supplied != "" {
			return invalid("source prefix requires at least one Issue")
		}
		return "", nil
	}
	prefix := supplied
	if prefix == "" {
		for _, issue := range issues {
			candidate := legacyImportPrefix(issue.ID)
			if len(candidate) > len(prefix) || (len(candidate) == len(prefix) && candidate < prefix) {
				prefix = candidate
			}
		}
	}
	if prefix == "" || strings.TrimSpace(prefix) != prefix || strings.HasSuffix(prefix, "-") || !utf8.ValidString(prefix) {
		return invalid("source prefix must be nonempty UTF-8, without surrounding whitespace or a trailing hyphen")
	}
	confident := false
	for _, issue := range issues {
		if !strings.HasPrefix(issue.ID, prefix+"-") || len(issue.ID) == len(prefix)+1 {
			return invalid(fmt.Sprintf("legacy import requires one Issue ID prefix; %s does not match %s", issue.ID, prefix))
		}
		if strings.LastIndex(issue.ID, "-") == len(prefix) {
			confident = true
		}
	}
	if supplied == "" && !confident {
		return invalid("source Issue prefix is ambiguous; supply --source-prefix from the original workspace configuration")
	}
	return prefix, nil
}

// Check original strings before encoding/json can replace invalid UTF-8.
// Legacy rows contain scalar fields, optional fields and relation slices;
// unexported time internals and raw JSON bytes are handled by their own codecs.
func legacyImportStringsValid(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.String:
		return utf8.ValidString(value.String())
	case reflect.Pointer:
		return value.IsNil() || legacyImportStringsValid(value.Elem())
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if value.Type().Field(i).PkgPath == "" && !legacyImportStringsValid(value.Field(i)) {
				return false
			}
		}
	case reflect.Slice:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			break
		}
		for i := 0; i < value.Len(); i++ {
			if !legacyImportStringsValid(value.Index(i)) {
				return false
			}
		}
	}
	return true
}

func verifyLegacyIssue(want, got *types.Issue) error {
	// Native import intentionally regenerates dependency surrogate IDs and
	// derived readiness. Comments remain outside the retained Issue payload.
	w, g := *want, *got
	var err error
	w.Metadata, err = commonMetadata(w.Metadata)
	if err != nil {
		return err
	}
	g.Metadata, err = commonMetadata(g.Metadata)
	if err != nil {
		return err
	}
	w.Labels = append([]string(nil), w.Labels...)
	g.Labels = append([]string(nil), g.Labels...)
	sort.Strings(w.Labels)
	sort.Strings(g.Labels)
	w.Dependencies, g.Dependencies = nil, nil
	w.Comments, g.Comments = nil, nil
	w.IsBlocked, g.IsBlocked = false, false
	w.ContentHash, g.ContentHash = "", ""
	w.RowVersion, g.RowVersion = 0, 0
	wraw, err := canonicalJSON(w)
	if err != nil {
		return err
	}
	graw, err := canonicalJSON(g)
	if err != nil {
		return err
	}
	if !bytes.Equal(wraw, graw) {
		var wf, gf map[string]json.RawMessage
		if err := json.Unmarshal(wraw, &wf); err != nil {
			return err
		}
		if err := json.Unmarshal(graw, &gf); err != nil {
			return err
		}
		for key, value := range wf {
			if !bytes.Equal(value, gf[key]) {
				return fmt.Errorf("%w: Issue %s field %s cannot be represented exactly in graph storage", storage.ErrValidation, w.ID, key)
			}
		}
		return fmt.Errorf("%w: Issue %s acquired unsupported fields in graph storage", storage.ErrValidation, w.ID)
	}
	// Retain the native comment feed exactly, including imported IDs. Detect
	// duplicate-content collapse and timestamp coercion before committing.
	if len(want.Comments) != len(got.Comments) {
		return fmt.Errorf("%w: comments for %s cannot be preserved exactly", storage.ErrValidation, w.ID)
	}
	storedComments := make(map[string]*types.Comment, len(got.Comments))
	for _, stored := range got.Comments {
		storedComments[stored.ID] = stored
	}
	for _, comment := range want.Comments {
		// The native writer fills missing IDs before this check. A missing
		// timestamp still permits its generated time, without wildcard pairing.
		stored := storedComments[comment.ID]
		if comment.ID == "" || stored == nil || comment.IssueID != stored.IssueID || comment.Author != stored.Author || comment.Text != stored.Text || (!comment.CreatedAt.IsZero() && !comment.CreatedAt.Equal(stored.CreatedAt)) {
			return fmt.Errorf("%w: comment for %s cannot be preserved exactly", storage.ErrValidation, w.ID)
		}
		delete(storedComments, comment.ID)
	}
	// Verify edge provenance separately; the graph Link reader validates its
	// shape and the native retained snapshot validates the complete edge set.
	if len(want.Dependencies) != len(got.Dependencies) {
		return fmt.Errorf("%w: dependencies for %s cannot be preserved exactly", storage.ErrValidation, w.ID)
	}
	for _, dep := range want.Dependencies {
		found := false
		for _, stored := range got.Dependencies {
			if dep.IssueID == stored.IssueID && dep.DependsOnID == stored.DependsOnID && dep.Type == stored.Type && (dep.CreatedBy == "" || dep.CreatedBy == stored.CreatedBy) && (dep.CreatedAt.IsZero() || dep.CreatedAt.Equal(stored.CreatedAt)) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: dependency for %s cannot be preserved exactly", storage.ErrValidation, w.ID)
		}
	}
	return nil
}
