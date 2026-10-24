//go:build cgo

package graphstore

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/steveyegge/beads/internal/memoryapi"
)

// Imported empty values remain found bodies. This exercises the actual selected
// catalog rather than assuming that an empty summary is an admissible title.
func TestMemoryTitleDefaultSelectedCatalog(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, store := memoryDeleteStore(t, engine)
			for i, body := range []string{"", " \t\r\n"} {
				path := fmt.Sprintf("beads/empty-value-%d", i)
				created, err := store.Create(ctx, CreateRequest{Path: path, Title: memoryapi.TitleFromBody(body), Body: body})
				if err != nil {
					t.Fatal(err)
				}
				want := Properties{Title: "Untitled memory", Body: body}
				if created.Properties != want {
					t.Fatalf("creation lost independent title/body: %+v", created.Properties)
				}
				current, err := store.Show(ctx, path)
				if err != nil || current.Properties != want {
					t.Fatalf("read lost exact empty value: %+v %v", current, err)
				}
				changedBody := "new body"
				changed, err := store.PatchMemory(ctx, MemoryPatchRequest{Path: path, Body: &changedBody, ExpectedRevision: created.Revision})
				if err != nil || changed.Memory.Properties != (Properties{Title: want.Title, Body: changedBody}) {
					t.Fatalf("body update replaced omitted title: %+v %v", changed, err)
				}
			}
			explicit, err := store.Create(ctx, CreateRequest{Path: "beads/explicit-title", Title: "Chosen title", Body: ""})
			if err != nil || explicit.Properties != (Properties{Title: "Chosen title", Body: ""}) {
				t.Fatalf("explicit title changed: %+v %v", explicit, err)
			}
			before := workflowState(t, ctx, store)
			for _, title := range []string{"", " \t"} {
				if _, err := store.Create(ctx, CreateRequest{Path: "beads/refused-title", Title: title, Body: "body"}); err == nil {
					t.Fatalf("explicit blank title %q accepted", title)
				}
			}
			if !reflect.DeepEqual(before, workflowState(t, ctx, store)) {
				t.Fatal("blank-title refusal changed state")
			}
		})
	}
}
