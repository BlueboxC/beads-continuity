package fix

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/steveyegge/beads/internal/storage/doltutil"
)

// repairTarget binds a confined path to the filesystem entries admitted for
// a repair. Mutations use the open parent so changing its name cannot redirect
// them to a replacement directory after an identity check.
type repairTarget struct {
	beadsDir, path string
	workspace, dir os.FileInfo
	original       os.FileInfo
	parent         *os.Root
}

func captureRepairTarget(beadsDir, databasePath string) (*repairTarget, error) {
	workspace, err := repairDirectoryIdentity(beadsDir)
	if err != nil {
		return nil, fmt.Errorf("inspecting workspace directory: %w", err)
	}
	databasePath, err = confinedDatabasePath(beadsDir, databasePath)
	if err != nil {
		return nil, err
	}
	target := &repairTarget{beadsDir: beadsDir, path: databasePath, workspace: workspace}
	parent, err := os.OpenRoot(filepath.Dir(databasePath))
	if os.IsNotExist(err) {
		// Fingerprint reinitialization can initialize an absent database.
		// Record the absent parent so a later appearance is a change.
		if err := target.validateOriginal(); err != nil {
			return nil, err
		}
		return target, nil
	}
	if err != nil {
		return nil, fmt.Errorf("opening database parent: %w", err)
	}
	target.parent = parent
	target.dir, err = parent.Stat(".")
	if err == nil {
		target.original, err = target.entry(filepath.Base(databasePath))
	}
	if err == nil {
		err = target.validateOriginal()
	}
	if err != nil {
		target.close()
		return nil, err
	}
	return target, nil
}

func (t *repairTarget) close() {
	if t.parent != nil {
		_ = t.parent.Close()
	}
}

// Obtain the identity from an open handle. In particular, Windows path-based
// FileInfo can defer loading its file ID until the first SameFile call.
func repairDirectoryIdentity(path string) (os.FileInfo, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.Stat(".")
}

func (t *repairTarget) entry(name string) (os.FileInfo, error) {
	if t.parent == nil {
		return nil, nil
	}
	entry, err := t.parent.Lstat(name)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return entry, err
}

func sameRepairEntry(a, b os.FileInfo) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return os.SameFile(a, b)
}

func (t *repairTarget) validateLocation() error {
	workspace, err := repairDirectoryIdentity(t.beadsDir)
	if err != nil {
		return fmt.Errorf("checking original workspace directory: %w", err)
	}
	if !os.SameFile(t.workspace, workspace) {
		return fmt.Errorf("workspace directory %s changed", doltutil.PrintedValue(t.beadsDir))
	}
	parentPath := filepath.Dir(t.path)
	parent, err := repairDirectoryIdentity(parentPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("checking original database parent: %w", err)
	}
	if !sameRepairEntry(t.dir, parent) {
		return fmt.Errorf("database parent %s changed", doltutil.PrintedValue(parentPath))
	}
	_, err = confinedDatabasePath(t.beadsDir, t.path)
	return err
}

func (t *repairTarget) validateOriginal() error {
	if err := t.validateLocation(); err != nil {
		return err
	}
	entry, err := t.entry(filepath.Base(t.path))
	if err != nil {
		return fmt.Errorf("checking original database entry: %w", err)
	}
	if !sameRepairEntry(t.original, entry) {
		return fmt.Errorf("database entry %s changed", doltutil.PrintedValue(t.path))
	}
	return nil
}

func (t *repairTarget) validateRecovery(backupPath string) error {
	if err := t.validateLocation(); err != nil {
		return err
	}
	if backupPath == t.path || filepath.Dir(backupPath) != filepath.Dir(t.path) {
		return fmt.Errorf("recovery entry is outside the admitted backup location")
	}
	backup, err := t.entry(filepath.Base(backupPath))
	if err != nil {
		return fmt.Errorf("checking recovery entry: %w", err)
	}
	if t.original == nil || !sameRepairEntry(t.original, backup) {
		return fmt.Errorf("original recovery entry is missing or was replaced")
	}
	return nil
}
