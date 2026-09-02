---
name: generate-tests
description: Generate focused, maintainable tests from existing implementation behavior and repository conventions. Use when adding regression tests, covering a feature, or improving meaningful test coverage.
argument-hint: "[file, package, route, or feature]"
disable-model-invocation: true
---

# Generate tests

Generate tests for `$ARGUMENTS` without inventing product behavior.

## Workflow

1. Read the target implementation before proposing or writing tests.
2. Inspect nearby tests, fixtures, helpers, build configuration, and repository guidance.
3. Identify behavior visible at the target boundary, including documented errors and state transitions.
4. Choose the narrowest test layer that proves the behavior:
   - unit tests for isolated rules;
   - integration tests for persistence, transactions, or framework wiring;
   - HTTP tests for request and response contracts;
   - real client tests for protocols such as Git;
   - browser tests for user-visible interactions and navigation.
5. State the intended test files and cases before editing when the target or expected behavior is ambiguous.
6. Write tests in the existing project style and reuse established fixtures.
7. Run focused tests first, then the applicable project gates.
8. Report files changed, commands run, failures, assumptions, and skipped coverage.

## Test quality rules

- Assert externally meaningful behavior rather than reproducing implementation details.
- Include allow and deny cases for authorization, tenant scope, credentials, paths, and state transitions.
- Add a regression test that fails for the reproduced defect before changing production code.
- Use a real database or protocol client when mocks would hide the boundary being tested.
- Keep fixtures deterministic, isolated, and free of credentials or production data.
- Preserve existing tests and meaningful assertions.
- Do not replace precise assertions with broad snapshots, `not null`, or success-only checks.
- Do not weaken validation, permissions, or error handling to make a test pass.

## Scope limits

By default, modify test files only. Do not modify production code, dependencies, CI, configuration, generated artifacts, or documentation unless the user explicitly expands the scope.

Do not commit, push, publish a Plugin version, reset data, access external services, or run destructive commands unless explicitly requested. Never print or persist passwords, tokens, private URLs, or secret environment values.

If existing behavior conflicts with a specification or appears defective, stop after producing the failing test and explain the conflict rather than silently redefining the expected behavior.
