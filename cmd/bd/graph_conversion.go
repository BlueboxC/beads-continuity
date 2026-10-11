package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/steveyegge/beads/internal/codeindex"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/knowledge"
	"github.com/steveyegge/beads/internal/migration"
	"github.com/steveyegge/beads/internal/storage/graphstore"
	"github.com/steveyegge/beads/internal/workspacegate"
)

type graphConversionPlan struct {
	Version        int                       `json:"version"`
	BinarySHA256   string                    `json:"binary_sha256"`
	MetadataSHA256 string                    `json:"metadata_sha256"`
	Store          graphstore.ConversionPlan `json:"store"`
}

type graphConversionReceipt struct {
	Version        int                        `json:"version"`
	Phase          string                     `json:"phase"`
	Plan           graphConversionPlan        `json:"plan"`
	Directory      string                     `json:"directory"`
	BackupSHA256   string                     `json:"backup_sha256"`
	CandidateState graphstore.ConversionState `json:"candidate_state"`
	AdoptionPlan   *graphstore.ConversionPlan `json:"adoption_plan,omitempty"`
	PrunedCode     map[string]string          `json:"pruned_code,omitempty"`
}

var graphConvertCmd = &cobra.Command{
	Use: "graph-convert", GroupID: "advanced", Short: "Plan and rehearse explicit native-to-graph conversion on a restored candidate",
	Annotations: map[string]string{skipStoreAnnotation: "1"},
	Long: `Capture a private native Dolt checkpoint and rehearse adoption on a separate candidate.
The existing project remains native. This command never initializes another project,
executes project code, changes hooks, renews learning, or publishes project data.
Initial support requires current embedded native schema and main as the only dirty branch.`,
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		initCommandContext()
		setRootContext(setupGracefulShutdown())
		applyNoColorFlag()
		allowed := map[string]bool{"json": true, "actor": true, "quiet": true, "no-color": true, "readonly": true, "sandbox": true, "workspace": true, "scope-url": true, "out": true, "plan": true, "receipt": true, "writers-stopped": true, "prune-obsolete-code": true}
		var bad string
		cmd.Flags().Visit(func(f *pflag.Flag) {
			if !allowed[f.Name] && bad == "" {
				bad = f.Name
			}
		})
		if bad != "" {
			return fmt.Errorf("graph-convert does not implement --%s; use explicit workspace/receipt paths", bad)
		}
		return nil
	},
	PersistentPostRunE: func(_ *cobra.Command, _ []string) error { return nil },
}

func init() {
	plan := &cobra.Command{Use: "plan", Short: "Write a readonly private plan bound to binary, selector and exact database roots", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := runGraphConversionPlan(cmd)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		}}
	plan.Flags().String("workspace", "", "Explicit existing .beads directory (no discovery fallback)")
	plan.Flags().String("scope-url", "", "Permanent operator-selected canonical Scope URL")
	plan.Flags().String("out", "", "New private plan JSON file")
	plan.Flags().Bool("prune-obsolete-code", false, "Explicitly permit official obsolete-code pruning only on the restored candidate (native plane <=2 GiB)")
	prepare := &cobra.Command{Use: "prepare", Short: "Capture native backup, restore an isolated candidate, adopt and verify it", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			CheckReadonly("graph-convert prepare")
			result, err := runGraphConversionPrepare(cmd)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		}}
	prepare.Flags().String("plan", "", "Private plan JSON")
	prepare.Flags().String("out", "", "New private rehearsal directory outside the existing workspace")
	prepare.Flags().Bool("writers-stopped", false, "Assert all clients/hooks/viewer writers are quiescent; does not stop them")
	verify := &cobra.Command{Use: "verify", Short: "Reopen and verify a prepared candidate and preserved native data", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			receipt, err := loadGraphConversionReceipt(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(getRootContext(), 5*time.Minute)
			defer cancel()
			result, err := graphConversionVerifyReceipt(ctx, receipt)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		}}
	verify.Flags().String("receipt", "", "Private rehearsal receipt JSON")
	graphConvertCmd.AddCommand(plan, prepare, verify, graphConversionPublicationCommand(false), graphConversionPublicationCommand(true))
	rootCmd.AddCommand(graphConvertCmd)
}

func graphConversionOptions(plan graphConversionPlan) graphstore.Options {
	return graphstore.Options{Backend: "embedded", DataDir: plan.Store.SourceDataDir, Database: plan.Store.Database, Branch: "main", Binding: plan.Store.Binding}
}

func graphConversionFileHash(path string) (hash string, err error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("conversion artifact must be a regular file, not a symlink or device")
	}
	f, err := os.Open(path) // #nosec G304 -- explicit operator-selected artifact/executable; no server or project payload selects this path
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func graphConversionBinaryHash() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	return graphConversionFileHash(path)
}

func graphConversionCanonicalPath(path string) (string, error) {
	if path == "" {
		return "", errors.New("an explicit path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func graphConversionNewPath(path string) (string, error) {
	if path == "" {
		return "", errors.New("an explicit output path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}

func graphConversionWriteNew(path string, value any) (err error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > 16<<20 {
		return errors.New("conversion artifact exceeds 16 MiB")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600) // #nosec G304 -- explicit new private output; canonical parent and exclusive creation prevent replacement
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	if _, err := f.Write(raw); err != nil {
		return err
	}
	return f.Sync()
}

func graphConversionReadJSON(path string, value any) error {
	f, err := os.Open(path) // #nosec G304 -- explicit operator-selected artifact/executable; no server or project payload selects this path
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return errors.New("conversion artifact must be a regular JSON file under 16 MiB")
	}
	decoder := json.NewDecoder(io.LimitReader(f, 16<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("conversion artifact contains trailing JSON")
	}
	return nil
}

func runGraphConversionPlan(cmd *cobra.Command) (result graphConversionPlan, err error) {
	workspace, _ := cmd.Flags().GetString("workspace")
	workspace, err = graphConversionCanonicalPath(workspace)
	if err != nil {
		return result, err
	}
	if filepath.Base(workspace) != ".beads" {
		return result, errors.New("--workspace must name the existing .beads directory")
	}
	metadata := filepath.Join(workspace, configfile.ConfigFileName)
	info, err := os.Lstat(metadata)
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return result, errors.New("conversion selector must be a regular file under 16 MiB")
	}
	// Require the actual selector: do not invoke legacy config.json migration.
	raw, err := os.ReadFile(metadata) // #nosec G304 -- selector in the explicit canonical operator workspace
	if err != nil {
		return result, err
	}
	var cfg configfile.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return result, err
	}
	if cfg.DoltMode != "embedded" || cfg.GetBackend() != configfile.BackendDolt || cfg.GraphMode != "" && cfg.GraphMode != "dependency" || cfg.DoltDataDir != "" || cfg.DoltTeamServer {
		return result, errors.New("conversion requires explicit native embedded metadata without custom/shared/proxy routing")
	}
	if _, err := os.Lstat(filepath.Join(workspace, graphPreviewMarker)); !os.IsNotExist(err) {
		return result, errors.New("existing graph/incomplete-format marker blocks native conversion")
	}
	data, err := graphConversionCanonicalPath(filepath.Join(workspace, "embeddeddolt"))
	if err != nil {
		return result, err
	}
	if data != filepath.Join(workspace, "embeddeddolt") {
		return result, errors.New("conversion refuses a symlinked external database root")
	}
	scope, _ := cmd.Flags().GetString("scope-url")
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return result, err
	}
	result.Version = 1
	result.BinarySHA256, err = graphConversionBinaryHash()
	if err != nil {
		return result, err
	}
	h := sha256.Sum256(raw)
	result.MetadataSHA256 = hex.EncodeToString(h[:])
	o := graphstore.Options{Backend: "embedded", DataDir: data, Database: cfg.GetDoltDatabase(), Branch: "main", Binding: graphstore.Binding{WorkspaceID: workspace, ScopeURL: scope, AuthorityID: hex.EncodeToString(token[:]), SchemaVersion: graphstore.SchemaVersion}}
	ctx, cancel := context.WithTimeout(getRootContext(), 5*time.Minute)
	defer cancel()
	handle, err := acquireExclusiveWorkspaceGates(ctx, workspace, "graph-convert consistent plan", data)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, handle.Release()) }()
	prune, _ := cmd.Flags().GetBool("prune-obsolete-code")
	result.Store, err = graphConversionNativePlan(ctx, o, prune)
	if err != nil {
		return result, err
	}
	currentSelector, err := graphConversionFileHash(metadata)
	if err != nil || currentSelector != result.MetadataSHA256 {
		return result, errors.New("workspace selector changed during planning")
	}
	if _, err := os.Lstat(filepath.Join(workspace, graphPreviewMarker)); !os.IsNotExist(err) {
		return result, errors.New("workspace format changed during planning")
	}
	out, _ := cmd.Flags().GetString("out")
	out, err = graphConversionNewPath(out)
	if err != nil {
		return result, err
	}
	if err := graphConversionWriteNew(out, result); err != nil {
		return result, err
	}
	return result, nil
}

func runGraphConversionPrepare(cmd *cobra.Command) (receipt graphConversionReceipt, err error) {
	quiescent, _ := cmd.Flags().GetBool("writers-stopped")
	if !quiescent {
		return receipt, errors.New("prepare requires --writers-stopped after quiescing clients/hooks/viewers")
	}
	actor, _ := cmd.Flags().GetString("actor")
	if actor == "" {
		return receipt, errors.New("prepare requires explicit --actor for conversion provenance")
	}
	file, _ := cmd.Flags().GetString("plan")
	if err := graphConversionReadJSON(file, &receipt.Plan); err != nil {
		return receipt, err
	}
	if receipt.Plan.Version != 1 {
		return receipt, errors.New("unsupported conversion plan")
	}
	binary, err := graphConversionBinaryHash()
	if err != nil {
		return receipt, err
	}
	if binary != receipt.Plan.BinarySHA256 {
		return receipt, errors.New("conversion binary differs from plan; create a fresh plan")
	}
	o := graphConversionOptions(receipt.Plan)
	workspace := o.Binding.WorkspaceID
	if freeze := migration.Find(workspace); freeze.Frozen() {
		return receipt, errors.New("conversion refuses a frozen or unreadable migration boundary")
	}
	metadata := filepath.Join(workspace, configfile.ConfigFileName)
	hash, err := graphConversionFileHash(metadata)
	if err != nil {
		return receipt, err
	}
	if hash != receipt.Plan.MetadataSHA256 {
		return receipt, errors.New("workspace selector changed since planning")
	}
	out, _ := cmd.Flags().GetString("out")
	out, err = graphConversionNewPath(out)
	if err != nil {
		return receipt, err
	}
	if out == workspace || strings.HasPrefix(out, workspace+string(filepath.Separator)) {
		return receipt, errors.New("rehearsal directory must stay outside the original workspace")
	}
	ctx, cancel := context.WithTimeout(getRootContext(), 5*time.Minute)
	defer cancel()
	handle, err := acquireExclusiveWorkspaceGates(ctx, workspace, "graph-convert checkpoint/rehearsal", o.DataDir)
	if err != nil {
		return receipt, err
	}
	defer func() { err = errors.Join(err, handle.Release()) }()
	// Recheck the selector after acquiring the same gate held throughout capture.
	hash, err = graphConversionFileHash(metadata)
	if err != nil || hash != receipt.Plan.MetadataSHA256 {
		return receipt, errors.New("workspace selector changed before checkpoint")
	}
	current, err := graphConversionNativePlan(ctx, o, receipt.Plan.Store.PruneObsoleteCode)
	if err != nil {
		return receipt, err
	}
	if !reflect.DeepEqual(current, receipt.Plan.Store) {
		return receipt, errors.New("native database changed since planning")
	}
	if err := graphConversionCheckSpace(o.DataDir, filepath.Dir(out)); err != nil {
		return receipt, err
	}
	if err := os.Mkdir(out, 0700); err != nil {
		return receipt, err
	}
	receipt.Version = 1
	receipt.Phase = "preparing"
	receipt.Directory = out
	if err := graphConversionWriteNew(filepath.Join(out, "preparation.json"), receipt); err != nil {
		return receipt, err
	}
	if err := graphConversionCopyFile(metadata, filepath.Join(out, "original-metadata.json"), 0600); err != nil {
		return receipt, err
	}
	exe, err := os.Executable()
	if err != nil {
		return receipt, err
	}
	if err := graphConversionCopyFile(exe, filepath.Join(out, "rollback-bd"), 0700); err != nil {
		return receipt, err
	}
	copied, err := graphConversionFileHash(filepath.Join(out, "rollback-bd"))
	if err != nil || copied != receipt.Plan.BinarySHA256 {
		return receipt, errors.New("rollback binary differs from the planned executable")
	}
	copied, err = graphConversionFileHash(filepath.Join(out, "original-metadata.json"))
	if err != nil || copied != receipt.Plan.MetadataSHA256 {
		return receipt, errors.New("checkpoint selector differs from plan")
	}
	if err := os.Mkdir(filepath.Join(out, "candidate"), 0700); err != nil {
		return receipt, err
	}
	if err := graphstore.CheckpointConversion(ctx, o, receipt.Plan.Store, filepath.Join(out, "backup"), graphConversionCandidateDir(receipt)); err != nil {
		return receipt, err
	}
	receipt.BackupSHA256, err = graphConversionDirectoryHash(filepath.Join(out, "backup"))
	if err != nil {
		return receipt, err
	}
	o.DataDir = graphConversionCandidateDir(receipt)
	if receipt.Plan.Store.PruneObsoleteCode {
		if err := graphConversionPruneCandidate(ctx, &receipt); err != nil {
			return receipt, err
		}
	}
	if err := graphstore.AdoptConversionCandidate(ctx, o, graphConversionAdoptionPlan(receipt), actor); err != nil {
		return receipt, err
	}
	receipt.CandidateState, err = graphstore.VerifyConversionCandidate(ctx, o, graphConversionAdoptionPlan(receipt))
	if err != nil {
		return receipt, err
	}
	if err := graphConversionValidateProjections(ctx, o); err != nil {
		return receipt, err
	}
	receipt.Phase = "prepared"
	if err := graphConversionWriteNew(filepath.Join(out, "receipt.json"), receipt); err != nil {
		return receipt, err
	}
	return receipt, nil
}

func graphConversionCopyFile(source, destination string, mode os.FileMode) (err error) {
	in, err := os.Open(source) // #nosec G304 -- only the operator selector and this executable are copied; receipt hashes verify the result
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, in.Close()) }()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode) // #nosec G304 -- private rehearsal path; exclusive creation refuses replacement
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func loadGraphConversionReceipt(cmd *cobra.Command) (receipt graphConversionReceipt, err error) {
	file, _ := cmd.Flags().GetString("receipt")
	if err := graphConversionReadJSON(file, &receipt); err != nil {
		return receipt, err
	}
	if receipt.Version != 1 || receipt.Phase != "prepared" || receipt.Plan.Version != 1 {
		return receipt, errors.New("receipt is not a completed rehearsal")
	}
	dir, err := graphConversionCanonicalPath(filepath.Dir(file))
	if err != nil {
		return receipt, err
	}
	if dir != receipt.Directory {
		return receipt, errors.New("receipt was moved from its bound rehearsal directory")
	}
	return receipt, nil
}

func graphConversionCheckSpace(source, parent string) error {
	var bytes uint64
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("conversion refuses non-regular data entry %q", path)
		}
		bytes += uint64(info.Size())
		return nil
	}); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	info, err := os.Stat(executable)
	if err != nil {
		return err
	}
	required := 2*bytes + uint64(info.Size()) + (4 << 20)
	available, err := graphConversionFreeBytes(parent)
	if err != nil {
		return err
	}
	if available < required {
		return fmt.Errorf("conversion checkpoint/candidate need at least %d bytes; only %d are available", required, available)
	}
	return nil
}

func graphConversionValidateProjections(ctx context.Context, o graphstore.Options) (err error) {
	s, err := graphstore.OpenExisting(ctx, o)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, s.Close()) }()
	plane, _, err := s.ReadContinuity(ctx)
	if err != nil {
		return err
	}
	if _, err := codeindex.Load(plane.Memories); err != nil {
		return fmt.Errorf("conversion code index: %w", err)
	}
	if _, err := knowledge.LoadCatalog(plane.Memories); err != nil {
		return fmt.Errorf("conversion document catalog: %w", err)
	}
	return nil
}

func graphConversionCandidateDir(receipt graphConversionReceipt) string {
	return filepath.Join(receipt.Directory, "candidate", "embeddeddolt")
}
func graphConversionAdoptionPlan(receipt graphConversionReceipt) graphstore.ConversionPlan {
	if receipt.AdoptionPlan != nil {
		return *receipt.AdoptionPlan
	}
	return receipt.Plan.Store
}
func graphConversionNativePlan(ctx context.Context, o graphstore.Options, prune bool) (graphstore.ConversionPlan, error) {
	if prune {
		return graphstore.PlanConversionForCodePrune(ctx, o)
	}
	return graphstore.PlanConversion(ctx, o)
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func graphConversionVerifyReceipt(ctx context.Context, receipt graphConversionReceipt) (result map[string]any, err error) {
	if err := graphConversionRecoveryArtifacts(receipt); err != nil {
		return nil, err
	}
	workspace := receipt.Plan.Store.Binding.WorkspaceID
	expectedSelector := receipt.Plan.MetadataSHA256
	phase := "prepared"
	path := graphConversionCandidateDir(receipt)
	converted := false
	if _, err := os.Lstat(filepath.Join(receipt.Directory, "activation.json")); err == nil {
		intent, err := graphConversionActivationIntent(receipt)
		if err != nil {
			return nil, err
		}
		if _, err := os.Lstat(filepath.Join(receipt.Directory, "rolled-back.json")); err == nil {
			phase = "rolled-back"
			_, staged, _, err := graphConversionActivationPaths(receipt)
			if err != nil {
				return nil, err
			}
			path = staged
		} else if !os.IsNotExist(err) {
			return nil, err
		} else {
			if _, err := os.Lstat(filepath.Join(receipt.Directory, "rollback.json")); !os.IsNotExist(err) {
				return nil, errors.New("rollback is incomplete; resume graph-convert rollback")
			}
			if _, err := os.Lstat(filepath.Join(receipt.Directory, "activated.json")); err != nil {
				return nil, errors.New("activation is incomplete; resume graph-convert activate")
			}
			phase = "active"
			path = receipt.Plan.Store.SourceDataDir
			converted = true
		}
		expected := receipt.Plan.MetadataSHA256
		if converted {
			expected = intent.GraphMetadataSHA256
		}
		expectedSelector = expected
		actual, err := graphConversionFileHash(filepath.Join(workspace, configfile.ConfigFileName))
		if err != nil || actual != expected {
			return nil, errors.New("live selector differs from completed conversion")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	gates, err := buildWorkspaceGateSet(workspace, path)
	if err != nil {
		return nil, err
	}
	handle, err := workspacegate.AcquireAll(ctx, workspacegate.Shared, workspacegate.Options{Reason: "graph-convert verify"}, gates...)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, handle.Release()) }()
	selector, err := graphConversionFileHash(filepath.Join(workspace, configfile.ConfigFileName))
	if err != nil || selector != expectedSelector {
		return nil, errors.New("selector changed before verification")
	}
	marker, err := graphConversionMarker(workspace)
	if err != nil {
		return nil, err
	}
	if converted && string(marker) != graphPreviewGeneration || !converted && len(marker) != 0 {
		return nil, errors.New("conversion format publication is incomplete")
	}
	if converted {
		_, _, native, err := graphConversionActivationPaths(receipt)
		if err != nil {
			return nil, err
		}
		if err := graphConversionNativeState(ctx, receipt, native); err != nil {
			return nil, err
		}
	}
	if !converted {
		if err := graphConversionNativeState(ctx, receipt, receipt.Plan.Store.SourceDataDir); err != nil {
			return nil, err
		}
	}
	if err := graphConversionGraphState(ctx, receipt, path); err != nil {
		return nil, err
	}
	return map[string]any{"verified": true, "phase": phase, "resources": receipt.Plan.Store.Resources, "original_converted": converted}, nil
}

// Seal native backup bytes after the successful restore, not just its presence.
func graphConversionDirectoryHash(root string) (string, error) {
	real, err := graphConversionCanonicalPath(root)
	if err != nil || real != root {
		return "", errors.New("backup root is not canonical")
	}
	h := sha256.New()
	encoder := json.NewEncoder(h)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("backup contains a non-regular entry")
		}
		digest, err := graphConversionFileHash(path)
		if err != nil {
			return err
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return encoder.Encode(struct {
			Path   string
			Size   int64
			SHA256 string
		}{filepath.ToSlash(name), info.Size(), digest})
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
