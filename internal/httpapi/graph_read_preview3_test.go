//go:build cgo

package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/httpapi/bdpwire"
	"github.com/steveyegge/beads/internal/httpapi/graphread"
	"github.com/steveyegge/beads/internal/storage/graphstore"
)

// The listener/store boundary must resolve public identities, preserve endpoint
// pins and refuse private locators. The optional server run also qualifies SQL.
func TestPreview3HTTPOpaqueIdentityAndIncidentRoutes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const scope = "https://preview3.example/read/"
	o := graphstore.Options{Backend: "embedded", DataDir: filepath.Join(work, "engine"), Database: "graph_http", Binding: graphstore.Binding{WorkspaceID: work, ScopeURL: scope, AuthorityID: "0123456789abcdef0123456789abcdef", SchemaVersion: graphstore.SchemaVersion}, OperationalProfile: graphstore.ContinuityProfile}
	if port, _ := strconv.Atoi(os.Getenv("BEADS_GRAPH_TEST_SERVER_PORT")); port != 0 {
		o.Backend = "server"
		o.ServerHost = "127.0.0.1"
		o.ServerPort = port
		o.ServerUser = "root"
		o.Database = fmt.Sprintf("graph_p3_%d", time.Now().UnixNano())
	}
	if err := graphstore.Init(ctx, o); err != nil {
		t.Fatal(err)
	}
	s, err := graphstore.OpenExisting(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	for i, id := range []string{"work", "links/literal-bead", "types/custom", "alias/real"} {
		if _, err := s.Create(ctx, graphstore.CreateRequest{Path: fmt.Sprintf("beads/private%d", i), PublicID: id, Title: id}); err != nil {
			t.Fatal(err)
		}
	}
	link, err := s.AddInformationalLink(ctx, graphstore.LinkCreateRequest{Path: "links/private", PublicID: "beads/literal-link", SourcePath: "beads/private0", TargetPath: "beads/private1", UnconditionalSource: true, TargetPin: graphstore.EndpointPin{Current: true}})
	if err != nil {
		t.Fatal(err)
	}
	g, err := NewGraphRead(graphread.New(s), scope)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	srv := newTestServer(t, Config{GraphRead: g})
	get := func(path string, want int) []byte {
		t.Helper()
		res, err := http.Get(srv.base + "/read/" + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != want {
			t.Fatalf("%s status=%d want=%d body=%s", path, res.StatusCode, want, raw)
		}
		return raw
	}
	for _, id := range []string{"work", "links/literal-bead", "types/custom", "alias/real"} {
		var b bdpwire.BeadRecord
		if err := bdpwire.Unmarshal(get(id, 200), &b); err != nil || b.ID != scope+id {
			t.Fatalf("%s record=%+v err=%v", id, b, err)
		}
		get(id+"?view=properties", 200)
		get(id+"?view=links", 200)
		get(id+"?include=links", 200)
	}
	var l bdpwire.LinkRecord
	if err := bdpwire.Unmarshal(get("beads/literal-link", 200), &l); err != nil || l.ID != link.Link.ID || l.Source.URI != scope+"work" || l.Target.URI != scope+"links/literal-bead" || l.Target.Revision != link.Link.TargetRevision {
		t.Fatalf("Link=%+v err=%v", l, err)
	}
	get("beads/literal-link?view=links", 400)
	for _, path := range []string{"beads/private0", "links/private", "missing", "work?revision=old", "work?view=events"} {
		want := 404
		if path == "work?revision=old" || path == "work?view=events" {
			want = 400
		}
		get(path, want)
	}
	for _, path := range []string{"beads/?limit=1", "links/?source=work", "links/?target=links%2Fliteral-bead", "types/"} {
		get(path, 200)
	}
	var page struct {
		Items []json.RawMessage `json:"items"`
		Next  string            `json:"next"`
	}
	if err := json.Unmarshal(get("work?view=links", 200), &page); err != nil || len(page.Items) != 1 {
		t.Fatalf("incident count=%d err=%v", len(page.Items), err)
	}
}
