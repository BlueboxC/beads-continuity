package doctor

import (
	"os"
	"path/filepath"
	"testing"
)

func snapshotBeadsDir(t *testing.T, root string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name = filepath.ToSlash(name)
		if info.IsDir() {
			result[name] = "/"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[name] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
