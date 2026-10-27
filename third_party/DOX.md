# Purpose

- Own deliberately retained third-party source dependencies.

# Ownership

- Each child owns its upstream provenance, license, local changes and verification.

# Local Contracts

- Root DOX.md remains binding. Preserve upstream license and attribution.
- Local Go replacements must be mirrored in independent example modules; callers cannot inherit dependency replacements.

# Work Guidance

- Carry only qualified changes and keep upstream source distinguishable from local corrections.

# Verification

- Run each changed module's tests and verify effective module selection in its consumers.

# Child DOX Index

- [mysql/DOX.md](mysql/DOX.md) — Official go-sql-driver/mysql v1.10.0 with bounded greeting parsing.
