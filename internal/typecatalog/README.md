# Fixed selected Type foundation

This package is an unused foundation for the selected Preview3 Type lane. The
application supplies a finite catalog; there is no public Type installation,
CRUD, promotion, discovery or bootstrap API. Synthetic tests deliberately do
not select the pending Memory editorial catalog.

`New` resolves a single selected definition per Type identity, checks references,
category agreement and acyclic direct conformance, and computes a deduplicated
transitive closure. Exact references must match the selected revision; an
identity-only reference matches this selection without fetching anything.
`Definition` and `Public` return independent copies. Public names cannot be the
four intrinsic metatypes. CLI canonical/relative/alias admission, including the
Memory Decision versus Issue decision collision, is a separate integration gate.

`ValidateProperties` checks the complete prospective properties object against
each inherited local schema independently. No property conversion, deletion,
defaults or affiliation inference occurs. Draft 2020-12 is the default schema
dialect. The caller supplies the explicit schema retrieval URI and retained
resources; missing resources fail without file or network loading. Application
retrieval keys (`SchemaBase` and `SchemaResources` keys) in the exact
`http://json-schema.org/` or `https://json-schema.org/` namespace are refused:
the pinned compiler reserves those prefixes and skips schema-shape validation
there. Standard `$schema` dialect declarations remain supported; supplied
retrieval identities are never rewritten. Formats in
the explicit supported set are asserted; unknown/unsupported formats are refused,
including idn-email/idn-hostname rather than silently accepting them. Dialect
checks visit schema positions and embedded resource roots without interpreting
literal data as schemas. Content annotations retain standard behavior. Final selected
schemas, retained-closure admission details and the unpublished BDP profile must
be reconciled at the exact editorial pin before activation.

`ValidateEndpoints` checks the union of correlated alternatives in each local
definition, intersected with all parent definitions. Endpoints must match both
columns of the same row. Caller-supplied evidence describes the actual selected
endpoint state; validation does not establish existence, visibility, retention,
endpoint pin requirements or authorization. `Owns` unions inherited ownership
declarations and matches specialized Link Types once, including through diamonds.
It is a predicate, not the retained owned-group projection or a revision writer.

This first infrastructure slice is deliberately incomplete as operation admission.
Participation bounds, family maxima, uniqueness, acyclicity/self-identity rules,
pin rules and product-specific constraints still need selected definitions and
their explicit enforcement mapping. None is waived. Storage, retained grouping,
property conversion, invalid-Link/retype guards, workspace compatibility and
CLI adapters remain separately gated. Historical readers must retain original
Type interpretation, rather than revalidate against a newer current catalog.
The old `graphops.TypeDescriptor` wire shape/fingerprint remains unchanged; its
bounded adapter must follow the selected representation and cannot flatten
correlated pairs or convert unbounded ownership to invented legacy maxima.

The earlier isolated [PR115 foundation](https://github.com/donnabox/beads/pull/115)
and its tests/review evidence remain intact. Its older fixed seven-Bead/six-Link
catalog and handwritten property predicates are not promoted to today's catalog.
This slice retains its complete-payload, caller-evidence and same-row endpoint
validation boundaries while adding an explicit selected definition graph. It
does not replace or close that PR.

## Execution ownership

Janet owns all Go/Bazel/build/test/backend execution. Janet ran the focused native Go checks: 23 Type-catalog cases (including subtests)
and 53 existing graphops tests passed with no failures or skips. This is inner-loop
evidence, not a Bazel or product-activation qualification. Intended focused targets are
`//internal/typecatalog:typecatalog_test` and the existing
`//graphops:graphops_test`, followed by Janet's selected checks. The new package
uses the already pinned `github.com/santhosh-tekuri/jsonschema/v6 v6.0.2` from
`go.mod`; its direct Bazel repository mapping and direct Go dependency marker were added
without changing the pinned version. Go formatting and generated source filegroups
are synchronized. Bazel is absent on the current host, so Gazelle, module tidy,
Bazel tests and their lint/guard gates remain unverified. Final selected catalog,
descriptor adapter and storage/CLI activation remain separate work.
