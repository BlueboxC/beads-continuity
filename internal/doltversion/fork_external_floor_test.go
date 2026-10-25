package doltversion

import "testing"

func TestForkExternalVersionFloor(t *testing.T) {
	stub := writeVersionEchoStub(t, t.TempDir(), "dolt-floor", "dolt version 2.5.1")
	_, _, err := ProbeWithPolicy(t.Context(), stub)
	if err == nil {
		t.Fatal("recognized external Dolt 2.5.1 was admitted below the required 2.6.0 floor")
	}
}
