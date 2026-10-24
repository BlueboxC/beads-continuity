package graphops

import "fmt"

// NewPublicInScopeRef constructs a Preview3 identity. Endpoint kind is validated
// against stored affiliation; NewInScopeRef retains the schema8 rooted contract.
func NewPublicInScopeRef(path, pin string) (Ref, error) {
	if err := ValidateCanonicalResourceID(path); err != nil {
		return Ref{}, err
	}
	if err := validUTF8(pin, "a reference pin"); err != nil {
		return Ref{}, err
	}
	return Ref{inScope: true, path: path, pin: pin}, nil
}

// ParsePublicRef classifies canonical Preview3 IDs and exact Scope URLs.
func ParsePublicRef(scopeURL, reference, pin string) (Ref, error) {
	if err := ValidateScopeURL(scopeURL); err != nil {
		return Ref{}, err
	}
	if reference == "" {
		return Ref{}, fmt.Errorf("%w: reference must be a canonical local Bead ID or an absolute URI", ErrValidation)
	}
	if err := validUTF8(pin, "a reference pin"); err != nil {
		return Ref{}, err
	}
	if !isAbsoluteURI(reference) {
		return NewPublicInScopeRef(reference, pin)
	}
	if claimsScope(scopeURL, reference) {
		path, err := ScopeLocalPublicResourceID(scopeURL, reference)
		if err != nil {
			return Ref{}, fmt.Errorf("%w: reference %q claims the Scope but is not its canonical public identity", ErrValidation, reference)
		}
		return Ref{inScope: true, path: path, pin: pin}, nil
	}
	return Ref{uri: reference, pin: pin}, nil
}

// ValidateReferenceURI checks absolute-URI syntax without asserting authority.
func ValidateReferenceURI(uri string) error {
	if !isAbsoluteURI(uri) {
		return fmt.Errorf("%w: Reference requires an absolute URI", ErrValidation)
	}
	return nil
}

// NewPublicBead constructs a Preview3 Bead without inferring kind from its ID.
func NewPublicBead(spec BeadSpec) (Bead, error) {
	if err := ValidateCanonicalResourceID(spec.Path); err != nil {
		return Bead{}, err
	}
	return newBead(spec)
}

// NewPublicLink constructs a Preview3 Link without inferring kind from its ID.
func NewPublicLink(spec LinkSpec) (Link, error) {
	if err := ValidateCanonicalResourceID(spec.Path); err != nil {
		return Link{}, err
	}
	return newLink(spec)
}
