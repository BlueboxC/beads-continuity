package server

import (
	"io"
	"log"
	"sync"
)

// backendLog drains both output pipes even after a disk/rotation failure. An
// os/exec copying error would otherwise stop the reader and break the backend's
// stdout/stderr. Keep the active diagnostics and report a failure once instead.
type backendLog struct {
	io.WriteCloser
	limit   int64
	warn    sync.Once
	onError func(error)
}

func (w *backendLog) Write(data []byte) (int, error) {
	n := len(data)
	if int64(n) > w.limit {
		// os/exec can coalesce many diagnostics into one pipe buffer. Retain
		// its newest bytes rather than losing the end of a large buffer.
		data = data[n-int(w.limit):]
	}
	if _, err := w.WriteCloser.Write(data); err != nil {
		w.report(err)
	}
	return n, nil
}

func (w *backendLog) report(err error) {
	w.warn.Do(func() {
		if w.onError != nil {
			w.onError(err)
		} else {
			log.Printf("server log write failed: %v", err)
		}
	})
}

type doltServerOptions struct {
	boundedLog bool
	onLogError func(error)
}

// DoltServerOption configures optional owned resources, without changing the
// existing constructor's treatment of operator-selected log paths.
type DoltServerOption func(*doltServerOptions)

// WithBoundedLog opts an owned server log into runtime size limits. The
// long-lived proxy process owns its output readers until cmd.Wait completes.
func WithBoundedLog() DoltServerOption {
	return func(opts *doltServerOptions) { opts.boundedLog = true }
}

// WithLogErrorHandler routes a failed log sink to the proxy's independent
// bounded diagnostic log, rather than appending a warning to that failed sink.
func WithLogErrorHandler(report func(error)) DoltServerOption {
	return func(opts *doltServerOptions) { opts.onLogError = report }
}
