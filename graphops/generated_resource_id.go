package graphops

import (
	"crypto/rand"
	"fmt"
	"io"
)

const (
	// GeneratedResourceIDInitialLength is the first allocation candidate size.
	GeneratedResourceIDInitialLength = 6
	// GeneratedResourceIDMaxLength bounds collision-driven lengthening.
	GeneratedResourceIDMaxLength = 26
	// GeneratedResourceIDRootMaxBytes leaves room for a separator, the longest
	// generated suffix and the private beads/ or links/ locator prefix.
	GeneratedResourceIDRootMaxBytes = 1024 - 6 - 1 - GeneratedResourceIDMaxLength
)

// ValidateGeneratedResourceIDRoot applies the shared generated-ID request budget.
// Empty means a flat generated ID. This is stricter than canonical ID syntax,
// whose general-purpose validator intentionally has no operational byte cap.
func ValidateGeneratedResourceIDRoot(root string) error {
	if root == "" {
		return nil
	}
	if err := ValidateCanonicalResourceID(root); err != nil {
		return err
	}
	if len(root) > GeneratedResourceIDRootMaxBytes {
		return fmt.Errorf("%w: generated ID root exceeds %d bytes", ErrValidation, GeneratedResourceIDRootMaxBytes)
	}
	return nil
}

// GeneratedResourceIDCandidate generates an already-canonical public ID. This
// is only a candidate: the writer must reserve the COMPLETE name atomically
// against lifetime allocations and authority/navigation identities, and retry
// on collision. Randomness alone does not guarantee uniqueness, particularly
// when independently written stores are later combined.
func GeneratedResourceIDCandidate(root string, length int) (string, error) {
	return generatedResourceIDCandidate(root, length, rand.Reader)
}

func generatedResourceIDCandidate(root string, length int, entropy io.Reader) (string, error) {
	if length < GeneratedResourceIDInitialLength || length > GeneratedResourceIDMaxLength {
		return "", fmt.Errorf("%w: generated ID length must be between %d and %d", ErrValidation, GeneratedResourceIDInitialLength, GeneratedResourceIDMaxLength)
	}
	if err := ValidateGeneratedResourceIDRoot(root); err != nil {
		return "", err
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(entropy, buf); err != nil {
		return "", err
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567"
	// A power-of-two alphabet makes masking unbiased and avoids shell
	// metacharacters. Only suffixes after @ are revision shorthand.
	for i, b := range buf {
		buf[i] = alphabet[b&31]
	}
	return MountCanonicalResourceID(root, string(buf))
}
