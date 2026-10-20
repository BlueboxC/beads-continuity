// Package gittestenv isolates Git fixture setup from the invoking process.
package gittestenv

import (
	"os"
	"strings"
)

// Clear removes inherited Git routing and configuration before TestMain setup.
// Individual tests may then install the explicit environment they exercise.
func Clear() {
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(name), "GIT_") {
			_ = os.Unsetenv(name)
		}
	}
	_ = os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	_ = os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	_ = os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
}
