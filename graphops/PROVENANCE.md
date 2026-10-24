# Graph Preview provenance

The graph domain, storage adapter, CLI, property patches, BDP Read transport,
wire schema and regression fixtures are adapted from contributor work in
[versioned-beads/beads](https://github.com/versioned-beads/beads), under the
Beads MIT license. Original contributors include donnabox and the contributors
credited in those repositories; existing Beads attribution is retained.

- Integration source: [c8817c708006f1eaa9e2bf840723fdd0ef515c25](https://github.com/donnabox/beads/commit/c8817c708006f1eaa9e2bf840723fdd0ef515c25), Graph Preview proposal #81.
- Public attribution/property correction: [76fde9c9cb0865d430bf12d76fc473490f51128a](https://github.com/donnabox/beads/commit/76fde9c9cb0865d430bf12d76fc473490f51128a), proposal #79.
- BDP wire/schema pins and fixture provenance: `internal/httpapi/bdpwire/schema/PROVENANCE` and its generation manifest.

Fork adaptations keep the v1.3.1 native writers, security repairs, continuity
and existing viewer intact. Native version prerequisites are provisioned only
in fresh graph databases; the graph adapter calls the contributor recorder
once after successful native mutation. No ordinary project migration or
upstream integration-branch merge is performed. Revision CLI flags are graph-only
on this fork. Proxy refusals identify missing implementation with #6703.
Metadata file reads are bounded to the graph property input limit. Project `.env` retains the fork's passive-setting allowlist; backend and executable settings remain operator-owned. Graph guidance is independent of ordinary `bd prime` instructions.

The additional canonical-JSON dependency is `github.com/gowebpki/jcs v1.0.1`
(Apache-2.0). Its license is retained at
`internal/storage/issueops/JCS-LICENSE`; THIRD_PARTY_LICENSES credits its authors.
Experimental format limits remain explicit; upstream draft release qualification
is not inherited by this adaptation.

## Preview3 isolated adaptation

The isolated integration branch adapts provider, descriptor/catalog/query and identity work from [donnabox/beads PR291](https://github.com/donnabox/beads/pull/291), production source `0c4dc1f0d68a371e56b3e461b968e828e4ea254c`. Compatible native seams were compared with `79450c2d67ccab326b20b07cc3d66179fa148ec0`; contributor domain/catalog/query regressions remain attributed and retained.

The fork preserves its native writers, qualified engine tuple and continuity adapter. Its schema11/generation13 adds the three frozen schema8 informational Types and immutable native dependency ID/thread facts; it is not the contributor's generation12. Explicit copy conversion archives original bytes and Type declarations, with current boundaries and identity mappings kept separately. Ordinary opens never migrate existing projects. Copy qualification grants neither installation nor project activation.
