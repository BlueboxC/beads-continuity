# Type-family graph constraints

This internal validator checks declaration-driven graph restrictions against a
complete prospective population of live Links. It uses the selected catalog's
nominal refinement closure, with no Memory-specific names or fixed inventory.
It checks identity-self refusal, same-Scope and floating-endpoint requirements,
family source/target maxima, ordered-pair uniqueness and directed acyclicity.
False or absent restrictions add no constraint; ancestor declarations compose.

Identity is the canonical Scope and local path. Endpoint revision selectors
cannot evade self checks, family counts or cycles. Distinct parallel Links count
separately and remain legal unless a declaration forbids them. A family reached
through multiple parent paths still counts each Link once. Cycle checking is
iterative and tolerates parallel edges in an acyclic graph.

The storage adapter must establish a complete live population inside the write
transaction, overlay all proposed edits/deletions/retypes and validate the final
state before publication. A partial collection query cannot prove a maximum or
absence of a cycle. The caller also establishes endpoint existence, visibility,
Type/category, retention and authority. This package supplies no query, solver,
storage write, migration, repair, installation or protocol API. Payload and
correlated endpoint eligibility remain the catalog validator's responsibility.

The new package is not yet wired into storage. Tests are authored but have not
been executed; Janet owns formatting, generated build metadata and qualification.
The intended focused target is `//internal/typeconstraints:typeconstraints_test`.
