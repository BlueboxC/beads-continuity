package config

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestYamlUnsetSharedPathAllocationIsBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("remove: value\n")
	for depth := range 32 {
		fmt.Fprintf(&b, "%s%s:\n", strings.Repeat(" ", depth*2), strings.Repeat("k", 512))
	}
	for range 4096 {
		b.WriteString(strings.Repeat(" ", 64) + "- value\n")
	}
	before := b.String()
	after := "# remove: value\n" + strings.TrimPrefix(before, "remove: value\n")
	if _, err := yamlDocuments(before); err != nil {
		t.Fatal("invalid allocation fixture:", err)
	}
	runtime.GC()
	var start, end runtime.MemStats
	runtime.ReadMemStats(&start)
	err := collateralUnsetRefusal(before, after, "remove")
	runtime.ReadMemStats(&end)
	if err != nil {
		t.Fatal(err)
	}
	allocated := end.TotalAlloc - start.TotalAlloc
	t.Logf("input_bytes=%d allocated_bytes=%d", len(before), allocated)
	if allocated > 16<<20 {
		t.Fatalf("validating a shared YAML path allocated %d bytes for %d input bytes", allocated, len(before))
	}
	if err := collateralUnsetRefusal(before, strings.Replace(after, "- value", "- altered", 1), "remove"); err == nil {
		t.Fatal("accepted a collateral edit beside the removed key")
	}
}
