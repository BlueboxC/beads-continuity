package graphstore

import (
	"database/sql"
	"sync"
)

// Read-only graph callbacks observe one immutable SQL snapshot. Cache only its
// validated binding, never current payloads or facts across transactions.
var readBindingSnapshots sync.Map // *sql.Tx -> *bindingSnapshot
type bindingSnapshot struct {
	mu        sync.Mutex
	validated bool
	options   Options
}

func bindReadSnapshot(tx *sql.Tx) func() {
	readBindingSnapshots.Store(tx, &bindingSnapshot{})
	return func() { readBindingSnapshots.Delete(tx) }
}
func checkedReadBinding(tx *sql.Tx, o Options) bool {
	v, ok := readBindingSnapshots.Load(tx)
	if !ok {
		return false
	}
	snapshot := v.(*bindingSnapshot)
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()
	return snapshot.validated && snapshot.options == o
}
func rememberReadBinding(tx *sql.Tx, o Options) {
	if v, ok := readBindingSnapshots.Load(tx); ok {
		snapshot := v.(*bindingSnapshot)
		snapshot.mu.Lock()
		defer snapshot.mu.Unlock()
		snapshot.options = o
		snapshot.validated = true
	}
}
