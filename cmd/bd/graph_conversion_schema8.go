package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/graphstore"
)

func graphConversionSchema8CopyCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "schema8-copy", Short: "Rehearse full-history schema8 conversion from an explicit isolated copy", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			CheckReadonly("graph-convert schema8-copy")
			ctx, cancel := context.WithTimeout(getRootContext(), 15*time.Minute)
			defer cancel()
			receipt, err := runSchema8Copy(ctx, cmd)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(receipt)
		}}
	cmd.Flags().String("source", "", "Coherent copied embedded engine directory, outside the bound workspace")
	cmd.Flags().String("metadata", "", "Copied original schema8 metadata.json")
	cmd.Flags().String("out", "", "New private rehearsal directory; never a real workspace")
	return cmd
}

func runSchema8Copy(ctx context.Context, cmd *cobra.Command) (receipt graphConversionReceipt, err error) {
	source, _ := cmd.Flags().GetString("source")
	source, err = graphConversionCanonicalPath(source)
	if err != nil {
		return receipt, err
	}
	metadata, _ := cmd.Flags().GetString("metadata")
	metadata, err = graphConversionCanonicalPath(metadata)
	if err != nil {
		return receipt, err
	}
	info, err := os.Stat(metadata)
	if err != nil {
		return receipt, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return receipt, errors.New("copied metadata must be a regular file of at most 1 MiB")
	}
	file, err := os.Open(metadata) // #nosec G304 -- explicit canonical copied selector; bounded descriptor read, never a project-supplied executable
	if err != nil {
		return receipt, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err = errors.Join(readErr, file.Close()); err != nil {
		return receipt, err
	}
	if len(raw) > 1<<20 {
		return receipt, errors.New("copied metadata exceeds 1 MiB")
	}
	var cfg configfile.Config
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return receipt, err
	}
	if cfg.GraphSchemaVersion != 8 || cfg.DoltMode != configfile.DoltModeEmbedded {
		return receipt, errors.New("schema8-copy requires copied embedded schema8 metadata")
	}
	o := graphOptionsFor(filepath.Dir(metadata), &cfg)
	o.DataDir = source
	o.Binding.SchemaVersion = graphstore.SchemaVersion
	plan, err := graphstore.PlanSchema8Conversion(ctx, o)
	if err != nil {
		return receipt, err
	}
	out, _ := cmd.Flags().GetString("out")
	out, err = graphConversionNewPath(out)
	if err != nil {
		return receipt, err
	}
	for _, protected := range []string{source, cfg.GraphWorkspace} {
		if graphstore.ConversionPathsOverlap(out, protected) {
			return receipt, errors.New("rehearsal output overlaps source/workspace")
		}
	}
	if err = graphConversionCheckSpace(source, filepath.Dir(out)); err != nil {
		return receipt, err
	}
	if err = os.Mkdir(out, 0700); err != nil {
		return receipt, err
	}
	receipt.Version = 1
	receipt.Phase = "preparing-schema8-copy"
	receipt.Directory = out
	receipt.Plan.Version = 1
	receipt.Plan.Store = plan
	receipt.Plan.BinarySHA256, err = graphConversionBinaryHash()
	if err != nil {
		return receipt, err
	}
	receipt.Plan.MetadataSHA256, err = graphConversionFileHash(metadata)
	if err != nil {
		return receipt, err
	}
	if err = graphConversionWriteNew(filepath.Join(out, "preparation.json"), receipt); err != nil {
		return receipt, err
	}
	if err = graphConversionCopyFile(metadata, filepath.Join(out, "original-metadata.json"), 0600); err != nil {
		return receipt, err
	}
	backup, candidate := filepath.Join(out, "backup"), filepath.Join(out, "candidate")
	if err = graphstore.CheckpointConversion(ctx, o, plan, backup, candidate); err != nil {
		return receipt, err
	}
	receipt.BackupSHA256, err = graphConversionDirectoryHash(backup)
	if err != nil {
		return receipt, err
	}
	o.DataDir = candidate
	if err = graphstore.UpgradeSchema8Candidate(ctx, o, plan, "copy-conversion"); err != nil {
		return receipt, err
	}
	receipt.CandidateState, err = graphstore.VerifySchema8Candidate(ctx, o, plan)
	if err != nil {
		return receipt, err
	}
	if err = graphConversionValidateProjections(ctx, o); err != nil {
		return receipt, err
	}
	receipt.Phase = "prepared-schema8-copy"
	if err = graphConversionWriteNew(filepath.Join(out, "receipt.json"), receipt); err != nil {
		return receipt, err
	}
	return receipt, nil
}
