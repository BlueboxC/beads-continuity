package graphops

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestGeneratedResourceIDCandidateRootAndAlphabet(t *testing.T) {
	id, err := generatedResourceIDCandidate("notes/links", 6, bytes.NewReader([]byte{0, 1, 25, 26, 31, 255}))
	if err != nil || id != "notes/links/abz277" {
		t.Fatalf("candidate = %q, %v", id, err)
	}
	for n := 6; n <= 26; n++ {
		id, err := generatedResourceIDCandidate("", n, bytes.NewReader(bytes.Repeat([]byte{31}, n)))
		if err != nil || id != strings.Repeat("7", n) {
			t.Fatalf("length %d: %q, %v", n, id, err)
		}
	}
}

func TestGeneratedResourceIDCandidateRefusesInvalidRootAndEntropy(t *testing.T) {
	for _, root := range []string{"a/", "a//b", "..", "a?b", "a\\b"} {
		if _, err := generatedResourceIDCandidate(root, 6, bytes.NewReader(make([]byte, 6))); err == nil {
			t.Errorf("accepted root %q", root)
		}
	}
	for _, n := range []int{0, 5, 27} {
		if _, err := generatedResourceIDCandidate("", n, bytes.NewReader(nil)); err == nil {
			t.Errorf("accepted length %d", n)
		}
	}
	if _, err := generatedResourceIDCandidate("", 6, bytes.NewReader([]byte{1})); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("entropy error = %v", err)
	}
}

func TestGeneratedResourceIDRootBudget(t *testing.T) {
	root := strings.Repeat("a", GeneratedResourceIDRootMaxBytes)
	id, err := GeneratedResourceIDCandidate(root, GeneratedResourceIDMaxLength)
	if err != nil || len(id) != 1018 {
		t.Fatalf("maximum root: %d bytes, %v", len(id), err)
	}
	if _, err := GeneratedResourceIDCandidate(root+"a", GeneratedResourceIDInitialLength); err == nil {
		t.Fatal("overbudget root accepted")
	}
}
