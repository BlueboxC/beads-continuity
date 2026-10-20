# DOX.md — Git test environment

## Purpose

Isolate temporary Git fixtures from inherited repository and configuration selectors.

## Ownership

Shared test-startup environment reset; production Git routing remains unchanged.

## Local Contracts

- Call Clear at TestMain entry before fixture setup. Tests may set their own Git environment afterward.
- Remove inherited Git variables and disable system/global configuration until a test explicitly supplies its private configuration.
- Never alter the parent process or an existing repository.

## Work Guidance

Keep this helper independent of storage and production Git packages.

## Verification

Run this package and the internal/git outer-repository preservation regression through scripts/test.sh.

## Child DOX Index
