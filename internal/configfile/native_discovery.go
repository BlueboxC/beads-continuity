package configfile

import (
	"fmt"
	"os"
	"path/filepath"
)

const GraphFormatMarkerFile = "graph-preview-format"

// LoadNativeForDiscovery inspects metadata without migrating or opening storage.
// Native issue writers and artifact cleanup must not reinterpret graph data.
func LoadNativeForDiscovery(beadsDir string) (*Config, error) {
	if _, err := os.Lstat(filepath.Join(beadsDir, GraphFormatMarkerFile)); !os.IsNotExist(err) {
		return nil, fmt.Errorf("native operation refuses graph-format marker or inspection failure at %s", beadsDir)
	}
	cfg, err := LoadForDiscovery(beadsDir)
	if err != nil {
		return nil, err
	}
	mode, err := cfg.GetGraphMode()
	if err != nil {
		return nil, err
	}
	if mode != GraphModeDependency {
		return nil, fmt.Errorf("native operation refuses workspace graph mode %q at %s", mode, beadsDir)
	}
	return cfg, nil
}
