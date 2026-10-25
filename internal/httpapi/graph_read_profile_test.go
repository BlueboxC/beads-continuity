package httpapi

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/httpapi/graphread"
	"github.com/steveyegge/beads/internal/storage/graphstore"
)

func TestContinuityHTTPProfileChecksEveryPageBeforePublishing(t *testing.T) {
	g := graphRouteFixture(t)
	g.pager.Close()
	o := graphread.DefaultPaginationOptions(g.scope)
	o.MaxSnapshotItems, o.MaxSnapshotBytes, o.MaxRetainedBytes = 16384, 64<<20, 128<<20
	var err error
	g.pager, err = graphread.NewPagination(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.pager.Close)
	g.boundedPages = true
	q, err := graphread.CompileCollection(g.scope, "beads", url.Values{"limit": {"2"}}, g.limits)
	if err != nil {
		t.Fatal(err)
	}
	record := func(n int) json.RawMessage {
		raw, err := json.Marshal(map[string]string{"body": strings.Repeat("x", n)})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	small, large := record(2<<20), record(4<<20)
	// A complete snapshot can exceed 7 MiB while every individual page fits.
	page, err := g.firstPage(q, []json.RawMessage{small, small, small, small, small})
	if err != nil || page.Next == nil {
		t.Fatalf("bounded pages refused: %v", err)
	}
	// The first page fits, but a later page does not. Refuse before issuing an
	// unreachable continuation, rather than fail after the client receives it.
	page, err = g.firstPage(q, []json.RawMessage{small, small, large, large})
	if !errors.Is(err, graphstore.ErrLimitExceeded) || page.Next != nil || len(page.Items) != 0 {
		t.Fatalf("unservable continuation issued: %+v %v", page, err)
	}
}
