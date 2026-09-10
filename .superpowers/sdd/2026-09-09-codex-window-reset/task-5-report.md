# Task 5 implementation report

## Scope

Implemented only the strict Codex probe protocol requested by `task-5-brief.md`.

Changed files:

- `internal/probe/sse.go`
- `internal/probe/client.go`
- `internal/probe/sse_test.go`
- `internal/probe/client_test.go`
- `NOTICE`

The required caller-selected `model` is included in every request body, despite
being absent from the illustrative request template.

## Requirement evidence

- `ParseCompleted` uses `bufio.Scanner` with a 4 MiB maximum token and returns a
  sanitized `response_error` for scanner overflow, malformed JSON, missing
  completion, terminal failure/incomplete/error events, and missing text.
- SSE parsing ignores non-data lines, empty data, and `[DONE]`; it also accepts a
  single completed JSON response.
- Text extraction precedence is terminal completed response, output-text done,
  completed output item/content part, then accumulated deltas.
- `Client.Execute` uses `host.API` and `accounts.AuthMaterial`, posts to the
  specified Responses endpoint, sends the selected model and required request
  options, and trims the final text before requiring exact `OK`.
- Required authentication, account, content, accept, originator, and
  Linux/architecture-aware user-agent headers are sent only through the host
  request. They are not logged or returned in results/errors.
- HTTP, timeout, network, malformed-response, and unexpected-output outcomes
  map to the requested `domain.RequestOutcome` values. Retry eligibility is
  limited to network failures, timeouts, HTTP 429, and HTTP 5xx.
- `NOTICE` contains the exact codex-health-monitor attribution required by the
  brief.

## TDD evidence

Initial parser/client tests were written before production implementation.

Red run:

```text
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24 go test ./internal/probe -run TestParseCompleted -v
```

Result: expected compile failure because `ParseCompleted`, `New`, and the
endpoint were not yet implemented.

An additional red cycle added transport edge cases for wrapped timeout errors
and caller cancellation. Before the mapping change, those cases failed with
`network_error`/retryable output instead of `timeout`/non-retryable output.

## Verification evidence

All commands ran with the official `golang:1.24` Docker image unless noted.

```text
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24 go test ./internal/probe -v
PASS

docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24 go test ./...
PASS: accounts, domain, host, probe, schedule, simulate, store

docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24 go vet ./...
PASS

docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24 go test -race ./internal/probe
PASS

git diff --check
PASS
```

The focused suite covers all accepted/rejected terminal shapes, completed
items/content parts, raw completed JSON, oversized events, request-body fields
including `model`, required headers, all requested HTTP outcomes, timeout and
network errors, malformed terminal JSON, `NOT OK`, credential failure, and
retry eligibility.

## Self-review and concerns

- No prior files or commits were reverted; the worktree contained no unrelated
  changes before implementation.
- No live Codex request was made. Upstream behavior is covered through the
  injected host fake and parser fixtures; deployment still depends on the host
  adapter providing the documented HTTP and credential capabilities.

Commit message: `feat: add strict Codex probe protocol`.

The final commit SHA is reported in the handoff; it is intentionally not
embedded in this committed report because changing a commit to record its own
hash necessarily changes that hash.

## Fix round 1

Fixed the Important finding in `internal/probe/sse.go`:

- `outputText` now recognizes text only from an object whose `type` is
  exactly `output_text` and whose `text` is a non-null JSON string. Arbitrary
  `output_text` fields are no longer accepted, and an invalid typed object
  cannot fall through to nested extraction.
- `rawStringOK` now decodes through `*string`, so JSON `null` is rejected
  instead of being treated as a valid empty string.

Added focused regressions to `internal/probe/sse_test.go`:

- `TestParseCompleted/arbitrary_output_text_field_is_rejected` rejects a
  terminal response containing `response.output_text` without a valid
  output-text object.
- `TestParseCompleted/null_output_text_is_rejected` rejects a valid-shaped
  output-text part whose `text` is JSON `null`.

The focused red run was:

```text
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24 go test ./internal/probe -run TestParseCompleted -v
```

Result before the production fix: FAIL, with
`TestParseCompleted/arbitrary_output_text_field_is_rejected` returning
`"OK", <nil>` and `TestParseCompleted/null_output_text_is_rejected`
returning `"", <nil>`.

Post-fix verification:

```text
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24 go test ./internal/probe -run TestParseCompleted -v
```

Result: PASS; all `TestParseCompleted` cases, including both regressions, and
`TestParseCompletedRejectsOversizedEvent` passed.

```text
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24 go test ./internal/probe -v
```

Result: PASS; all probe client/parser tests and `ExampleParseCompleted`
passed.

```text
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24 go test ./...
```

Result: PASS; all packages (`accounts`, `domain`, `host`, `probe`, `schedule`,
`simulate`, and `store`) passed.

```text
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24 gofmt -d internal/probe/sse.go internal/probe/sse_test.go
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24 go vet ./...
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24 go test -race ./internal/probe
git diff --check
```

Results: `gofmt -d` produced no diff, `go vet ./...` passed, the race-enabled
probe suite passed, and `git diff --check` passed.
