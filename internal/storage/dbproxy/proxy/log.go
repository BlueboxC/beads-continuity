package proxy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const maxProxyLogBytes int64 = 4 * 1024 * 1024

// proxyLog retains the active file and one bounded tail, including on startup
// with an oversized historical log. Rotation runs under the existing proxy lock.
type proxyLog struct {
	mu          sync.Mutex
	file        *os.File
	path        string
	size, limit int64
}

func openProxyLog(path string, limit int64) (*proxyLog, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("proxy log limit must be positive")
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
		return nil, fmt.Errorf("proxy log must be a regular file without symlinks: %s", path)
	}
	w := &proxyLog{file: f, path: path, size: info.Size(), limit: limit}
	if w.size > limit {
		if err := w.rotate(); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	return w, nil
}

func (w *proxyLog) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	original := len(data)
	// One malformed diagnostic must not defeat the file bound.
	if int64(len(data)) > w.limit {
		data = data[:int(w.limit)]
	}
	if w.size+int64(len(data)) > w.limit {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(data)
	w.size += int64(n)
	if err != nil {
		return n, err
	}
	return original, nil
}

func (w *proxyLog) rotate() error {
	// Open a read handle to the same admitted file; O_APPEND's write handle may
	// not support ReadAt. Refuse replacement before copying or truncating it.
	source, err := os.Open(w.path) // #nosec G304 -- admitted operator log path
	if err != nil {
		return err
	}
	defer source.Close()
	a, err := source.Stat()
	if err != nil {
		return err
	}
	b, err := w.file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(a, b) {
		return fmt.Errorf("proxy log replaced during rotation")
	}
	tmp, err := os.CreateTemp(filepath.Dir(w.path), ".proxy-log-*")
	if err != nil {
		return err
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	start := a.Size() - w.limit
	if start < 0 {
		start = 0
	}
	if _, err := io.Copy(tmp, io.NewSectionReader(source, start, w.limit)); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), w.path+".1"); err != nil {
		return err
	}
	if err := w.file.Truncate(0); err != nil {
		return err
	}
	w.size = 0
	return nil
}

func (w *proxyLog) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}
