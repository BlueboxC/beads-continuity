# Selected Memory catalog projection

This pure package projects nine Bead and nine Link Type declarations from
[`bdp@afec911`](https://github.com/gastownhall/bdp/blob/afec9117914bd1c1fe0027c2067e4d89677e40a3/specs/types/memory/memory.md).
The independently fetched source is 106,334 bytes, SHA256
`1f97f8a80f725b2a44a0c715b4ccf09918f02af2abb9885797c3395255dc9e34`.
`SelectedSource` retains that exact publication identity. This successor records
Donna's selection of Evaluation refining Memory, owning Conformance-family
claims, with exact subject revision references. Historical publications and
schema fixtures do not qualify this source or the consuming implementation.

`New` requires explicit application bindings for all eighteen symbols: canonical
Type URI, optional caller-selected exact revision, and schema retrieval base. It
also requires the existing native Issue nominal reference. Construction chooses
no store, authority, installed Type identity, CLI alias, latest revision, or
promotion. It reads no files, network, or storage and compiles no schema.

| Bead Type | Direct parent |
| --- | --- |
| Memory | none within this catalog |
| Evaluation, Constraint | Memory |
| Policy, Decision, Procedure, Requirement, Scenario, Specification | Constraint |

| Link Type | Direct parent |
| --- | --- |
| References | none within this catalog |
| Superseded By, Requires, Subject To, Conformance | References |
| Defines | Requires |
| Addresses, Satisfies, Violates | Conformance |

Conformance is abstract. Its concrete refinements remain concrete; abstractness
is local to a definition and never inherited. Family selectors and ownership
still recognize concrete refinements. The Type-definition Bead remains an
instance of its metatype. Intrinsic metatypes and Refines are not public product
Types in this catalog. No public `bd type` command tree is supplied here.

## Property boundary

Definitions retain local and inherited declarations with their origins. Accessors
copy nested slices and JSON bytes. Shape fragments are value fragments, not
complete effective properties schemas or an extension-field policy. Nonblank,
URI, retained revision and other checks remain explicit `AdditionalChecks` for
the runtime adapter; projection is not operation admission.

Memory title is required and nonblank; body is required Markdown and may be
empty. Link body is optional but nonblank when present. Policy and Decision
permit an authority Reference; Specification permits an open version label.
Conformance declares optional nonblank evidence inherited by its concrete peers.

Evaluation declares required `subjects`: a nonempty array of objects containing
only nonempty string `uri` and `revision` fields. Floating string references and
objects without revisions are refused by this shape. Shape alone cannot prove
URI validity, same-Scope membership, or that a reference binds an actual retained
Bead revision. Those checks require the storage/reference bridge. The full group
is assessed collectively by every owned claim; no claim selects a subset. A
separate group requires a separate Evaluation. No subject is revised merely by
being assessed, and apparent components are not substituted for subject identity.

Constraint applicability remains reserved with no concrete schema fragment.
Its executable representation, authority, inputs and evaluator are deferred.
It is neither an arbitrary executable string nor an automatically supported
predicate. A runtime adapter must state its refusal/preservation behavior rather
than claiming this declaration implements it.

## Endpoint, ownership and graph declarations

References retains four correlated alternatives: Memory to Memory, Memory to
Issue, Issue to Memory, and any Bead to Constraint. Both endpoints must match one
row. Child rules add inherited intersections. Any Bead includes native Types;
it does not include Links or opaque Resources. All selected endpoints are
same-Scope.

Subject To connects any Bead to Constraint. Conformance and each concrete peer
connect Evaluation to Constraint. Memory owns Subject To and Superseded By;
Evaluation adds Conformance; Constraint adds Requires (including Defines).
Every claim is evaluation-owned, including self-authored or unattested claims.
Other participating native Bead Types require Subject To ownership only.
`ExistingIssue` preserves native payload validation and reports the bridge as
not integrated; no permissive Issue schema substitutes for native validation.
Existing Issue/Dependency behavior remains its native owner's responsibility.

Graph sidecars retain identity-self refusal and same-Scope restrictions from
References. Superseded By adds source-family maximum one, identity acyclicity,
and floating endpoints. Defines adds target-family maximum one and acyclicity
within that family. Other families do not gain those narrower bounds. Maxima
count live Links by endpoint identity, not historical copies or selected pins.
No graph evaluator is implemented by this projection.

Subject To is nontransitive. Obligations compose through the target's Requires
and Defines closure. Satisfies claims may compose through compatible Evaluation
subject bindings, using singleton intermediate Constraints. This is not native
stored-Link transitivity, and collective intermediate groups do not distribute
claims to individual members. Original evaluations, complete groups, claims and
evidence remain visible; no inferred Links are stored or evidence verified.
Closure does not imply satisfaction and claim counts are not verdicts.

## Validation and integration

Tests cover inventory, inheritance, exact subject value-shape declarations,
local abstractness, native validation boundaries, collective claim meanings,
correlated endpoint matching, ownership and copy isolation. Matcher fixtures
use restrictive synthetic schemas; they do not validate native Issue payloads
or a complete installed Memory schema.

Janet owns formatting, generation, Go/Bazel/build/backend/test execution. No
checks were run by this author for this successor diff. Focused target:
`//internal/memorycatalog:memorycatalog_test`. Older source-bound results are
historical and do not qualify this candidate. Runtime installation, CRUD guards,
retention, retyping and CLI integration belong to the consuming adapter and
storage batches. No main/default pin, release activation or promotion follows
from these declarations.
