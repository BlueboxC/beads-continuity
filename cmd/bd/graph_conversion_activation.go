package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/migration"
	"github.com/steveyegge/beads/internal/storage/graphstore"
)

const graphConversionPending = "graph-conversion-incomplete\n"

type graphConversionActivation struct {
	Version             int    `json:"version"`
	ReceiptSHA256       string `json:"receipt_sha256"`
	GraphMetadataSHA256 string `json:"graph_metadata_sha256"`
}

func graphConversionStep(after func(string) error, step string) error {
	if after == nil {
		return nil
	}
	return after(step)
}

func graphConversionSyncDir(path string) (err error) {
	if runtime.GOOS == "windows" {
		return errors.New("graph conversion publication is not qualified on Windows")
	}
	f, err := os.Open(path) // #nosec G304 -- fsync the validated conversion directory; no payload-selected path
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	return f.Sync()
}

func graphConversionReplace(path string, data []byte) (err error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("publication target must be an existing regular file: %s", path)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".graph-conversion-")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer func() { _ = os.Remove(temp) }()
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		return err
	}
	return graphConversionSyncDir(filepath.Dir(path))
}

func graphConversionExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("conversion engine path is not a real directory: %s", path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil || real != path {
		return false, fmt.Errorf("conversion engine path is not canonical: %s", path)
	}
	return true, nil
}

func graphConversionMove(source, destination string) error {
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return fmt.Errorf("conversion destination must not exist: %s", destination)
	}
	if err := os.Rename(source, destination); err != nil {
		return fmt.Errorf("conversion requires same-filesystem atomic rename; source retained: %w", err)
	}
	return errors.Join(graphConversionSyncDir(filepath.Dir(source)), graphConversionSyncDir(filepath.Dir(destination)))
}

func graphConversionActivationPaths(receipt graphConversionReceipt) (source, staged, native string, err error) {
	o := graphConversionOptions(receipt.Plan)
	workspace, err := graphConversionCanonicalPath(o.Binding.WorkspaceID)
	if err != nil || workspace != o.Binding.WorkspaceID || filepath.Base(workspace) != ".beads" || o.DataDir != filepath.Join(workspace, "embeddeddolt") {
		return "", "", "", errors.New("conversion receipt must bind the canonical .beads/embeddeddolt source")
	}
	id, err := hexDecodeAuthority(o.Binding.AuthorityID)
	if err != nil {
		return "", "", "", err
	}
	return o.DataDir, filepath.Join(workspace, "graph-conversion-"+id+"-candidate"), filepath.Join(workspace, "graph-conversion-"+id+"-native"), nil
}

func hexDecodeAuthority(id string) (string, error) {
	// Authority is also checked by storage; constrain it before using it in paths.
	if len(id) != 32 {
		return "", errors.New("invalid conversion authority")
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return "", errors.New("invalid conversion authority")
		}
	}
	return id, nil
}

func graphConversionCheckArtifacts(receipt graphConversionReceipt) error {
	binary, err := graphConversionBinaryHash()
	if err != nil {
		return err
	}
	if binary != receipt.Plan.BinarySHA256 {
		return errors.New("conversion requires the exact planned binary")
	}
	return graphConversionRecoveryArtifacts(receipt)
}

func graphConversionRecoveryArtifacts(receipt graphConversionReceipt) error {
	for _, file := range []struct{ name, hash string }{{"original-metadata.json", receipt.Plan.MetadataSHA256}, {"rollback-bd", receipt.Plan.BinarySHA256}} {
		got, err := graphConversionFileHash(filepath.Join(receipt.Directory, file.name))
		if err != nil || got != file.hash {
			return fmt.Errorf("conversion recovery artifact differs: %s", file.name)
		}
	}
	hash, err := graphConversionDirectoryHash(filepath.Join(receipt.Directory, "backup"))
	if err != nil || hash != receipt.BackupSHA256 {
		return errors.New("native backup differs from its verified seal")
	}
	return nil
}

func graphConversionNativeState(ctx context.Context, receipt graphConversionReceipt, path string) error {
	o := graphConversionOptions(receipt.Plan)
	o.DataDir = path
	state, err := graphstore.InspectConversionState(ctx, o)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(state, receipt.Plan.Store.State) {
		return errors.New("native recovery state changed; refusing replacement")
	}
	return nil
}

func graphConversionGraphState(ctx context.Context, receipt graphConversionReceipt, path string) error {
	o := graphConversionOptions(receipt.Plan)
	o.DataDir = path
	observed, err := graphstore.InspectConversionState(ctx, o)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(observed, receipt.CandidateState) {
		return errors.New("graph changed since rehearsal; refusing to overwrite later writes")
	}
	state, err := graphstore.VerifyConversionCandidate(ctx, o, graphConversionAdoptionPlan(receipt))
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(state, receipt.CandidateState) {
		return errors.New("graph changed since rehearsal; refusing to overwrite later writes")
	}
	return graphConversionValidateProjections(ctx, o)
}

func graphConversionActivationIntent(receipt graphConversionReceipt) (intent graphConversionActivation, err error) {
	intentFile := filepath.Join(receipt.Directory, "activation.json")
	receiptHash, err := graphConversionFileHash(filepath.Join(receipt.Directory, "receipt.json"))
	if err != nil {
		return intent, err
	}
	if _, err := os.Lstat(intentFile); err == nil {
		if err := graphConversionReadJSON(intentFile, &intent); err != nil {
			return intent, err
		}
		if intent.Version != 1 || intent.ReceiptSHA256 != receiptHash {
			return intent, errors.New("activation intent differs from sealed rehearsal")
		}
		got, err := graphConversionFileHash(filepath.Join(receipt.Directory, "graph-metadata.json"))
		if err != nil || got != intent.GraphMetadataSHA256 {
			return intent, errors.New("activation metadata differs from sealed intent")
		}
		return intent, nil
	} else if !os.IsNotExist(err) {
		return intent, err
	}
	raw, err := os.ReadFile(filepath.Join(receipt.Directory, "original-metadata.json")) // #nosec G304 -- fixed recovery artifact in the bound private receipt directory; hash checked before publication
	if err != nil {
		return intent, err
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return intent, err
	}
	binding := receipt.Plan.Store.Binding
	for key, value := range map[string]any{"graph_mode": "link", "graph_scope_url": binding.ScopeURL, "graph_authority_id": binding.AuthorityID, "graph_workspace": binding.WorkspaceID, "graph_schema_version": binding.SchemaVersion, "graph_ready": true} {
		encoded, err := json.Marshal(value)
		if err != nil {
			return intent, err
		}
		cfg[key] = encoded
	}
	path := filepath.Join(receipt.Directory, "graph-metadata.json")
	// A crash before sealing intent may leave this deterministic file.
	expected, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return intent, err
	}
	expected = append(expected, '\n')
	if raw, readErr := os.ReadFile(path); readErr == nil { // #nosec G304 -- fixed activation artifact in the bound private receipt directory; exact bytes compared
		if !reflect.DeepEqual(raw, expected) {
			return intent, errors.New("unsealed activation metadata differs")
		}
	} else if os.IsNotExist(readErr) {
		if err := graphConversionWriteNew(path, cfg); err != nil {
			return intent, err
		}
	} else {
		return intent, readErr
	}
	hash, err := graphConversionFileHash(path)
	if err != nil {
		return intent, err
	}
	intent = graphConversionActivation{Version: 1, ReceiptSHA256: receiptHash, GraphMetadataSHA256: hash}
	if err := graphConversionWriteNew(intentFile, intent); err != nil {
		return intent, err
	}
	return intent, graphConversionSyncDir(receipt.Directory)
}

func graphConversionMarker(workspace string) ([]byte, error) {
	marker, err := os.ReadFile(filepath.Join(workspace, graphPreviewMarker)) // #nosec G304 -- fixed format marker in the canonical operator workspace
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if string(marker) != graphConversionPending && string(marker) != graphPreviewGeneration {
		return nil, errors.New("unknown conversion format marker; refusing recovery")
	}
	return marker, nil
}

// Recovery infers the next step only from the sealed intent and exact engine
// fingerprints. Every filesystem transition is repeatable after interruption.
func runGraphConversionActivate(ctx context.Context, receipt graphConversionReceipt, after func(string) error) (err error) {
	if runtime.GOOS == "windows" {
		return errors.New("conversion activation is not qualified on Windows")
	}
	source, staged, native, err := graphConversionActivationPaths(receipt)
	if err != nil {
		return err
	}
	workspace := receipt.Plan.Store.Binding.WorkspaceID
	if freeze := migration.Find(workspace); freeze.Frozen() {
		return errors.New("conversion refuses a frozen or unreadable migration boundary")
	}
	if err := graphConversionCheckArtifacts(receipt); err != nil {
		return err
	}
	handle, err := acquireExclusiveWorkspaceGates(ctx, workspace, "graph-convert activate/recover", source, staged, native, graphConversionCandidateDir(receipt))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, handle.Release()) }()
	if _, err := os.Lstat(filepath.Join(receipt.Directory, "rollback.json")); !os.IsNotExist(err) {
		return errors.New("rollback has begun; finish rollback instead of activating")
	}
	originalExists, err := graphConversionExists(native)
	if err != nil {
		return err
	}
	sourceExists, err := graphConversionExists(source)
	if err != nil {
		return err
	}
	stagedExists, err := graphConversionExists(staged)
	if err != nil {
		return err
	}
	candidate := graphConversionCandidateDir(receipt)
	candidateExists, err := graphConversionExists(candidate)
	if err != nil {
		return err
	}
	marker, err := graphConversionMarker(workspace)
	if err != nil {
		return err
	}
	if !originalExists {
		if !sourceExists {
			return errors.New("original native engine is missing")
		}
		if err := graphConversionNativeState(ctx, receipt, source); err != nil {
			return err
		}
		hash, err := graphConversionFileHash(filepath.Join(workspace, configfile.ConfigFileName))
		if err != nil || hash != receipt.Plan.MetadataSHA256 {
			return errors.New("native selector changed since planning")
		}
	} else {
		if err := graphConversionNativeState(ctx, receipt, native); err != nil {
			return err
		}
		if len(marker) == 0 {
			return errors.New("native recovery engine exists without the conversion fence")
		}
	}
	if candidateExists == stagedExists && (!originalExists || !sourceExists) {
		return errors.New("candidate layout is incomplete or ambiguous")
	}
	if candidateExists {
		if originalExists || stagedExists || len(marker) != 0 {
			return errors.New("unexpected prepared candidate layout")
		}
		if err := graphConversionGraphState(ctx, receipt, candidate); err != nil {
			return err
		}
	} else if stagedExists {
		if sourceExists && originalExists {
			return errors.New("two candidate engines are present")
		}
		if err := graphConversionGraphState(ctx, receipt, staged); err != nil {
			return err
		}
	} else if originalExists && sourceExists {
		if err := graphConversionGraphState(ctx, receipt, source); err != nil {
			return err
		}
	} else {
		return errors.New("prepared graph engine is missing")
	}
	intent, err := graphConversionActivationIntent(receipt)
	if err != nil {
		return err
	}
	selectorHash, err := graphConversionFileHash(filepath.Join(workspace, configfile.ConfigFileName))
	if err != nil {
		return err
	}
	if selectorHash != receipt.Plan.MetadataSHA256 && selectorHash != intent.GraphMetadataSHA256 {
		return errors.New("selector differs from sealed native and graph metadata")
	}
	if err := graphConversionStep(after, "intent"); err != nil {
		return err
	}
	if candidateExists {
		if err := graphConversionMove(candidate, staged); err != nil {
			return err
		}
		stagedExists = true
		if err := graphConversionStep(after, "candidate-staged"); err != nil {
			return err
		}
	}
	markerPath := filepath.Join(workspace, graphPreviewMarker)
	if len(marker) == 0 {
		f, err := os.OpenFile(markerPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600) // #nosec G304 -- fixed marker in the validated workspace; exclusive creation refuses replacement
		if err != nil {
			return err
		}
		_, writeErr := f.WriteString(graphConversionPending)
		syncErr := f.Sync()
		closeErr := f.Close()
		if err := errors.Join(writeErr, syncErr, closeErr, graphConversionSyncDir(workspace)); err != nil {
			return err
		}
		if err := graphConversionStep(after, "fenced"); err != nil {
			return err
		}
	}
	if !originalExists {
		if err := graphConversionMove(source, native); err != nil {
			return err
		}
		sourceExists = false
		if err := graphConversionStep(after, "native-retained"); err != nil {
			return err
		}
	}
	if !sourceExists && stagedExists {
		if err := graphConversionMove(staged, source); err != nil {
			return err
		}
		if err := graphConversionStep(after, "graph-installed"); err != nil {
			return err
		}
	}
	if err := graphConversionGraphState(ctx, receipt, source); err != nil {
		return err
	}
	metadata := filepath.Join(workspace, configfile.ConfigFileName)
	hash, err := graphConversionFileHash(metadata)
	if err != nil {
		return err
	}
	if hash != intent.GraphMetadataSHA256 {
		if hash != receipt.Plan.MetadataSHA256 {
			return errors.New("selector differs from both sealed native and graph metadata")
		}
		raw, err := os.ReadFile(filepath.Join(receipt.Directory, "graph-metadata.json")) // #nosec G304 -- fixed private artifact; sealed hash verified before selector publication
		if err != nil {
			return err
		}
		if err := graphConversionReplace(metadata, raw); err != nil {
			return err
		}
		if err := graphConversionStep(after, "selector-published"); err != nil {
			return err
		}
	}
	if err := graphConversionReplace(markerPath, []byte(graphPreviewGeneration)); err != nil {
		return err
	}
	if err := graphConversionStep(after, "format-published"); err != nil {
		return err
	}
	complete := filepath.Join(receipt.Directory, "activated.json")
	if _, err := os.Lstat(complete); os.IsNotExist(err) {
		if err := graphConversionWriteNew(complete, intent); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		var got graphConversionActivation
		if err := graphConversionReadJSON(complete, &got); err != nil {
			return err
		}
		if got != intent {
			return errors.New("activation completion differs from intent")
		}
	}
	return graphConversionSyncDir(receipt.Directory)
}

func runGraphConversionRollback(ctx context.Context, receipt graphConversionReceipt, after func(string) error) (err error) {
	if runtime.GOOS == "windows" {
		return errors.New("conversion rollback is not qualified on Windows")
	}
	source, staged, native, err := graphConversionActivationPaths(receipt)
	if err != nil {
		return err
	}
	workspace := receipt.Plan.Store.Binding.WorkspaceID
	if freeze := migration.Find(workspace); freeze.Frozen() {
		return errors.New("rollback refuses a frozen or unreadable migration boundary")
	}
	if err := graphConversionCheckArtifacts(receipt); err != nil {
		return err
	}
	handle, err := acquireExclusiveWorkspaceGates(ctx, workspace, "graph-convert rollback/recover", source, staged, native)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, handle.Release()) }()
	var intent graphConversionActivation
	if err := graphConversionReadJSON(filepath.Join(receipt.Directory, "activation.json"), &intent); err != nil {
		return err
	}
	if intent, err = graphConversionActivationIntent(receipt); err != nil {
		return err
	}
	marker, err := graphConversionMarker(workspace)
	if err != nil {
		return err
	}
	sourceExists, err := graphConversionExists(source)
	if err != nil {
		return err
	}
	stagedExists, err := graphConversionExists(staged)
	if err != nil {
		return err
	}
	nativeExists, err := graphConversionExists(native)
	if err != nil {
		return err
	}
	rollbackPath := filepath.Join(receipt.Directory, "rollback.json")
	var rollback graphConversionActivation
	rollbackErr := graphConversionReadJSON(rollbackPath, &rollback)
	if rollbackErr != nil && !os.IsNotExist(rollbackErr) {
		return rollbackErr
	}
	if rollbackErr == nil && rollback != intent {
		return errors.New("rollback intent differs from activation")
	}
	if nativeExists {
		if len(marker) == 0 {
			return errors.New("native recovery engine exists without the conversion fence")
		}
		if err := graphConversionNativeState(ctx, receipt, native); err != nil {
			return err
		}
		if sourceExists == stagedExists {
			return errors.New("rollback candidate layout is ambiguous")
		}
		graphPath := source
		if stagedExists {
			graphPath = staged
		}
		if err := graphConversionGraphState(ctx, receipt, graphPath); err != nil {
			return err
		}
	} else {
		if rollbackErr != nil || !sourceExists || !stagedExists {
			return errors.New("native recovery engine is missing")
		}
		if err := graphConversionNativeState(ctx, receipt, source); err != nil {
			return err
		}
		if err := graphConversionGraphState(ctx, receipt, staged); err != nil {
			return err
		}
	}
	metadata := filepath.Join(workspace, configfile.ConfigFileName)
	hash, err := graphConversionFileHash(metadata)
	if err != nil {
		return err
	}
	if hash != intent.GraphMetadataSHA256 && hash != receipt.Plan.MetadataSHA256 {
		return errors.New("rollback selector differs from sealed metadata")
	}
	if rollbackErr != nil {
		if err := graphConversionWriteNew(rollbackPath, intent); err != nil {
			return err
		}
		if err := graphConversionSyncDir(receipt.Directory); err != nil {
			return err
		}
	}
	if err := graphConversionStep(after, "rollback-intent"); err != nil {
		return err
	}
	if len(marker) != 0 {
		if err := graphConversionReplace(filepath.Join(workspace, graphPreviewMarker), []byte(graphConversionPending)); err != nil {
			return err
		}
	}
	if nativeExists {
		if sourceExists {
			if err := graphConversionMove(source, staged); err != nil {
				return err
			}
			if err := graphConversionStep(after, "graph-retained"); err != nil {
				return err
			}
		}
		if err := graphConversionMove(native, source); err != nil {
			return err
		}
		if err := graphConversionStep(after, "native-restored"); err != nil {
			return err
		}
	}
	if hash != receipt.Plan.MetadataSHA256 {
		raw, err := os.ReadFile(filepath.Join(receipt.Directory, "original-metadata.json")) // #nosec G304 -- fixed recovery artifact in the bound private receipt directory; hash checked before publication
		if err != nil {
			return err
		}
		if err := graphConversionReplace(metadata, raw); err != nil {
			return err
		}
		if err := graphConversionStep(after, "native-selector-restored"); err != nil {
			return err
		}
	}
	if err := graphConversionNativeState(ctx, receipt, source); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(workspace, graphPreviewMarker)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := graphConversionSyncDir(workspace); err != nil {
		return err
	}
	if err := graphConversionStep(after, "native-format-restored"); err != nil {
		return err
	}
	complete := filepath.Join(receipt.Directory, "rolled-back.json")
	if _, err := os.Lstat(complete); os.IsNotExist(err) {
		if err := graphConversionWriteNew(complete, intent); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		var got graphConversionActivation
		if err := graphConversionReadJSON(complete, &got); err != nil {
			return err
		}
		if got != intent {
			return errors.New("rollback completion differs from intent")
		}
	}
	return graphConversionSyncDir(receipt.Directory)
}

func graphConversionPublicationCommand(rollback bool) *cobra.Command {
	name := "activate"
	short := "Explicitly publish or resume a sealed graph conversion"
	if rollback {
		name = "rollback"
		short = "Restore the native format only when the graph has no later writes"
	}
	cmd := &cobra.Command{Use: name, Short: short, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		CheckReadonly("graph-convert " + name)
		stopped, _ := cmd.Flags().GetBool("writers-stopped")
		if !stopped {
			return errors.New(name + " requires --writers-stopped; stop all clients/hooks/viewers first")
		}
		receipt, err := loadGraphConversionReceipt(cmd)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(getRootContext(), 5*time.Minute)
		defer cancel()
		if rollback {
			err = runGraphConversionRollback(ctx, receipt, nil)
		} else {
			err = runGraphConversionActivate(ctx, receipt, nil)
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"verified": true, "phase": name, "original_converted": !rollback})
	}}
	cmd.Flags().String("receipt", "", "Private completed rehearsal receipt")
	cmd.Flags().Bool("writers-stopped", false, "Assert all clients/hooks/viewers are stopped; does not stop them")
	return cmd
}
