# Purpose

- Own the retained MySQL network driver used by this Beads build.

# Ownership

- Upstream is go-sql-driver/mysql v1.10.0, commit a065b60ab6d0c8e15468e7709c7f76acf4431647. Preserve LICENSE, AUTHORS and original source headers.
- packets.go carries only the qualified handshake length checks; handshake_p3_test.go carries the regression matrix. Other upstream files remain unchanged.

# Local Contracts

- Root and parent DOX remain binding. Keep module path github.com/go-sql-driver/mysql.
- Missing server-version terminators and incomplete mandatory greeting fields return ErrMalformPkt before slicing. Preserve valid short/full greetings, MariaDB flags and non-NUL plugin names.
- The root go.mod and both independent examples select this source explicitly. Keep Dolt, SQL analyzer, schema and graph formats separate.
- The original dependency is MPL-2.0, as recorded in LICENSE; Beads' own MIT license does not replace it.

# Work Guidance

- Preserve a minimal diff against the pinned official archive. No Beads-side recover around parser panics.

# Verification

- go test -count=1 -skip '^TestConnectorReturnsTimeout$' ./... with MYSQL_TEST_PROT=unix and an absent owned socket; server tests must skip.
- go test -race -count=1 -run '^(TestP3|TestRegression801)' . and `go vet ./...`.
- The omitted upstream timeout test dials an external address and is unsuitable for the offline boundary; preserve its prior sandbox-refusal evidence.

# Child DOX Index
