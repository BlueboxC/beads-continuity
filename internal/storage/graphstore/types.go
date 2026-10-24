// Package graphstore implements explicit typed graph persistence and copy-only
// conversion over the fork's ordinary Dolt engine. It grants no project authority.
package graphstore

import (
	"encoding/json"
	"errors"

	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

// SchemaVersion identifies this explicitly experimental storage layout.
const SchemaVersion = 11

// TypeGeneration identifies the selected fresh graph contract, including its
// Type installation and public identity semantics. It never migrates stores.
const TypeGeneration = 13

// Binding is the exact identity expected by the local workspace metadata.
// WorkspaceID is its canonical filesystem path; C0 does not support moving it.
type Binding struct {
	WorkspaceID   string
	ScopeURL      string
	AuthorityID   string
	SchemaVersion int
}

// Options selects an explicit backend. Existing opens never provision or migrate.
// DataDir is the already-created embedded engine directory, not a second DB.
type Options struct {
	// AssessmentsEnabled admits assessment authoring; definitions and reads remain intact.
	AssessmentsEnabled bool

	Backend  string
	DataDir  string
	Database string
	Branch   string
	Binding  Binding
	// TypeGeneration is the admitted workspace marker generation. Zero lets
	// direct storage callers inspect the checked fresh installation; the CLI
	// supplies TypeGeneration and requires that marker's exact installed catalog.
	TypeGeneration int
	ServerHost     string
	ServerPort     int
	ServerUser     string
	ServerPassword string
	ServerSocket   string
	ServerTLS      bool
	// IssuePrefix is resolved by normal CLI initialization. It is used only
	// for fresh bootstrap; existing opens read the persisted config value.
	IssuePrefix string
}

// CreateRequest creates a new canonical Memory path; it is not a keyed upsert.
type CreateRequest struct {
	// PublicID is an exact user identity; Path is its private locator. Empty
	// retains the legacy allocation-path public identity.
	PublicID string
	// IDRoot prefixes an automatically generated public ID, never an explicit ID.
	IDRoot               string
	generatePublicID     bool
	privatePathGenerated bool
	TypeURL              string
	PropertiesJSON       json.RawMessage // complete object; exclusive with nonempty legacy Title/Body
	Path                 string
	Title                string
	Body                 string
	Actor                string
	Metadata             json.RawMessage
}

// Properties retains the common Memory fields and every other authored property.
// Extra fields are held as canonical JSON so value copies remain independent
// and comparable. JSON construction preserves values without numeric conversion.
type Properties struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	extra string
}

// Record is a complete preview Memory record, including its outgoing owned Links.
// Version identifies retained state; it is not a local ordinal or timestamp.
type Record struct {
	// PublicID is checked allocation data for local CLI projection, not BDP wire.
	PublicID    string            `json:"-"`
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	Revision    string            `json:"revision"`
	Version     string            `json:"version"`
	Properties  Properties        `json:"properties"`
	Metadata    json.RawMessage   `json:"metadata"`
	Owned       []json.RawMessage `json:"owned"`
	Attribution Attribution       `json:"attribution"`
}

// Attribution records only what this write supplies. RecordedAt is an observed
// UTC wall-clock time, not an acceptance-order token or an as-of boundary.
// HistoricalUpdateContext distinguishes the retained content source from the actual
// predecessor replaced by this operation. It is output-only, per-version state.
type HistoricalUpdateContext struct {
	FromRevision     string `json:"fromRevision"`
	PreviousRevision string `json:"previousRevision"`
}

type Attribution struct {
	Inception        string                   `json:"inception,omitempty"`
	Actor            string                   `json:"actor"`
	Status           string                   `json:"status"`
	RecordedAt       string                   `json:"recordedAt"`
	HistoricalUpdate *HistoricalUpdateContext `json:"historicalUpdate,omitempty"`
}

// IssueRecord projects the authoritative native Issue aggregate. Type is the
// Scope-local concrete declaration derived from its native issue_type column.
// Exact retained records use their retained classification, never the current one.
type IssueRecord struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	Revision    string            `json:"revision"`
	Version     string            `json:"version"`
	Properties  *publicops.Issue  `json:"properties"`
	Metadata    json.RawMessage   `json:"metadata"`
	Owned       []json.RawMessage `json:"owned"`
	Attribution Attribution       `json:"attribution"`
}

var (
	ErrInvalidStore   = errors.New("graph preview store identity or schema is invalid")
	ErrAlreadyExists  = errors.New("canonical graph path has already been allocated")
	ErrNotFound       = errors.New("graph resource not found")
	ErrGone           = errors.New("graph resource was deleted")
	ErrConflict       = errors.New("graph transaction conflicted")
	ErrOutcomeUnknown = errors.New("graph transaction outcome is unknown; do not replay automatically")
)

// DependencyRequest asserts one Issue-owned native Dependency between live
// local Issues. Type preserves the legacy blocking-only caller contract and
// rejects unsupported kinds; TypeURL selects an installed canonical Link Type
// in the workspace generation. If both are set, they must agree. Path is an
// optional never-reused Link allocation; empty allocates a fresh path.
type DependencyRequest struct {
	Metadata json.RawMessage
	ThreadID string

	// PublicID is exact public identity; Path is only the private allocation locator.
	PublicID string
	IDRoot   string
	// ExpectedSourceRevision guards generic authoring; empty retains the legacy command contract.
	ExpectedSourceRevision string
	Type                   types.DependencyType
	TypeURL                string
	SourcePath             string
	TargetPath             string
	// Native IDs select opaque public Issue IDs inside the checked write.
	// Empty IDs keep canonical graph paths exact for existing callers.
	SourceNativeID string
	TargetNativeID string
	Path           string
	Actor          string
}

// LinkRecord projects an authoritative specialized Dependency or generic Link.
// Endpoint identity remains URI-based; optional revision pins are carried
// separately and encoded as structured References in JSON.
type LinkRecord struct {
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	Revision       string          `json:"revision"`
	Version        string          `json:"version"`
	Source         string          `json:"source"`
	Target         string          `json:"target"`
	SourceRevision string          `json:"-"`
	TargetRevision string          `json:"-"`
	Properties     map[string]any  `json:"properties"`
	Metadata       json.RawMessage `json:"metadata"`
	Attribution    Attribution     `json:"attribution"`
}

type DependencyResult struct {
	Link    LinkRecord  `json:"link"`
	Source  IssueRecord `json:"source"`
	Target  IssueRecord `json:"-"`
	Changed bool        `json:"changed"`
}

// DependencyRemoveRequest keeps an explicit graph path authoritative. Native
// ID fallback is supplied only for a bare CLI selector and resolved atomically
// against the live Issue catalog, with collisions refused.
type DependencyRemoveRequest struct {
	SourcePath, TargetPath         string
	SourceNativeID, TargetNativeID string
	Actor                          string
}

// DependencyRemoveResult is the native pair-removal outcome. A missing edge
// returns Removed=false with both checked live Issues and no Link tombstone.
type DependencyRemoveResult struct {
	Removed bool
	Source  IssueRecord
	Target  IssueRecord
	Link    LinkTombstone
}

type IssueMutationResult struct {
	Issue        IssueRecord `json:"issue"`
	Changed      bool        `json:"changed"`
	OpenChildren int         `json:"openChildren,omitempty"`
}

// PreviewOwnedLinkLimit is a disposable descriptor budget, not a production limit.
const PreviewOwnedLinkLimit = 1000

// LinkCreateRequest allocates an independent informational Link. Equal endpoints
// do not deduplicate intent; an explicit allocated Path cannot be reused.
type LinkCreateRequest struct {
	// PublicID is exact public identity; Path remains a private SQL locator.
	PublicID string
	IDRoot   string
	// TypeURL selects an installed informational Type; empty preserves the original Related Type.
	TypeURL                             string
	Path, SourcePath, TargetPath, Actor string
	// Local IDs resolve against the live public catalog in the Link write transaction.
	// Empty IDs keep SourcePath and TargetPath as exact canonical paths.
	SourceLocalID, TargetLocalID string
	SourcePin, TargetPin         EndpointPin
	Properties                   map[string]any
	Metadata                     json.RawMessage
	ExpectedSourceRevision       string
	UnconditionalSource          bool
}

// LinkUpdateRequest changes payload and optional endpoint pins without changing
// Type or endpoint identities. Nil pin intent preserves the stored Reference.
// A guard is mandatory for the Link and, when owned, its source Memory.
type LinkUpdateRequest struct {
	FromRevision           string
	Path, Actor            string
	SourcePin, TargetPin   *EndpointPin
	Properties             map[string]any
	Metadata               publicops.MetadataPatch
	MetadataOnly           bool
	MergeProperties        bool
	ExpectedRevision       string
	Unconditional          bool
	ExpectedSourceRevision string
	UnconditionalSource    bool
}

type LinkMutationResult struct {
	Link           LinkRecord      `json:"link"`
	Source         any             `json:"source"`
	Changed        bool            `json:"changed"`
	ReplacedSource *ReplacedMemory `json:"replacedSource,omitempty"`
}

// LinksRequest selects complete current incident state in one read transaction.
// Empty direction means both. Empty TypeURL admits all installed Link Types.
type LinksRequest struct{ BeadPath, Direction, TypeURL, ConformsTo string }

// LinkPinSelection is an optional CLI endpoint-revision applicability test.
// Revision is an exact retained token; Current requests the live Bead revision.
type LinkPinSelection struct {
	Revision string
	Explicit bool
	Current  bool
}

// LinkCollectionRequest selects current Links without enumerating Beads.
// An empty selector is unconstrained; the preview refuses an incomplete
// collection rather than returning a silently truncated page.
type LinkCollectionRequest struct {
	TypeURL, ConformsTo, SourcePath, TargetPath, IncidentPath string
	// URLs are checked public identities, independent of private path spelling.
	SourceURL, TargetURL, IncidentURL string
	SourcePin, TargetPin, IncidentPin LinkPinSelection
	PairDirection                     string // out, in, or both; empty keeps conjunctive flag filters
	IncidentDirection                 string // in/out/both relative to IncidentPath, independent of source/target filters
	AllRevisions                      bool
}

// PreviewIncidentLinkLimit bounds this disposable non-paginated reader.
const PreviewIncidentLinkLimit = 1000

// ErrAmbiguousLink identifies all candidates without deleting any of them.
type ErrAmbiguousLink struct{ CandidateIDs []string }

func (e *ErrAmbiguousLink) Error() string {
	return "multiple live Links match the endpoint pair; select a canonical Link ID"
}

// LinkDeleteRequest selects an ID or an exact typed endpoint pair, never both.
type LinkDeleteRequest struct {
	Path, SourcePath, TargetPath, TypeURL, Actor string
	SourceLocalID, TargetLocalID                 string
	ExpectedRevision                             string
	Unconditional                                bool
	ExpectedSourceRevision                       string
	UnconditionalSource                          bool
	// DefaultInformationalSource accepts the current source only after the
	// selected Link is classified in the write transaction. It cannot accompany
	// an explicit source guard and never relaxes a blocking Dependency guard.
	DefaultInformationalSource bool
}

// LinkTombstone retains deletion state in this experimental local format.
// It does not define restoration, erasure, or the public History wire contract.
type LinkTombstone struct {
	ID              string      `json:"id"`
	Type            string      `json:"type"`
	Revision        string      `json:"revision"`
	Version         string      `json:"version"`
	State           string      `json:"state"`
	PreviousVersion string      `json:"previousVersion"`
	Attribution     Attribution `json:"attribution"`
}
type LinkDeleteResult struct {
	Link           LinkTombstone   `json:"link"`
	Source         any             `json:"source"`
	Changed        bool            `json:"changed"`
	ReplacedSource *ReplacedMemory `json:"replacedSource,omitempty"`
}
