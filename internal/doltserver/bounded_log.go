package doltserver

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// BoundedLog retains an active file and one tail, each bounded by its limit.
// The owner serializes file admission; writes and rotation share this mutex.
type BoundedLog struct {
	mu    sync.Mutex
	file  *os.File
	path  string
	limit int64
}

// OpenBoundedLog admits an owned regular log, rejecting symlink targets before
// writing or truncating them. A non-nil retention warning handler tolerates
// initial archive failures; subsequent writes retry rotation without growing
// the active file. Admission failures always return an error.
func OpenBoundedLog(path string, limit int64, onRetentionError func(error)) (*BoundedLog, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("bounded log limit must be positive")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 -- operator-selected proxy root
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	named, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || !named.Mode().IsRegular() || !os.SameFile(info, named) {
		_ = f.Close()
		return nil, fmt.Errorf("bounded log must be a regular file without symlinks: %s", path)
	}
	w := &BoundedLog{file: f, path: path, limit: limit}
	// The former startup-only policy could have moved an unbounded log to .1.
	archive := path + ".1"
	if old, err := os.Lstat(archive); err == nil && old.Mode().IsRegular() && old.Size() > limit {
		if err := retainBoundedTail(archive, archive, limit, old); err != nil {
			if onRetentionError == nil {
				_ = f.Close()
				return nil, err
			}
			onRetentionError(err)
		}
	}
	if info.Size() > limit {
		if err := w.rotate(); err != nil {
			if onRetentionError == nil {
				_ = f.Close()
				return nil, err
			}
			onRetentionError(err)
		}
	}
	return w, nil
}

func (w *BoundedLog) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	original := len(data)
	// One malformed diagnostic must not defeat the file bound.
	if int64(len(data)) > w.limit {
		data = data[:int(w.limit)]
	}
	// The proxy child may still hold the bootstrap append descriptor. Include
	// diagnostics written through it instead of trusting a cached byte count.
	info, err := w.file.Stat()
	if err != nil {
		return 0, err
	}
	if info.Size()+int64(len(data)) > w.limit {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(data)
	if err != nil {
		return n, err
	}
	return original, nil
}

func (w *BoundedLog) rotate() error {
	info, err := w.file.Stat()
	if err != nil {
		return err
	}
	if err := retainBoundedTail(w.path, w.path+".1", w.limit, info); err != nil {
		return err
	}
	// Windows O_APPEND handles lack FILE_WRITE_DATA, required by Truncate.
	// Keep append for output and truncate through a separately admitted handle.
	truncate, err := os.OpenFile(w.path, os.O_WRONLY, 0o600) // #nosec G304 -- admitted owner log path
	if err != nil {
		return err
	}
	defer func() { _ = truncate.Close() }()
	current, err := truncate.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, current) {
		return fmt.Errorf("bounded log replaced before truncation")
	}
	if err := truncate.Truncate(0); err != nil {
		return err
	}
	return nil
}

func retainBoundedTail(path, target string, limit int64, expected os.FileInfo) error {
	// Open a read handle to the admitted file; O_APPEND may not support ReadAt.
	// Refuse replacement before copying or truncating it.
	source, err := os.Open(path) // #nosec G304 -- admitted owner log path
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	actual, err := source.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(actual, expected) {
		return fmt.Errorf("bounded log replaced during rotation")
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".bounded-log-*")
	if err != nil {
		return err
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	start := actual.Size() - limit
	if start < 0 {
		start = 0
	}
	if _, err := io.Copy(tmp, io.NewSectionReader(source, start, limit)); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Close the historical archive's read handle before replacing it on Windows.
	if err := source.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}

func (w *BoundedLog) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}
