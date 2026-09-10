# Codex Window Reset Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a restart-safe CLIProxyAPI plugin that schedules bounded Codex preheat requests, supports manual health probes and auditable single-account quota resets, and exposes a responsive management panel.

**Architecture:** A small C-shared entry point injects CLIProxyAPI host callbacks into `internal/app.Runtime`, which alone owns mutable state and goroutines. Pure domain, calendar, simulation, probe parsing, and quota parsing modules sit behind typed interfaces; atomic JSON repositories persist configuration/runtime/history/audit while usage snapshots remain in memory. The management layer exposes fixed JSON routes and an exact allowlist of embedded ES-module resources.

**Tech Stack:** Go 1.24 standard library, Linux CGO `c-shared`, native HTML/CSS/JavaScript ES modules, Node built-in test runner, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-09-09-codex-window-reset-design.md`

## Global Constraints

- Go version is exactly 1.24; production Go code may import only the standard library and the CLIProxyAPI host ABI.
- Release targets are Linux `amd64` and Linux `arm64`, built with CGO `-buildmode=c-shared`.
- The plugin starts with scheduling disabled, no Scheduled Accounts, and no default preheat lead/span.
- Automatic actions may issue only Preheat Requests; Quota Reset is always manual and targets exactly one account.
- Health Probe and Preheat Request use the same real Codex request and disclose that they may consume ordinary quota or begin a Short Window.
- Quota Reset uses both account-level single-flight and a durable caller-provided idempotency key.
- CLIProxyAPI owns management authentication; neither backend nor browser stores or returns management keys or Codex access tokens.
- Quota is fetched only on manual refresh, immediately before and after a probe, and immediately after a reset; there is no polling.
- Usage Snapshots are in memory, become stale after five minutes, and never authorize `sufficient_window` when stale.
- A known Guardrail Hold survives snapshot staleness and restarts until a successful refresh proves all Long Windows above the floor.
- Normal history retains 100 records; Reset Audit retains 365 days and requires `X-Confirmation: DELETE AUDIT` for deletion.
- Browser assets are native embedded files registered one path at a time; no wildcard resource route or frontend runtime dependency.
- Preserve MIT attribution for protocol/release code substantially derived from `tapaixx/codex-health-monitor`.
- Every error response uses the stable envelope and includes a correlation ID; logs never contain credentials, keys, raw auth JSON, or upstream response bodies.

## Locked File Map and Interfaces

Create these focused files; do not collapse them into a monolith:

```text
go.mod, version.go, assets.go, main.go, package.json
internal/domain/{errors,config,account,clock,quota,operation,reset,simulation}.go
internal/store/{atomic_json,config,history,runtime_state,reset_audit}.go
internal/host/{types,client}.go
internal/accounts/service.go
internal/schedule/{local_time,planner,scheduler}.go
internal/simulate/service.go
internal/probe/{client,sse}.go
internal/quota/{parse,service}.go
internal/app/{interfaces,runtime,preheat,manual,reset,status}.go
internal/management/{types,router,assets}.go
web/panel.html, web/styles.css
web/modules/{api,state,accounts,schedule,simulator,history,main}.js
web/tests/{api,state,markup}.test.mjs
.github/workflows/{ci,release}.yml
scripts/{package-release,package_release_test}.sh
README.md, LICENSE, NOTICE, registry.json, docs/verification.md
```

The following signatures are contracts across tasks:

```go
// internal/host/types.go
type API interface {
    ListAuthFiles(context.Context) ([]AuthFile, error)
    GetAuth(context.Context, string) (json.RawMessage, error)
    HTTPDo(context.Context, HTTPRequest) (HTTPResponse, error)
    Log(context.Context, string, string, map[string]any)
}

// internal/store repositories, declared in internal/app/interfaces.go
type ConfigRepository interface { Load() (domain.Config, error); Save(domain.Config) error }
type HistoryRepository interface { Load() ([]domain.OperationRecord, error); Append(domain.OperationRecord) error; Clear() error }
type RuntimeStateRepository interface {
    Load() (domain.RuntimeState, error)
    Save(domain.RuntimeState) error
    Update(func(*domain.RuntimeState) error) error
}
type ResetAuditRepository interface {
    Load() ([]domain.ResetAudit, error)
    FindByKey(string) (domain.ResetAudit, bool, error)
    AppendPending(domain.ResetAudit) error
    Replace(domain.ResetAudit) error
    DeleteBefore(time.Time) error
    RecoverPending(time.Time) error
    Clear() error
}

// services, declared in internal/app/interfaces.go
type ProbeExecutor interface { Execute(context.Context, accounts.Account, string, time.Duration) domain.ProbeResult }
type QuotaService interface {
    Refresh(context.Context, accounts.Account) (domain.UsageSnapshot, error)
    Get(string, time.Time) (domain.SnapshotView, bool)
    Reset(context.Context, accounts.Account, string) (domain.ResetHTTPResult, error)
}
type AccountService interface { List(context.Context) ([]accounts.Account, error); Find(context.Context, string) (accounts.Account, error) }
type Planner interface { PlanDay(domain.Config, time.Time) ([]domain.PlannedOccurrence, error) }
```

```go
// internal/domain/clock.go
type Timer interface { Stop() bool }
type Clock interface { Now() time.Time; AfterFunc(time.Duration, func()) Timer }
```

All time values crossing persistence or JSON boundaries are UTC RFC3339; local wall-clock strings remain strict `HH:mm`.

---

### Task 1: Domain Model and Configuration Validation

**Files:**
- Create: `go.mod`
- Create: `version.go`
- Create: `internal/domain/errors.go`
- Create: `internal/domain/config.go`
- Create: `internal/domain/account.go`
- Create: `internal/domain/clock.go`
- Create: `internal/domain/quota.go`
- Create: `internal/domain/operation.go`
- Create: `internal/domain/reset.go`
- Create: `internal/domain/simulation.go`
- Test: `internal/domain/config_test.go`
- Test: `internal/domain/outcomes_test.go`

**Interfaces:**
- Consumes: only Go standard-library types.
- Produces: `domain.DefaultConfig() Config`, `domain.ValidateConfig(Config, ValidationMode, map[string]struct{}) error`, `domain.Error`, all JSON-facing enums and records used by every later task.

- [ ] **Step 1: Initialize the module and write failing default/validation tests**

```go
// go.mod
module github.com/tapaixx/codex-window-reset

go 1.24
```

```go
// internal/domain/config_test.go
func TestDefaultConfigStartsInert(t *testing.T) {
    got := DefaultConfig()
    if got.Enabled || len(got.ScheduledAccountKeys) != 0 || got.PreheatLeadMinutes != nil || got.PreheatSpanMinutes != nil {
        t.Fatalf("unsafe defaults: %#v", got)
    }
    if got.Timezone != "Asia/Shanghai" || got.ProbeModel != "gpt-5.6-luna" || got.ProbeTimeoutSeconds != 30 {
        t.Fatalf("unexpected defaults: %#v", got)
    }
}

func TestValidateConfigRequiresActivationFields(t *testing.T) {
    cfg := DefaultConfig()
    cfg.Enabled = true
    err := ValidateConfig(cfg, ValidatePersisted, map[string]struct{}{})
    assertCode(t, err, CodeConfigInvalid)
}

func TestValidateConfigRejectsCrossMidnightAndUnknownAccount(t *testing.T) {
    lead, span := 120, 60
    cfg := DefaultConfig()
    cfg.Enabled = true
    cfg.WorkPeriods = []LocalPeriod{{Start: "23:00", End: "01:00"}}
    cfg.PreheatLeadMinutes, cfg.PreheatSpanMinutes = &lead, &span
    cfg.ScheduledAccountKeys = []string{"missing"}
    assertCode(t, ValidateConfig(cfg, ValidatePersisted, map[string]struct{}{"known": {}}), CodeConfigInvalid)
}
```

- [ ] **Step 2: Run the domain tests and verify the package is missing**

Run: `go test ./internal/domain -run 'Test(DefaultConfig|ValidateConfig)' -v`

Expected: FAIL because `DefaultConfig`, `Config`, and validation symbols do not exist.

- [ ] **Step 3: Define exact config, errors, and validation**

```go
// internal/domain/errors.go
type ErrorCode string

const (
    CodeConfigInvalid ErrorCode = "config_invalid"
    CodeRevisionConflict ErrorCode = "revision_conflict"
    CodeRunInProgress ErrorCode = "run_in_progress"
    CodeAccountBusy ErrorCode = "account_busy"
    CodeAccountDisabled ErrorCode = "account_disabled"
    CodeAccountUnavailable ErrorCode = "account_unavailable"
    CodeGuardrailHold ErrorCode = "guardrail_hold"
    CodeQuotaRefreshFailed ErrorCode = "quota_refresh_failed"
    CodeProbeFailed ErrorCode = "probe_failed"
    CodeWindowUnverified ErrorCode = "window_unverified"
    CodeIdempotencyConflict ErrorCode = "idempotency_conflict"
    CodeResetOutcomeUnknown ErrorCode = "reset_outcome_unknown"
    CodeStoreCorrupt ErrorCode = "store_corrupt"
)

type Error struct {
    Code ErrorCode
    Message string
    Retryable bool
    HTTPStatus int
    CorrelationID string
}
func (e *Error) Error() string { return e.Message }
```

```go
// internal/domain/config.go
type Config struct {
    SchemaVersion int `json:"schema_version"`
    Revision int64 `json:"revision"`
    Enabled bool `json:"enabled"`
    Timezone string `json:"timezone"`
    Weekdays []int `json:"weekdays"`
    WorkPeriods []LocalPeriod `json:"work_periods"`
    PreheatLeadMinutes *int `json:"preheat_lead_minutes"`
    PreheatSpanMinutes *int `json:"preheat_span_minutes"`
    ProductivityMinutes int `json:"productivity_minutes"`
    RemainingQuotaFloorPercent int `json:"remaining_quota_floor_percent"`
    RemainingWindowFloorMinutes int `json:"remaining_window_floor_minutes"`
    LongWindowFloorPercent int `json:"long_window_floor_percent"`
    BlackoutPeriods []LocalPeriod `json:"blackout_periods"`
    ProbeModel string `json:"probe_model"`
    ProbeTimeoutSeconds int `json:"probe_timeout_seconds"`
    ScheduledAccountKeys []string `json:"scheduled_account_keys"`
}
type LocalPeriod struct { Start string `json:"start"`; End string `json:"end"` }
type ValidationMode uint8
const ( ValidatePersisted ValidationMode = iota; ValidateSimulation )

func DefaultConfig() Config {
    return Config{SchemaVersion: 1, Revision: 1, Timezone: "Asia/Shanghai",
        Weekdays: []int{1, 2, 3, 4, 5},
        WorkPeriods: []LocalPeriod{{Start: "09:00", End: "12:00"}, {Start: "13:30", End: "19:00"}},
        ProductivityMinutes: 60, RemainingQuotaFloorPercent: 20,
        RemainingWindowFloorMinutes: 60, LongWindowFloorPercent: 10,
        ProbeModel: "gpt-5.6-luna", ProbeTimeoutSeconds: 30,
        ScheduledAccountKeys: []string{}, BlackoutPeriods: []LocalPeriod{}}
}
```

Implement `ValidateConfig` with `time.LoadLocation`, exact `time.Parse("15:04", value)` round-trip checking, unique weekdays/account keys, sorted non-overlapping periods, all numeric ranges from the spec, same-local-date preheat derivation, and activation checks. In `ValidateSimulation`, permit `Enabled=false` but validate every other field identically.

- [ ] **Step 4: Define closed outcome and persistence types, then test enum JSON stability**

```go
type RequestOutcome string
const (
    RequestSucceeded RequestOutcome = "succeeded"
    RequestUnauthorized RequestOutcome = "unauthorized"
    RequestForbidden RequestOutcome = "forbidden"
    RequestPaymentRequired RequestOutcome = "payment_required"
    RequestRateLimited RequestOutcome = "rate_limited"
    RequestUpstreamError RequestOutcome = "upstream_error"
    RequestNetworkError RequestOutcome = "network_error"
    RequestTimeout RequestOutcome = "timeout"
    RequestResponseError RequestOutcome = "response_error"
    RequestUnexpectedOutput RequestOutcome = "unexpected_output"
    RequestCredentialError RequestOutcome = "credential_error"
    RequestDisabled RequestOutcome = "disabled"
)
type WindowOutcome string
const (
    WindowVerifiedStarted WindowOutcome = "verified_started"
    WindowAlreadyActive WindowOutcome = "already_active"
    WindowUnchanged WindowOutcome = "unchanged"
    WindowUnverified WindowOutcome = "unverified"
    WindowNotObserved WindowOutcome = "not_observed"
)

type UsageWindow struct {
    DurationMinutes int `json:"duration_minutes"`
    RemainingPercent int `json:"remaining_percent"`
    ResetAt time.Time `json:"reset_at,omitempty"`
    Short bool `json:"short"`
}
type ResetCredit struct { ID string `json:"id,omitempty"`; ExpiresAt time.Time `json:"expires_at,omitempty"` }
type UsageSnapshot struct {
    AccountKey string `json:"account_key"`
    CapturedAt time.Time `json:"captured_at"`
    Windows []UsageWindow `json:"windows"`
    ResetCredits []ResetCredit `json:"reset_credits,omitempty"`
    ResetApplicableCount *int `json:"reset_applicable_count,omitempty"`
    ResetInfoComplete bool `json:"reset_info_complete"`
}
type SnapshotView struct {
    Snapshot UsageSnapshot `json:"snapshot"`
    Stale bool `json:"stale"`
    LastAttemptAt time.Time `json:"last_attempt_at"`
    RefreshErrorCode ErrorCode `json:"refresh_error_code,omitempty"`
}
type GuardrailHold struct {
    AccountKey string `json:"account_key"`
    EstablishedAt time.Time `json:"established_at"`
    FloorPercent int `json:"floor_percent"`
}
type QuotaDecision string
const (
    DecisionProceed QuotaDecision = "proceed"
    DecisionSufficientWindow QuotaDecision = "sufficient_window"
    DecisionGuardrailHold QuotaDecision = "guardrail_hold"
    DecisionUnknownFailOpen QuotaDecision = "quota_unknown_fail_open"
)

type ProbeResult struct {
    Outcome RequestOutcome `json:"request_outcome"`
    HTTPStatus int `json:"http_status,omitempty"`
    LatencyMS int64 `json:"latency_ms"`
    ErrorCode ErrorCode `json:"error_code,omitempty"`
    RetryEligible bool `json:"retry_eligible"`
}
type OperationTrigger string
const (
    TriggerHealthProbe OperationTrigger = "health_probe"
    TriggerPreheat OperationTrigger = "preheat"
    TriggerCompensation OperationTrigger = "compensation"
)
type OperationRecord struct {
    ID string `json:"id"`
    CorrelationID string `json:"correlation_id"`
    Trigger OperationTrigger `json:"trigger"`
    OccurrenceID string `json:"occurrence_id,omitempty"`
    AccountKey string `json:"account_key"`
    MaskedIdentity string `json:"masked_identity"`
    StartedAt time.Time `json:"started_at"`
    FinishedAt time.Time `json:"finished_at"`
    RequestOutcome RequestOutcome `json:"request_outcome"`
    WindowOutcome WindowOutcome `json:"window_outcome"`
    Decision QuotaDecision `json:"decision,omitempty"`
    HTTPStatus int `json:"http_status,omitempty"`
    LatencyMS int64 `json:"latency_ms"`
    ErrorCode ErrorCode `json:"error_code,omitempty"`
}

type OccurrenceStatus string
const (
    OccurrencePlanned OccurrenceStatus = "planned"
    OccurrenceRunning OccurrenceStatus = "running"
    OccurrenceSucceeded OccurrenceStatus = "succeeded"
    OccurrenceFailed OccurrenceStatus = "failed"
    OccurrenceSkipped OccurrenceStatus = "skipped"
    OccurrenceMissed OccurrenceStatus = "missed"
)
type PlannedOccurrence struct {
    ID string `json:"id"`
    AccountKey string `json:"account_key"`
    LocalDate string `json:"local_date"`
    PeriodIndex int `json:"period_index"`
    WindowStart time.Time `json:"window_start"`
    WindowEnd time.Time `json:"window_end"`
    PlannedAt time.Time `json:"planned_at"`
    Missed bool `json:"missed"`
    MissedReason string `json:"missed_reason,omitempty"`
}
type OccurrenceState struct {
    PlannedOccurrence
    Status OccurrenceStatus `json:"status"`
    CompensationDueAt time.Time `json:"compensation_due_at,omitempty"`
    CompensationAttempted bool `json:"compensation_attempted"`
}
type RuntimeState struct {
    SchemaVersion int `json:"schema_version"`
    Occurrences map[string]OccurrenceState `json:"occurrences"`
    NextRuns map[string]time.Time `json:"next_runs"`
    GuardrailHolds map[string]GuardrailHold `json:"guardrail_holds"`
}

type ResetOutcome string
const (
    ResetPending ResetOutcome = "pending"
    ResetSucceeded ResetOutcome = "succeeded"
    ResetFailed ResetOutcome = "failed"
    ResetUnknown ResetOutcome = "unknown"
)
type ResetAudit struct {
    IdempotencyKey string `json:"idempotency_key"`
    RequestedAt time.Time `json:"requested_at"`
    FinishedAt time.Time `json:"finished_at,omitempty"`
    AccountKey string `json:"account_key"`
    MaskedIdentity string `json:"masked_identity"`
    PriorApplicableCredits *int `json:"prior_applicable_credits,omitempty"`
    Outcome ResetOutcome `json:"outcome"`
    HTTPCategory string `json:"http_category,omitempty"`
    CorrelationID string `json:"correlation_id"`
}
type ResetHTTPResult struct { StatusCode int; Category string }

type TimelineSegment struct {
    Kind string `json:"kind"`
    Start time.Time `json:"start"`
    End time.Time `json:"end"`
    AccountKey string `json:"account_key,omitempty"`
}
type StrategyMetrics struct {
    AvailableCoverageMinutes int `json:"available_coverage_minutes"`
    IdleWindowMinutes int `json:"idle_window_minutes"`
}
type SimulationResult struct {
    WorkMinutes int `json:"work_minutes"`
    Baseline StrategyMetrics `json:"baseline"`
    Scheduled StrategyMetrics `json:"scheduled"`
    NetGainMinutes int `json:"net_gain_minutes"`
    PreheatWindows []PlannedOccurrence `json:"preheat_windows"`
    TimelineSegments []TimelineSegment `json:"timeline_segments"`
    Assumptions map[string]int `json:"assumptions"`
}
type StatusView struct {
    Enabled bool `json:"enabled"`
    StoreErrorCode ErrorCode `json:"store_error_code,omitempty"`
    NextRuns map[string]time.Time `json:"next_runs"`
    RunID string `json:"run_id,omitempty"`
    RunTotal int `json:"run_total"`
    RunCompleted int `json:"run_completed"`
}
```

Add a table test that marshals every enum and compares its literal value. Add JSON tests proving identity projections expose only `account_key`, `masked_identity`, and `plan_label`, and that `ResetHTTPResult` has no JSON export path.

- [ ] **Step 5: Run all domain tests**

Run: `go test ./internal/domain -v`

Expected: PASS, including invalid timezone, duplicate weekdays/accounts, malformed clock, overlapping periods, out-of-range thresholds, preheat crossing midnight, and disabled simulation cases.

- [ ] **Step 6: Commit the domain foundation**

```bash
git add go.mod version.go internal/domain
git commit -m "feat: define window reset domain model"
```

### Task 2: Atomic Versioned Persistence and Retention Boundaries

**Files:**
- Create: `internal/store/atomic_json.go`
- Create: `internal/store/config.go`
- Create: `internal/store/history.go`
- Create: `internal/store/runtime_state.go`
- Create: `internal/store/reset_audit.go`
- Test: `internal/store/atomic_json_test.go`
- Test: `internal/store/repositories_test.go`

**Interfaces:**
- Consumes: `domain.Config`, `domain.OperationRecord`, `domain.RuntimeState`, `domain.ResetAudit`.
- Produces: `store.NewConfigRepository(string) *store.ConfigRepository`, `store.NewHistoryRepository(string, int) *store.HistoryRepository`, `store.NewRuntimeStateRepository(string) *store.RuntimeStateRepository`, `store.NewResetAuditRepository(string, time.Duration, domain.Clock) *store.ResetAuditRepository`, plus the repository methods listed above.

- [ ] **Step 1: Write failing atomicity, corruption, and deletion-boundary tests**

```go
func TestConfigCorruptionIsReportedNotReplaced(t *testing.T) {
    dir := t.TempDir()
    os.WriteFile(filepath.Join(dir, "config.json"), []byte("{"), 0o600)
    repo := NewConfigRepository(dir)
    _, err := repo.Load()
    var corrupt *domain.Error
    if !errors.As(err, &corrupt) || corrupt.Code != domain.CodeStoreCorrupt { t.Fatalf("got %v", err) }
    got, _ := os.ReadFile(filepath.Join(dir, "config.json"))
    if string(got) != "{" { t.Fatalf("corrupt source was overwritten: %q", got) }
}

func TestOrdinaryClearCannotDeleteAuditOrRuntimeState(t *testing.T) {
    dir := t.TempDir()
    history := NewHistoryRepository(dir, 100)
    states := NewRuntimeStateRepository(dir)
    audit := NewResetAuditRepository(dir, 365*24*time.Hour, fixedClock("2026-09-09T12:00:00Z"))
    if err := history.Append(domain.OperationRecord{ID:"op-1"}); err != nil { t.Fatal(err) }
    state := domain.RuntimeState{SchemaVersion:1, Occurrences:map[string]domain.OccurrenceState{
        "2026-09-09/p0/acct-a": {PlannedOccurrence:domain.PlannedOccurrence{ID:"2026-09-09/p0/acct-a"}, Status:domain.OccurrencePlanned},
    }}
    if err := states.Save(state); err != nil { t.Fatal(err) }
    record := domain.ResetAudit{IdempotencyKey:"11111111-1111-4111-8111-111111111111", AccountKey:"acct-a", Outcome:domain.ResetPending}
    if err := audit.AppendPending(record); err != nil { t.Fatal(err) }
    if err := history.Clear(); err != nil { t.Fatal(err) }
    gotHistory, _ := history.Load()
    gotState, _ := states.Load()
    gotAudit, _ := audit.Load()
    if len(gotHistory) != 0 || len(gotState.Occurrences) != 1 || len(gotAudit) != 1 { t.Fatalf("history=%#v state=%#v audit=%#v", gotHistory, gotState, gotAudit) }
}
```

- [ ] **Step 2: Run persistence tests and verify they fail**

Run: `go test ./internal/store -v`

Expected: FAIL because repositories do not exist.

- [ ] **Step 3: Implement the same-directory fsync/rename writer**

```go
func writeAtomic(path string, value any) error {
    data, err := json.MarshalIndent(value, "", "  ")
    if err != nil { return err }
    data = append(data, '\n')
    if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { return err }
    f, err := os.CreateTemp(filepath.Dir(path), ".window-reset-*.tmp")
    if err != nil { return err }
    name := f.Name()
    defer os.Remove(name)
    if err = f.Chmod(0o600); err == nil { _, err = f.Write(data) }
    if err == nil { err = f.Sync() }
    closeErr := f.Close()
    if err == nil { err = closeErr }
    if err != nil { return err }
    if err = os.Rename(name, path); err != nil { return err }
    dir, err := os.Open(filepath.Dir(path))
    if err != nil { return err }
    defer dir.Close()
    if err = dir.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) { return err }
    return nil
}
```

Use an injected rename hook in tests to prove a failed rename leaves the original document intact and removes the temporary file.

- [ ] **Step 4: Implement repositories with schema checks and locked read-modify-write**

Each repository owns a `sync.Mutex`. `ConfigRepository.Load` creates and saves `domain.DefaultConfig()` only when `config.json` is absent; malformed JSON or an unsupported schema returns `store_corrupt`. `Save` writes a complete replacement document. History append keeps the newest 100 records. `RuntimeStateRepository.Update` performs locked load-mutate-atomic-save so quota holds and scheduler occurrences cannot overwrite one another. Reset Audit removes records older than 365 days; `RecoverPending(now)` converts every persisted `pending` entry to `unknown`, sets `FinishedAt=now`, and saves before Runtime accepts reset traffic.

```go
func (r *resetAuditRepository) AppendPending(a domain.ResetAudit) error {
    r.mu.Lock(); defer r.mu.Unlock()
    items, err := r.loadLocked()
    if err != nil && !errors.Is(err, fs.ErrNotExist) { return err }
    for _, item := range items {
        if item.IdempotencyKey == a.IdempotencyKey { return domain.NewError(domain.CodeIdempotencyConflict, 409, false, "idempotency key already exists") }
    }
    items = append(items, a)
    return writeAtomic(r.path, auditDocument{SchemaVersion: 1, Records: items})
}
```

- [ ] **Step 5: Add retention and crash-ambiguity assertions**

Use a fixed clock `2026-09-09T12:00:00Z`; prove a record at `2025-09-09T11:59:59Z` is pruned, one at the exact 365-day cutoff remains, and calling `RecoverPending` on a freshly reconstructed repository converts a persisted `pending` audit to `unknown` without calling any upstream collaborator.

- [ ] **Step 6: Run and commit persistence**

Run: `go test ./internal/store -v`

Expected: PASS.

```bash
git add internal/store
git commit -m "feat: add atomic plugin data repositories"
```

### Task 3: CLIProxyAPI Host Adapter and Safe Account Discovery

**Files:**
- Create: `internal/host/types.go`
- Create: `internal/host/client.go`
- Create: `internal/accounts/service.go`
- Test: `internal/host/client_test.go`
- Test: `internal/accounts/service_test.go`

**Interfaces:**
- Consumes: injected `host.Caller func(context.Context, string, any, any) error`.
- Produces: `host.API`, `accounts.Service.List(context.Context) ([]accounts.Account, error)`, `accounts.Service.Find(context.Context, string) (accounts.Account, error)`, `accounts.AuthMaterial(context.Context, host.API, accounts.Account) (accounts.Material, error)`.

- [ ] **Step 1: Write failing host-shape compatibility and redaction tests**

```go
func TestHTTPResponseAcceptsHostCasingVariants(t *testing.T) {
    for _, raw := range []string{
        `{"StatusCode":200,"Headers":{"X":["y"]},"Body":"T0s="}`,
        `{"status_code":200,"headers":{"X":["y"]},"body":"T0s="}`,
    } {
        var got HTTPResponse
        if err := json.Unmarshal([]byte(raw), &got); err != nil || string(got.Body) != "OK" { t.Fatalf("%s: %#v %v", raw, got, err) }
    }
}

func TestAccountProjectionNeverContainsToken(t *testing.T) {
    raw := json.RawMessage(`{"access_token":"secret","account_id":"acct-upstream"}`)
    view, err := Project(AuthFile{AuthIndex:"7", Email:"alice@example.com", Provider:"codex"}, raw)
    if err != nil { t.Fatal(err) }
    encoded, _ := json.Marshal(view)
    if bytes.Contains(encoded, []byte("secret")) || bytes.Contains(encoded, []byte("alice@example.com")) { t.Fatalf("leaked: %s", encoded) }
}
```

- [ ] **Step 2: Run tests and verify missing symbols**

Run: `go test ./internal/host ./internal/accounts -v`

Expected: FAIL because the adapter and projection do not exist.

- [ ] **Step 3: Implement typed host operations and account identity**

`host.Client` maps exactly to `host.auth.list`, `host.auth.get`, `host.http.do`, and `host.log`. Filter discovered credentials to Codex provider/type, derive stable `Account.Key` from `AuthIndex` (never email), create `Fingerprint` as the first 12 lowercase hex characters of SHA-256(account key), and mask email as `a***@example.com`. `accounts.AuthMaterial` parses token/account ID only immediately before an upstream call and returns `credential_error` without including raw JSON.

```go
type Account struct {
    Key string `json:"account_key"`
    AuthIndex string `json:"-"`
    MaskedIdentity string `json:"masked_identity"`
    PlanLabel string `json:"plan_label,omitempty"`
    Disabled bool `json:"disabled"`
    Unavailable bool `json:"unavailable"`
    Fingerprint string `json:"fingerprint"`
}
type Material struct { AccessToken string `json:"-"`; AccountID string `json:"-"` }
```

- [ ] **Step 4: Prove no secret-bearing type is JSON serializable**

Add reflection tests requiring `AccessToken` and `AuthIndex` to carry `json:"-"`, and scan marshaled account/error values for `access_token`, raw email, and fixture token. Verify unavailable accounts remain discoverable and disabled accounts remain visible.

- [ ] **Step 5: Run and commit the host boundary**

Run: `go test ./internal/host ./internal/accounts -v`

Expected: PASS.

```bash
git add internal/host internal/accounts
git commit -m "feat: add safe CLIProxyAPI account adapter"
```

### Task 4: Local-Time Planner and Deterministic Simulator

**Files:**
- Create: `internal/schedule/local_time.go`
- Create: `internal/schedule/planner.go`
- Create: `internal/simulate/service.go`
- Test: `internal/schedule/local_time_test.go`
- Test: `internal/schedule/planner_test.go`
- Test: `internal/simulate/service_test.go`

**Interfaces:**
- Consumes: validated `domain.Config`, local date, and account keys.
- Produces: `schedule.Planner.PlanDay(domain.Config, time.Time) ([]domain.PlannedOccurrence, error)`, `schedule.OccurrenceID(string, int, string) string`, `simulate.Service.Run(domain.Config, time.Time) (domain.SimulationResult, error)`.

- [ ] **Step 1: Write failing derivation, blackout, stagger, and DST tests**

```go
func TestPlanDayUsesPreheatFormulaAndDeterministicSlots(t *testing.T) {
    cfg := validConfig("Asia/Shanghai", "09:00", "12:00", 120, 60, []string{"acct-b", "acct-a"})
    first, err := PlanDay(cfg, mustDate("2026-09-14"))
    if err != nil { t.Fatal(err) }
    second, _ := PlanDay(cfg, mustDate("2026-09-14"))
    if !reflect.DeepEqual(first, second) { t.Fatalf("non-deterministic: %#v %#v", first, second) }
    assertWithin(t, first, "06:00", "07:00")
    assertStrictlyIncreasingInstants(t, first)
}

func TestPlannerNeverDuplicatesFallbackHour(t *testing.T) {
    cfg := validConfig("America/New_York", "03:30", "04:00", 30, 120, []string{"acct-a"})
    occurrences, err := PlanDay(cfg, mustDate("2026-11-01"))
    if err != nil { t.Fatal(err) }
    if len(uniqueIDs(occurrences)) != len(occurrences) { t.Fatal("duplicate local occurrence") }
}
```

Also test spring-forward gaps, an entirely nonexistent allowed interval producing `missed`, blackout splitting into two intervals, and zero remaining allowed time.

- [ ] **Step 2: Run planner tests and verify they fail**

Run: `go test ./internal/schedule ./internal/simulate -v`

Expected: FAIL because planner primitives do not exist.

- [ ] **Step 3: Implement local wall-time resolution and blackout subtraction**

Resolve a local minute by scanning UTC instants around the expected offset and selecting the earliest instant whose localized year/month/day/hour/minute match. For a nonexistent boundary, advance minute-by-minute to the first valid instant inside the wall-clock interval; if none exists, mark the occurrence missed. Construct identity as `YYYY-MM-DD/p<work-period-index>/<account-key>` so both instances of a repeated local hour share one ID.

```go
func OccurrenceID(date string, periodIndex int, accountKey string) string {
    return fmt.Sprintf("%s/p%d/%s", date, periodIndex, accountKey)
}

func rank(localOccurrence, accountKey string) [32]byte {
    return sha256.Sum256([]byte(localOccurrence + "\x00" + accountKey))
}
```

Call `rank(date+"/p"+strconv.Itoa(periodIndex), accountKey)`, sort account keys by that rank, concatenate remaining allowed intervals, divide total allowed duration by account count, and place each account at its slot midpoint. Never persist randomness. When no valid instant remains, return one `PlannedOccurrence{Missed:true, MissedReason:"no_allowed_time"}` per account so the scheduler can durably record every Missed Occurrence.

- [ ] **Step 4: Implement simulator strictly from planner primitives**

`simulate.Service.Run` must call `schedule.PlanDay` for its occurrence timeline. For each work period, compare a baseline Short Window starting at first work use with the configured planned activation; calculate integer `work_minutes`, `available_coverage_minutes`, `idle_window_minutes`, `net_gain_minutes`, and non-overlapping 24-hour `timeline_segments`. Include an `assumptions` object containing Productivity Estimate and never use a live Usage Snapshot.

- [ ] **Step 5: Add an equality test between simulation and planner instants**

For `Asia/Shanghai`, Monday `2026-09-14`, two work periods, and three accounts, assert every simulator preheat segment starts at exactly the instant returned by `PlanDay`, and assert changing snapshot fixtures has no effect because snapshots are not an input.

- [ ] **Step 6: Run and commit planning**

Run: `go test ./internal/schedule ./internal/simulate -v`

Expected: PASS.

```bash
git add internal/schedule internal/simulate
git commit -m "feat: plan deterministic preheat occurrences"
```

### Task 5: Strict Codex Probe Protocol

**Files:**
- Create: `internal/probe/sse.go`
- Create: `internal/probe/client.go`
- Test: `internal/probe/sse_test.go`
- Test: `internal/probe/client_test.go`
- Create: `NOTICE`

**Interfaces:**
- Consumes: `host.API`, `accounts.AuthMaterial`, account, model, timeout.
- Produces: `probe.New(host.API) *Client`, `(*Client).Execute(context.Context, accounts.Account, string, time.Duration) domain.ProbeResult`, `probe.ParseCompleted(io.Reader) (string, error)`.

- [ ] **Step 1: Write failing table tests for every accepted and rejected terminal shape**

```go
func TestParseCompleted(t *testing.T) {
    tests := []struct { name, input, want string; wantErr bool }{
        {"completed terminal", `data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"OK"}]}]}}` + "\n\n", "OK", false},
        {"done text", "data: {\"type\":\"response.output_text.done\",\"text\":\"OK\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{}}\n\n", "OK", false},
        {"deltas", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"O\"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"K\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{}}\n\n", "OK", false},
        {"done only", "data: [DONE]\n\n", "", true},
        {"failure before completion", "data: {\"type\":\"response.failed\"}\n\ndata: {\"type\":\"response.completed\"}\n", "", true},
        {"incomplete", "data: {\"type\":\"response.incomplete\"}\n", "", true},
        {"error", "data: {\"type\":\"error\",\"message\":\"bad\"}\n", "", true},
    }
    for _, tt := range tests { t.Run(tt.name, func(t *testing.T) { got, err := ParseCompleted(strings.NewReader(tt.input)); if (err != nil) != tt.wantErr || got != tt.want { t.Fatalf("got %q, %v", got, err) } }) }
}
```

Add a reader containing one `data:` line larger than 4 MiB and require `response_error` instead of scanner truncation or panic. Add completed-item and single-JSON response cases.

- [ ] **Step 2: Run parser tests and verify they fail**

Run: `go test ./internal/probe -run TestParseCompleted -v`

Expected: FAIL because `ParseCompleted` does not exist.

- [ ] **Step 3: Implement bounded SSE parsing and exact output validation**

Use `bufio.Scanner` with a 4 MiB maximum token, ignore non-`data:`/empty/`[DONE]` input, fail immediately on terminal failure events, require `response.completed`, and choose text in this order: completed terminal response, output-text done, completed item/content part, accumulated deltas. `Client.Execute` trims whitespace and accepts only `OK` exactly.

```go
const endpoint = "https://chatgpt.com/backend-api/codex/responses"
const maxEventBytes = 4 << 20

var requestTemplate = struct {
    Instructions string `json:"instructions"`
    Input []inputMessage `json:"input"`
    Stream bool `json:"stream"`
    Store bool `json:"store"`
    ParallelToolCalls bool `json:"parallel_tool_calls"`
    Include []string `json:"include"`
    Reasoning struct{ Effort string `json:"effort"` } `json:"reasoning"`
}{Instructions: "Return exactly OK.", Stream: true, Store: false, ParallelToolCalls: true, Include: []string{"reasoning.encrypted_content"}, Reasoning: struct{ Effort string `json:"effort"` }{Effort: "low"}}
```

Set input text to `Reply with exactly OK`; set `Authorization`, `Chatgpt-Account-Id`, `Content-Type`, `Accept`, `Originator: codex-tui`, and a Linux architecture-aware `User-Agent`. Never log or return these headers.

- [ ] **Step 4: Test the request body and complete Request Outcome mapping**

Use a fake host to return 200/401/403/402/429/500, timeout, network error, malformed terminal JSON, and `NOT OK`. Assert outcomes respectively: `succeeded`, `unauthorized`, `forbidden`, `payment_required`, `rate_limited`, `upstream_error`, `timeout`, `network_error`, `response_error`, `unexpected_output`. Assert only network, timeout, 429, and 5xx set `RetryEligible=true`.

- [ ] **Step 5: Record reference attribution and run tests**

Add to `NOTICE`:

```text
Codex Window Reset includes protocol behavior derived from codex-health-monitor
(https://github.com/tapaixx/codex-health-monitor), Copyright Cai Feng,
licensed under the MIT License. See LICENSE.
```

Run: `go test ./internal/probe -v`

Expected: PASS.

- [ ] **Step 6: Commit the probe protocol**

```bash
git add internal/probe NOTICE
git commit -m "feat: add strict Codex probe protocol"
```

### Task 6: Quota Parsing, Snapshot Cache, and Guardrail Holds

**Files:**
- Create: `internal/quota/parse.go`
- Create: `internal/quota/service.go`
- Test: `internal/quota/parse_test.go`
- Test: `internal/quota/service_test.go`

**Interfaces:**
- Consumes: host upstream HTTP, `accounts.Account`, runtime-state repository for holds.
- Produces: `quota.New(host.API, RuntimeStateRepository, domain.Clock) *Service`, `Refresh(context.Context, accounts.Account) (domain.UsageSnapshot, error)`, `Get(string, time.Time) (domain.SnapshotView, bool)`, `Reset(context.Context, accounts.Account, string) (domain.ResetHTTPResult, error)`, `ClearSnapshots()`, `Evaluate(domain.Config, string, time.Time) domain.QuotaDecision`.

- [ ] **Step 1: Write failing compatibility tests for usage windows and reset credits**

```go
func TestParseUsageSelectsShortestWindow(t *testing.T) {
    now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
    raw := []byte(`{"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":30,"reset_at":1788973200},"secondary_window":{"limit_window_seconds":604800,"used_percent":91,"reset_at":1789578000}},"rate_limit_reset_credits":{"available":2}}`)
    got, err := ParseUsage(raw, now)
    if err != nil { t.Fatal(err) }
    if got.Windows[0].DurationMinutes != 300 || got.Windows[0].RemainingPercent != 70 { t.Fatalf("short: %#v", got.Windows[0]) }
    if !got.Windows[0].Short || got.Windows[1].Short { t.Fatalf("classification: %#v", got.Windows) }
    if got.ResetApplicableCount == nil || *got.ResetApplicableCount != 2 { t.Fatalf("credits: %#v", got) }
}
```

Add fixtures for camelCase/snake_case payload variants, arbitrary recognized window lists, fraction-versus-percent usage, missing reset boundaries, and invalid/no recognized windows.

- [ ] **Step 2: Run quota tests and verify they fail**

Run: `go test ./internal/quota -v`

Expected: FAIL because parsing and service symbols do not exist.

- [ ] **Step 3: Implement refresh with account-level single-flight and stale-preserving errors**

```go
type entry struct {
    Snapshot domain.UsageSnapshot
    LastError *domain.Error
    LastAttemptAt time.Time
}
type flight struct { done chan struct{}; snapshot domain.UsageSnapshot; err error }
type Service struct {
    mu sync.RWMutex
    entries map[string]entry
    flights map[string]*flight
    host host.API
    states store.RuntimeStateRepository
    clock domain.Clock
}
```

On refresh, one owner performs GET `/backend-api/wham/usage` and GET `/backend-api/wham/rate-limit-reset-credits`; concurrent callers wait on the same flight. On failure, retain the last successful snapshot, set the internal `entry.LastError`/`LastAttemptAt`, expose only `SnapshotView.RefreshErrorCode`, and return `quota_refresh_failed`. `Get` marks snapshots older than five minutes as stale without deleting them. A test must prove `Get` performs zero host calls.

Set `ResetInfoComplete=true` only when the reset-credit endpoint returned and parsed successfully; an embedded count in the usage response may be displayed as partial data but cannot authorize Quota Reset.

- [ ] **Step 4: Implement Guardrail Hold transitions and sufficient-window decisions**

```go
func decide(snapshot domain.SnapshotView, hold *domain.GuardrailHold, cfg domain.Config, now time.Time) domain.QuotaDecision {
    if hold != nil && (snapshot.RefreshErrorCode != "" || snapshot.Stale) { return domain.DecisionGuardrailHold }
    if snapshot.RefreshErrorCode != "" && hold == nil { return domain.DecisionUnknownFailOpen }
    if anyLongAtOrBelow(snapshot.Snapshot, cfg.LongWindowFloorPercent) { return domain.DecisionGuardrailHold }
    short := shortestWindow(snapshot.Snapshot.Windows)
    if !snapshot.Stale && short.RemainingPercent >= cfg.RemainingQuotaFloorPercent && short.ResetAt.Sub(now) >= time.Duration(cfg.RemainingWindowFloorMinutes)*time.Minute {
        return domain.DecisionSufficientWindow
    }
    return domain.DecisionProceed
}
```

A successful refresh persists a hold when any Long Window is at or below the floor and clears it only when every recognized Long Window is above the floor. A failed refresh never clears a hold. Verify a stale snapshot cannot yield sufficient-window, while a stale known hold still blocks after restart.

- [ ] **Step 5: Implement reset HTTP primitive without idempotency policy**

`Service.Reset` posts `{"redeem_request_id":"<idempotency-key>"}` to `/backend-api/wham/rate-limit-reset-credits/consume`, returns only status category/correlation-safe error, and does not retry. The application layer in Task 9 owns pending audit/idempotency and calls `Refresh` afterward.

- [ ] **Step 6: Run and commit quota behavior**

Run: `go test ./internal/quota -v`

Expected: PASS, including single-flight (upstream call count exactly one), three-worker independence, five-minute stale boundary, fail-open, hold persistence, and reset body.

```bash
git add internal/quota
git commit -m "feat: add quota snapshots and persistent guardrails"
```

### Task 7: Probe Orchestration and Separate Window Outcomes

**Files:**
- Create: `internal/app/runtime.go`
- Create: `internal/app/interfaces.go`
- Create: `internal/app/manual.go`
- Create: `internal/app/preheat.go`
- Create: `internal/app/status.go`
- Test: `internal/app/manual_test.go`
- Test: `internal/app/preheat_test.go`
- Test: `internal/app/outcomes_test.go`

**Interfaces:**
- Consumes: account, probe, quota, repository, planner, and clock interfaces.
- Produces: `app.New(Dependencies) (*Runtime, error)`, `Runtime.StartManualProbes(context.Context, []string, bool) (string, error)`, `Runtime.RefreshQuotas(context.Context, []string) ([]domain.SnapshotView, error)`, `Runtime.ExecutePreheat(context.Context, domain.PlannedOccurrence) domain.OperationRecord`, `Runtime.Status() domain.StatusView`, `Runtime.Stop()`.

- [ ] **Step 1: Write failing manual-run concurrency and eligibility tests**

```go
func TestManualProbeLimitsConcurrencyToThree(t *testing.T) {
    fx := newFixture(t, 5)
    runID, err := fx.runtime.StartManualProbes(context.Background(), fx.keys(), true)
    if err != nil || runID == "" { t.Fatalf("%q %v", runID, err) }
    fx.probes.ReleaseAll()
    fx.runtime.WaitRun(runID)
    if fx.probes.MaxConcurrent() != 3 { t.Fatalf("max=%d", fx.probes.MaxConcurrent()) }
}

func TestManualProbeAllowsUnavailableOverrideButRejectsDisabled(t *testing.T) {
    fx := newFixtureWithAccounts(t, unavailable("a"), disabled("b"))
    if _, err := fx.runtime.StartManualProbes(context.Background(), []string{"a"}, false); domain.CodeOf(err) != domain.CodeAccountUnavailable { t.Fatalf("got %v", err) }
    if _, err := fx.runtime.StartManualProbes(context.Background(), []string{"a"}, true); err != nil { t.Fatal(err) }
    if _, err := fx.runtime.StartManualProbes(context.Background(), []string{"b"}, true); domain.CodeOf(err) != domain.CodeAccountDisabled { t.Fatalf("got %v", err) }
}
```

Add tests that a second bulk run gets `run_in_progress` and two operations on the same account get `account_busy`.

- [ ] **Step 2: Run app tests and verify they fail**

Run: `go test ./internal/app -run 'TestManualProbe' -v`

Expected: FAIL because Runtime does not exist.

- [ ] **Step 3: Implement Runtime ownership and manual worker pool**

```go
type Dependencies struct {
    Accounts AccountService
    Probe ProbeExecutor
    Quota QuotaService
    Config store.ConfigRepository
    History store.HistoryRepository
    State store.RuntimeStateRepository
    Audit store.ResetAuditRepository
    Clock domain.Clock
    IDs func() string
}
type Runtime struct {
    mu sync.RWMutex
    deps Dependencies
    config domain.Config
    run *runState
    busy map[string]struct{}
    stop chan struct{}
    wg sync.WaitGroup
}
```

Manual runs use exactly `min(3, len(accounts))` workers. Acquire/release `busy[accountKey]` around each account use case. Always try quota refresh before and after the probe; a failed pre-refresh does not block a manual action. Never hold `Runtime.mu` during host I/O.

`RefreshQuotas` rejects an empty or duplicate account-key list, uses the same maximum of three workers, and returns one ordered `SnapshotView` per requested key. It calls only quota refresh—never probe—and preserves a prior successful snapshot when one account fails.

- [ ] **Step 4: Write failing Request/Window Outcome matrix tests**

```go
func TestClassifyWindowOutcome(t *testing.T) {
    resetA := time.Date(2026, 9, 9, 15, 0, 0, 0, time.UTC)
    resetB := resetA.Add(5 * time.Hour)
    cases := []struct { name string; request domain.RequestOutcome; before, after *domain.UsageSnapshot; want domain.WindowOutcome }{
        {"new boundary", domain.RequestSucceeded, snapshot(resetA, false), snapshot(resetB, true), domain.WindowVerifiedStarted},
        {"active before", domain.RequestSucceeded, snapshot(resetA, true), snapshot(resetA, true), domain.WindowAlreadyActive},
        {"same inactive boundary", domain.RequestSucceeded, snapshot(resetA, false), snapshot(resetA, false), domain.WindowUnchanged},
        {"missing after", domain.RequestSucceeded, snapshot(resetA, false), nil, domain.WindowUnverified},
        {"request failed", domain.RequestRateLimited, snapshot(resetA, false), nil, domain.WindowNotObserved},
    }
    for _, tc := range cases { if got := classifyWindow(tc.request, tc.before, tc.after); got != tc.want { t.Errorf("%s: %s", tc.name, got) } }
}
```

- [ ] **Step 5: Implement automatic preheat decision records and compensation eligibility**

`ExecutePreheat` rechecks enabled/selected state, skips Disabled/Unavailable, refreshes quota, records `guardrail_hold`, `sufficient_window`, or `quota_unknown_fail_open`, executes one probe, refreshes after it, and appends exactly one Operation Record with independent outcomes. Only failed automatic requests with network/timeout/429/5xx outcomes schedule `CompensationDueAt = now + 5m`; successful-but-unverified requests never do.

- [ ] **Step 6: Run and commit orchestration**

Run: `go test ./internal/app -run 'Test(Manual|Classify|Preheat|Compensation)' -v`

Expected: PASS.

```bash
git add internal/app
git commit -m "feat: orchestrate manual and scheduled probes"
```

### Task 8: Restart-Safe Scheduler and One-Shot Compensation

**Files:**
- Create: `internal/schedule/scheduler.go`
- Test: `internal/schedule/scheduler_test.go`
- Modify: `internal/app/runtime.go`
- Modify: `internal/app/preheat.go`
- Test: `internal/app/restart_test.go`

**Interfaces:**
- Consumes: `schedule.PlanDay`, clock/timer, persisted `domain.RuntimeState`, `Runtime.ExecutePreheat`.
- Produces: `schedule.NewScheduler(domain.Clock, schedule.Planner, RuntimeStateRepository, Executor) *Scheduler`, `Scheduler.Reconcile(domain.Config) error`, `Scheduler.Start()`, `Scheduler.Stop()`.

- [ ] **Step 1: Write failing startup-miss and no-catch-up tests**

```go
func TestStartupMarksEveryUnexecutedPastPlanMissed(t *testing.T) {
    now := instant("2026-09-09T06:30:00Z")
    state := domain.RuntimeState{Occurrences: map[string]domain.OccurrenceState{
        "2026-09-09/p0/a": {ID:"2026-09-09/p0/a", PlannedAt: instant("2026-09-09T06:45:00Z"), Status:domain.OccurrencePlanned},
    }}
    s := newSchedulerFixture(t, now, state)
    s.Start()
    got := s.saved().Occurrences["2026-09-09/p0/a"]
    if got.Status != domain.OccurrenceMissed || s.executor.Calls() != 0 { t.Fatalf("state=%#v calls=%d", got, s.executor.Calls()) }
}
```

The planned instant is deliberately still in the nominal window; the assertion proves restart never catches it up. Add a test for a newly planned future occurrence firing once.

- [ ] **Step 2: Run scheduler tests and verify they fail**

Run: `go test ./internal/schedule ./internal/app -run 'TestStartup|TestFutureOccurrence' -v`

Expected: FAIL because Scheduler does not exist.

- [ ] **Step 3: Implement one owner goroutine and persisted transitions**

At `Start`, load state and atomically persist all `planned` occurrences from earlier process ownership as `missed` before setting timers. `Reconcile` computes today plus the next seven local dates, adds only unseen identities, immediately persists planner results marked `Missed`, cancels timers for removed selections, and saves `NextRuns`; Disabled/Unavailable host state never edits Scheduled Account membership. Schedule a reconciliation callback for the next local midnight, and reconcile again after every occurrence callback, so the seven-day horizon advances without polling. Timer callbacks transition `planned -> running -> succeeded|failed|skipped`, saving before and after external I/O.

```go
type Executor interface { ExecutePreheat(context.Context, domain.PlannedOccurrence) domain.OperationRecord }
type Scheduler struct {
    mu sync.Mutex
    clock domain.Clock
    planner Planner
    states store.RuntimeStateRepository
    executor Executor
    timers map[string]Timer
    stopped bool
}
```

- [ ] **Step 4: Add failing compensation restart tests**

Persist an occurrence with `CompensationDueAt=2026-09-09T07:05:00Z` and `CompensationAttempted=false`; assert it fires once if the process stayed alive, but is marked missed on restart if ownership was lost. Assert authentication, authorization, payment, validation, disabled, and unexpected-output outcomes never create a compensation timer.

- [ ] **Step 5: Implement eligibility recheck at compensation time**

Before the one allowed compensation, re-read config/account state, require enabled + still selected + not Disabled/Unavailable + no Guardrail Hold, set `CompensationAttempted=true` and persist before calling probe. Do not schedule another compensation regardless of its result.

- [ ] **Step 6: Run and commit scheduler**

Run: `go test ./internal/schedule ./internal/app -run 'Test(Start|Future|Compensation|Reconcile)' -v`

Expected: PASS under the fake clock with zero wall-clock sleeps.

```bash
git add internal/schedule/scheduler.go internal/schedule/scheduler_test.go internal/app/runtime.go internal/app/preheat.go internal/app/restart_test.go
git commit -m "feat: add restart-safe preheat scheduler"
```

### Task 9: Durable Quota Reset Idempotency and Audit

**Files:**
- Create: `internal/app/reset.go`
- Test: `internal/app/reset_test.go`
- Modify: `internal/domain/reset.go`
- Modify: `internal/store/reset_audit.go`

**Interfaces:**
- Consumes: account discovery, `QuotaService.Refresh/Reset`, Reset Audit repository, Runtime account single-flight.
- Produces: `Runtime.ResetQuota(context.Context, string, string) (domain.ResetAudit, error)`, `Runtime.ListResetAudit() ([]domain.ResetAudit, error)`, `Runtime.ClearResetAudit(string) error`.

- [ ] **Step 1: Write failing pending-before-consume and replay tests**

```go
func TestResetPersistsPendingBeforeConsumeAndReplaysOutcome(t *testing.T) {
    fx := newResetFixture(t)
    key := "11111111-1111-4111-8111-111111111111"
    fx.quota.OnReset = func() {
        records, err := fx.audit.Load()
        if err != nil || len(records) != 1 || records[0].Outcome != domain.ResetPending { t.Fatalf("audit before consume: %#v %v", records, err) }
    }
    first, err := fx.runtime.ResetQuota(context.Background(), "acct-a", key)
    if err != nil || first.Outcome != domain.ResetSucceeded { t.Fatalf("%#v %v", first, err) }
    second, err := fx.runtime.ResetQuota(context.Background(), "acct-a", key)
    if err != nil || second.Outcome != domain.ResetSucceeded || fx.quota.ResetCalls() != 1 { t.Fatalf("%#v calls=%d err=%v", second, fx.quota.ResetCalls(), err) }
}
```

Add tests for the same key with a different account (`idempotency_conflict`), invalid UUID, zero applicable credits, disabled account, and a second simultaneous key on the same account (`account_busy`).

- [ ] **Step 2: Run reset tests and verify they fail**

Run: `go test ./internal/app -run TestReset -v`

Expected: FAIL because `Runtime.ResetQuota` does not exist.

- [ ] **Step 3: Implement the reset state machine**

Validate UUID with a strict canonical regex for versions 1-5 and RFC 4122 variant. Under the audit repository lock, return an existing final same-account record immediately or reject cross-account reuse. If the same key is pending in the current process, wait for its account flight and return the reloaded final record; startup invokes `RecoverPending` before accepting traffic, so an orphaned pending key is already `unknown`. Acquire account single-flight, require an enabled account and a successful fresh quota/reset-credit refresh with `ResetInfoComplete=true` and `ResetApplicableCount > 0`, append pending audit, call consume once, refresh once after the call, then replace the record.

```go
func finalResetOutcome(result domain.ResetHTTPResult, callErr error) domain.ResetOutcome {
    if callErr == nil && result.StatusCode >= 200 && result.StatusCode < 300 { return domain.ResetSucceeded }
    if result.StatusCode == 0 || errors.Is(callErr, context.DeadlineExceeded) { return domain.ResetUnknown }
    return domain.ResetFailed
}
```

Store only idempotency key, requested/finished timestamps, stable account key, masked identity snapshot, prior applicable-credit count, outcome, HTTP category, and correlation ID. Do not store raw body or auth material. If the final audit replace fails after consume, return `reset_outcome_unknown`; the pending record remains evidence and becomes unknown after restart.

- [ ] **Step 4: Test post-reset refresh and crash ambiguity**

Assert every attempted upstream consume is followed by one refresh, including a definite HTTP failure. Inject a process-stop point after the pending append and before consume; reconstruct repositories/runtime and assert the record becomes `unknown`, the consume call count stays zero, and replaying the key returns unknown without retry.

- [ ] **Step 5: Test audit deletion authorization**

```go
func TestClearResetAuditRequiresExactPhrase(t *testing.T) {
    fx := newResetFixture(t)
    for _, phrase := range []string{"", "delete audit", "DELETE AUDIT ", " DELETE AUDIT"} {
        if err := fx.runtime.ClearResetAudit(phrase); err == nil { t.Fatalf("accepted %q", phrase) }
    }
    if err := fx.runtime.ClearResetAudit("DELETE AUDIT"); err != nil { t.Fatal(err) }
}
```

- [ ] **Step 6: Run and commit reset behavior**

Run: `go test ./internal/app -run 'Test(Reset|ClearResetAudit)' -v`

Expected: PASS.

```bash
git add internal/app/reset.go internal/app/reset_test.go internal/domain/reset.go internal/store/reset_audit.go
git commit -m "feat: make quota reset durable and idempotent"
```

### Task 10: Schedule Updates, Management API, and Exact Asset Registration

**Files:**
- Create: `internal/management/types.go`
- Create: `internal/management/router.go`
- Create: `internal/management/assets.go`
- Test: `internal/management/router_test.go`
- Test: `internal/management/contract_test.go`
- Modify: `internal/app/runtime.go`
- Create: `web/panel.html`
- Create: `web/styles.css`
- Create: `web/modules/api.js`
- Create: `web/modules/state.js`
- Create: `web/modules/accounts.js`
- Create: `web/modules/schedule.js`
- Create: `web/modules/simulator.js`
- Create: `web/modules/history.js`
- Create: `web/modules/main.js`

**Interfaces:**
- Consumes: all Runtime use cases and embedded `web/*` files.
- Produces: `management.NewRouter(RuntimeAPI, Assets) *Router`, `Router.Handle(Request) Response`, `management.Registration(pluginID string) Registration`, `Runtime.UpdateSchedule(context.Context, domain.Config) (domain.Config, error)`.

- [ ] **Step 1: Write failing route table and response-envelope tests**

```go
func TestRegistrationDeclaresEveryExactRouteAndAsset(t *testing.T) {
    got := Registration("codex-window-reset-linux-amd64")
    assertRouteSet(t, got.Routes, map[string]string{
        "GET /plugins/codex-window-reset-linux-amd64/status":"", "GET /plugins/codex-window-reset-linux-amd64/accounts":"",
        "GET /plugins/codex-window-reset-linux-amd64/schedule":"", "PUT /plugins/codex-window-reset-linux-amd64/schedule":"",
        "POST /plugins/codex-window-reset-linux-amd64/simulate":"", "POST /plugins/codex-window-reset-linux-amd64/probes":"",
        "GET /plugins/codex-window-reset-linux-amd64/history":"", "DELETE /plugins/codex-window-reset-linux-amd64/history":"",
        "GET /plugins/codex-window-reset-linux-amd64/quota":"", "POST /plugins/codex-window-reset-linux-amd64/quota/refresh":"",
        "POST /plugins/codex-window-reset-linux-amd64/quota/reset":"", "GET /plugins/codex-window-reset-linux-amd64/reset-audit":"",
        "DELETE /plugins/codex-window-reset-linux-amd64/reset-audit":"",
    })
    assertResourcePaths(t, got.Resources, []string{"/panel", "/styles.css", "/modules/api.js", "/modules/state.js", "/modules/accounts.js", "/modules/schedule.js", "/modules/simulator.js", "/modules/history.js", "/modules/main.js"})
    if got.Resources[0].Menu == "" { t.Fatal("panel must be the menu resource") }
    for _, asset := range got.Resources[1:] { if asset.Menu != "" { t.Fatalf("asset leaked into menu: %#v", asset) } }
}
```

Test an undeclared `/modules/missing.js` path returns 404, a status success encodes as `{"ok":true,"result":{"enabled":false}}`, and every error contains code/message/retryable/correlation_id.

- [ ] **Step 2: Run management tests and verify they fail**

Run: `go test ./internal/management -v`

Expected: FAIL because management package does not exist.

- [ ] **Step 3: Implement dynamic path normalization and embedded assets**

Use `//go:embed` from a Go file at a common ancestor of `web`; if `internal/management/assets.go` cannot embed `../../web`, create root package `assets.go` with `//go:embed web/* web/modules/*` and pass its `fs.FS` into `management.NewAssets`. Do not duplicate generated strings.

```go
var assetTypes = map[string]string{
    "/panel":"text/html; charset=utf-8", "/styles.css":"text/css; charset=utf-8",
    "/modules/api.js":"text/javascript; charset=utf-8", "/modules/state.js":"text/javascript; charset=utf-8",
    "/modules/accounts.js":"text/javascript; charset=utf-8", "/modules/schedule.js":"text/javascript; charset=utf-8",
    "/modules/simulator.js":"text/javascript; charset=utf-8", "/modules/history.js":"text/javascript; charset=utf-8",
    "/modules/main.js":"text/javascript; charset=utf-8",
}
```

Accept `ResourceBasePath`, `resource_base_path`, or `resourceBasePath`; extract one safe plugin ID from `/v0/resource/plugins/<id>` and fall back to `codex-window-reset` on slash/query/whitespace. Normalize incoming resource and management prefixes without hardcoding a suffixed runtime ID.

- [ ] **Step 4: Implement optimistic schedule replacement**

`PUT /schedule` decodes one full `domain.Config`; under the Runtime config lock compare caller revision, validate against currently discovered account keys, set `Revision=current+1`, persist, then swap memory and reconcile scheduler. On mismatch return HTTP 409 `revision_conflict` without writing. If persistence fails, retain the old in-memory configuration.

```go
func (r *Runtime) UpdateSchedule(ctx context.Context, draft domain.Config) (domain.Config, error) {
    r.configMu.Lock()
    defer r.configMu.Unlock()
    if draft.Revision != r.config.Revision { return domain.Config{}, domain.NewError(domain.CodeRevisionConflict, 409, false, "schedule revision changed") }
    keys, err := r.discoveredKeys(ctx); if err != nil { return domain.Config{}, err }
    if err := domain.ValidateConfig(draft, domain.ValidatePersisted, keys); err != nil { return domain.Config{}, err }
    draft.Revision++
    if err := r.deps.Config.Save(draft); err != nil { return domain.Config{}, err }
    r.config = draft
    return draft, r.scheduler.Reconcile(draft)
}
```

- [ ] **Step 5: Implement all route contracts**

Map exactly the methods/paths from the spec. `POST /probes` requires non-empty account keys and `acknowledge_quota_effect:true`, returns 202/run ID, and reports overlapping runs as 409. `POST /quota/refresh` accepts `account_keys` and uses Runtime's three-worker helper. `POST /quota/reset` accepts only `account_key` and `idempotency_key`; reject unknown JSON fields. `DELETE /history` clears history plus in-memory snapshots only. `DELETE /reset-audit` passes the exact `X-Confirmation` header value.

- [ ] **Step 6: Add contract tests for all methods, secrets, and auth ownership**

For each route assert success status, malformed JSON 400, wrong method 405, and stable response content type. Marshal every response with fake token `sk-secret`, raw email, raw auth JSON, and management key fixtures available to dependencies; assert none appear. Assert router does not read/write plugin key files and resources contain no key prompt, `localStorage`, or `sessionStorage` credential fallback.

- [ ] **Step 7: Run and commit the management boundary**

Run: `go test ./internal/management ./internal/app -run 'Test(Registration|Router|Schedule|Management|Secret)' -v`

Expected: PASS.

```bash
git add internal/management internal/app/runtime.go assets.go web
git commit -m "feat: expose authenticated management contracts"
```

### Task 11: Operations Panel State, Accessibility, and Responsive UI

**Files:**
- Modify: `web/panel.html`
- Modify: `web/styles.css`
- Modify: `web/modules/api.js`
- Modify: `web/modules/state.js`
- Modify: `web/modules/accounts.js`
- Modify: `web/modules/schedule.js`
- Modify: `web/modules/simulator.js`
- Modify: `web/modules/history.js`
- Modify: `web/modules/main.js`
- Create: `web/tests/api.test.mjs`
- Create: `web/tests/state.test.mjs`
- Create: `web/tests/markup.test.mjs`
- Create: `package.json`

**Interfaces:**
- Consumes: management envelope and the runtime `resource_base_path` encoded into panel bootstrap metadata.
- Produces: browser-native `deriveManagementBase`, `request`, `createStore`, render functions, Chinese error localization, and the complete panel.

- [ ] **Step 1: Write failing API-path, credential-storage, and reducer tests**

```js
// web/tests/api.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { deriveManagementBase } from '../modules/api.js';

test('derives management path from a suffixed resource path', () => {
  assert.equal(deriveManagementBase('/v0/resource/plugins/codex-window-reset-linux-arm64/panel'), '/v0/management/plugins/codex-window-reset-linux-arm64');
});
test('does not own authentication storage', async () => {
  const source = await readFile(new URL('../modules/api.js', import.meta.url), 'utf8');
  assert.equal(/localStorage|sessionStorage|management[_ -]?key/i.test(source), false);
});
```

```js
// web/tests/state.test.mjs
test('restore changes returns to the last server revision', () => {
  const store = createStore({ schedule: { revision: 7, enabled: false } });
  store.dispatch({ type: 'edit-schedule', patch: { enabled: true } });
  store.dispatch({ type: 'restore-schedule' });
  assert.deepEqual(store.getState().draftSchedule, { revision: 7, enabled: false });
});
```

Also assert Action Selection does not mutate Scheduled Account membership, refresh actions do not start polling timers, and reset submission generates one UUID and preserves it on a retry.

- [ ] **Step 2: Run browser tests and verify they fail**

Run: `node --test web/tests/*.test.mjs`

Expected: FAIL because browser modules do not export the tested functions.

- [ ] **Step 3: Implement the panel shell and design tokens**

`panel.html` contains header, five summary cards, accounts region, and three workspace tabs. Use semantic buttons, tables, forms, meter text, `aria-live` run status, labels for every input, and a standard `<dialog>` for one-account reset confirmation. Do not add a management-key input.

```css
:root {
  --primary:#2563eb; --background:#f8fafc; --surface:#fff; --foreground:#1e293b;
  --muted:#475569; --border:#e2e8f0; --success:#059669; --warning:#d97706;
  --destructive:#dc2626; --focus:#2563eb;
  font-family:Inter,"Noto Sans SC","Microsoft YaHei",system-ui,sans-serif;
}
:focus-visible { outline:3px solid var(--focus); outline-offset:2px; }
button, input, select { min-height:44px; }
@media (max-width:767px) { .account-table thead { display:none; } .account-table tr { display:grid; width:100%; } body { overflow-x:hidden; } }
@media (prefers-reduced-motion:reduce) { *,*::before,*::after { animation-duration:.01ms!important; transition-duration:.01ms!important; } }
```

Use restrained 150–250 ms transitions and status text/icons in addition to colors. At widths 375/768/1024/1440, no page-level horizontal overflow is permitted.

Add `syncHostTheme()` in `main.js`: read a same-origin parent document's `data-theme` or `.dark` signal when present, observe only those attributes with `MutationObserver`, and otherwise use `prefers-color-scheme`. Do not read CLIProxyAPI theme or authentication from browser storage. Keep the specified light palette as the initial/default render.

- [ ] **Step 4: Implement API and state modules without polling**

`api.js` calls `fetch` with `credentials:'same-origin'`, JSON headers, and the derived management base. It neither reads nor writes local/session storage. If HTTP 401/403 occurs, dispatch `host-auth-required` so the UI links back to host login. `state.js` holds the server schedule and a separate draft, ephemeral action selection, session-only identity reveal set, current run status, and last errors.

```js
export async function request(path, options = {}) {
  const response = await fetch(`${deriveManagementBase(location.pathname)}${path}`, { credentials:'same-origin', ...options, headers:{'Content-Type':'application/json', ...(options.headers||{})} });
  const envelope = await response.json();
  if (!response.ok || !envelope.ok) throw Object.assign(new Error(envelope.error?.message || `HTTP ${response.status}`), envelope.error, { status:response.status });
  return envelope.result;
}
```

- [ ] **Step 5: Implement accounts and operations UX**

Render desktop rows/mobile cards with separate Scheduled toggle and Action Selection checkbox, masked identity/reveal for current session, plan, health, Short/Long bars, credits, next occurrence, Request Outcome, Window Outcome, HTTP/latency, capture time/stale label, and sanitized error. Manual probe requires selections plus an explicit quota-effect warning acknowledgement. Reset is enabled for exactly one selected account after successful current refresh and displays account + applicable credits in the dialog; call `crypto.randomUUID()` only when opening a new reset intent.

- [ ] **Step 6: Implement schedule, simulator, history, and audit workspaces**

Schedule editor covers every Config field, keeps probe model/timeout in a collapsed advanced section, validates required lead/span before enable, and labels Work Period suggestions as suggestions rather than defaults. Simulator posts the draft config and renders assumptions separately from observed quota, a 24-hour timeline, Available Coverage, Idle Window, and net gain without “recommendation” wording. History shows the latest 100 operation records with separate outcomes; Reset Audit is a separate table and deletion dialog requires typing `DELETE AUDIT`.

- [ ] **Step 7: Add markup/accessibility static tests**

Read `panel.html`, CSS, and modules as text. Assert unique element IDs, buttons have text or `aria-label`, tab/tabpanel links exist, reset uses `<dialog>`, all identity outputs start masked, all asset imports resolve, each module export name is unique, tokens equal the spec values, media queries include 767 px and reduced motion, and forbidden strings `access_token`, `management key`, `localStorage`, and `sessionStorage` do not occur in production web files.

Add a contrast helper test for every foreground/background token pair used for normal text and require WCAG ratio `>= 4.5`. The theme synchronization test supplies a fake parent root with `data-theme="dark"` and asserts the panel root changes without scheduling an interval.

- [ ] **Step 8: Run and commit the complete panel**

Run: `node --test web/tests/*.test.mjs`

Expected: PASS.

```bash
git add package.json web
git commit -m "feat: build responsive window operations panel"
```

### Task 12: C-Shared ABI Lifecycle and End-to-End Plugin Dispatch

**Files:**
- Create: `main.go`
- Create: `main_test.go`
- Create: `integration_test.go`
- Modify: `version.go`
- Modify: `assets.go`

**Interfaces:**
- Consumes: CLIProxyAPI ABI v1 callbacks, `app.Runtime`, management router/assets.
- Produces: exported `cliproxy_plugin_init`, `cliproxyPluginCall`, `cliproxyPluginFree`, `cliproxyPluginShutdown`; dispatch for `plugin.register`, `plugin.reconfigure`, `management.register`, and `management.handle`.

- [ ] **Step 1: Write failing registration and lifecycle tests**

```go
func TestDispatchRegistersManagementAndDynamicResources(t *testing.T) {
    installTestRuntime(t)
    got, err := dispatch("management.register", []byte(`{"resource_base_path":"/v0/resource/plugins/codex-window-reset-linux-amd64"}`))
    if err != nil { t.Fatal(err) }
    encoded, _ := json.Marshal(got)
    if !bytes.Contains(encoded, []byte("/plugins/codex-window-reset-linux-amd64/status")) || !bytes.Contains(encoded, []byte("/modules/main.js")) { t.Fatalf("%s", encoded) }
}

func TestShutdownStopsSchedulerAndClearsRuntime(t *testing.T) {
    rt := installTestRuntime(t)
    cliproxyPluginShutdown()
    if currentRuntime() != nil || !rt.Stopped() { t.Fatal("runtime still active") }
}
```

Add tests for nil/wrong ABI init, missing runtime, unsupported method, buffer allocation/free, repeated init stopping the old Runtime, and both snake/camel/Pascal resource-base keys.

- [ ] **Step 2: Run root tests and verify they fail**

Run: `go test . -v`

Expected: FAIL because ABI and dispatch symbols do not exist.

- [ ] **Step 3: Implement the minimal C bridge and synchronized Runtime pointer**

Port the ABI v1 struct layout and host call/free shims from the attributed reference project. `main.go` contains C exports and dispatch only. Keep the single allowed package-global mutable value:

```go
var runtimeSlot struct {
    sync.RWMutex
    runtime *app.Runtime
}
```

Wrap the C callback as `host.Caller`, copying request/response buffers exactly once and always calling the host's free function. Allocate plugin response memory with `C.malloc`; `cliproxyPluginFree` calls `C.free`. Never retain a C pointer in Go state except the host table held by the C shim.

Route all structured logs through one helper that accepts only correlation ID, stable account fingerprint, domain error code, latency milliseconds, and HTTP category. Add a test logger that fails if field names include token, auth, email, body, header, key, or credential.

- [ ] **Step 4: Wire startup, corruption behavior, and shutdown**

Resolve the data directory as `/CLIProxyAPI/plugins/codex-window-reset` when `/CLIProxyAPI/plugins` exists, otherwise `plugins/codex-window-reset`. On init, build repositories and Runtime. A corrupt config sets status error `store_corrupt`, leaves scheduling disabled, and still constructs management/router so account diagnostics and manual refresh remain reachable. `Stop` cancels timers/context and waits for all owned goroutines before clearing the pointer.

- [ ] **Step 5: Add end-to-end in-process management tests**

Through `dispatch("management.handle", requestJSON)`, where `requestJSON` is each concrete request fixture in a table, exercise: inert first startup; account listing; revision-checked schedule enable; deterministic next run; manual probe 202; quota refresh; reset pending/final audit; ordinary history clear preserving audit; explicit audit clear. Use only fake host callbacks and temporary data. Inspect every persisted and returned byte sequence to prove the fake token, management key, raw email, and upstream body are absent.

- [ ] **Step 6: Run race-enabled backend tests**

Run: `go test ./...`

Expected: PASS.

Run: `go test -race ./...`

Expected: PASS with no data races across scheduler, refresh, manual probe, reset, or shutdown tests.

- [ ] **Step 7: Commit ABI integration**

```bash
git add main.go main_test.go integration_test.go version.go assets.go
git commit -m "feat: integrate CLIProxyAPI plugin lifecycle"
```

### Task 13: Linux Release Packaging, CI, and Operator Documentation

**Files:**
- Create: `LICENSE`
- Create: `README.md`
- Create: `registry.json`
- Create: `scripts/package-release.sh`
- Create: `.github/workflows/ci.yml`
- Create: `.github/workflows/release.yml`
- Modify: `.gitignore`
- Test: `scripts/package_release_test.sh`

**Interfaces:**
- Consumes: tagged version, Go module, C toolchains.
- Produces: two `.so` files, two plugin-store ZIPs, two individual `.sha256` files, and `checksums.txt`.

- [ ] **Step 1: Write a failing release-layout smoke test**

```bash
#!/usr/bin/env bash
set -euo pipefail
release_dir="${1:?release directory required}"
version="${2:?version required}"
for arch in amd64 arm64; do
  test -f "${release_dir}/codex-window-reset-linux-${arch}.so"
  test -f "${release_dir}/codex-window-reset_${version}_linux_${arch}.zip"
  test -f "${release_dir}/codex-window-reset-linux-${arch}.so.sha256"
  unzip -Z1 "${release_dir}/codex-window-reset_${version}_linux_${arch}.zip" | diff -u - <(printf 'codex-window-reset.so\n')
done
test -f "${release_dir}/checksums.txt"
test "$(wc -l < "${release_dir}/checksums.txt")" -eq 4
sha256sum --check "${release_dir}/checksums.txt"
```

- [ ] **Step 2: Implement deterministic packaging**

`scripts/package-release.sh VERSION OUTPUT_DIR` validates semantic version and existing architecture libraries, copies the ZIP entry `codex-window-reset.so`, runs `zip -X`, emits individual SHA-256 files for the `.so` files, and creates a sorted aggregate manifest covering both `.so` and both ZIP files. It must refuse unsupported architectures and must not download tools.

- [ ] **Step 3: Add continuous integration**

`ci.yml` runs on pushes and pull requests with Go 1.24 and the repository's available Node LTS:

```yaml
- run: go test ./...
- run: go test -race ./...
- run: go vet ./...
- run: node --test web/tests/*.test.mjs
- run: CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -buildmode=c-shared -o /tmp/codex-window-reset-amd64.so .
```

Install `gcc-aarch64-linux-gnu`, then build arm64 with `CC=aarch64-linux-gnu-gcc CGO_ENABLED=1 GOOS=linux GOARCH=arm64`. Do not execute cross-architecture binaries.

- [ ] **Step 4: Add tag-triggered release automation**

On tags `v*`, repeat all verification, build with `-trimpath -ldflags="-s -w -X main.pluginVersion=${VERSION}"`, call the packaging script, run its smoke test, and upload all seven release assets to a GitHub Release. Keep artifact names exactly as specified in Step 1.

- [ ] **Step 5: Write operator documentation and registry metadata**

README must cover: Linux-only prerequisites; `.so` installation paths for both architectures; minimal CLIProxyAPI plugin enablement; panel URL; inert first startup; schedule activation; Probe quota warning; manual-only Reset with Reset Credit warning; no page polling; five-minute staleness; Guardrail Hold recovery; persistence files and deletion boundaries; host-owned login; upgrade/rollback; verification commands. State explicitly that the plugin never asks for or saves the CLIProxyAPI management key.

`registry.json` identifies `codex-window-reset`, repository `https://github.com/tapaixx/codex-window-reset`, license MIT, and Linux `amd64`/`arm64`. `LICENSE` is the MIT text for Codex Window Reset contributors; `NOTICE` retains the upstream attribution.

- [ ] **Step 6: Run release validation locally**

Run: `go vet ./...`

Expected: PASS.

Run: `CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -buildmode=c-shared -o /tmp/codex-window-reset-linux-amd64.so .`

Expected: PASS and both `/tmp/codex-window-reset-linux-amd64.so` plus its generated header exist.

Run the arm64 build when `aarch64-linux-gnu-gcc` is installed:

```bash
CC=aarch64-linux-gnu-gcc CGO_ENABLED=1 GOOS=linux GOARCH=arm64 go build -buildmode=c-shared -o /tmp/codex-window-reset-linux-arm64.so .
```

Expected: PASS; otherwise CI remains the authoritative arm64 build gate and the missing local compiler is documented in verification notes.

- [ ] **Step 7: Commit release support**

```bash
git add LICENSE README.md registry.json scripts .github .gitignore
git commit -m "build: package Linux plugin releases"
```

### Task 14: Final Spec Traceability and Clean Verification

**Files:**
- Create: `docs/verification.md`
- Modify: only files needed to correct failures found by the checks below.

**Interfaces:**
- Consumes: the approved design and all completed tasks.
- Produces: an evidence-backed verification record with one acceptance-criterion row per requirement.

- [ ] **Step 1: Create a requirement-to-test matrix**

In `docs/verification.md`, list every Acceptance Criteria bullet from the design verbatim and map it to exact test names and source files. Include separate rows for inert startup, deterministic/no-catch-up scheduling, fail-open/Guardrail Hold, Request versus Window outcomes, reset single-flight/idempotency/audit, deletion boundaries, secret absence, responsive layout, and both architectures.

- [ ] **Step 2: Run format and static checks**

Run: `gofmt -w $(find . -name '*.go' -not -path './.git/*')`

Run: `go vet ./...`

Run: `node --check web/modules/api.js && node --check web/modules/state.js && node --check web/modules/accounts.js && node --check web/modules/schedule.js && node --check web/modules/simulator.js && node --check web/modules/history.js && node --check web/modules/main.js`

Expected: every command exits 0; inspect and commit any mechanical formatting change with the owning task's files.

- [ ] **Step 3: Run the complete clean test suite**

Run: `go test -count=1 ./...`

Run: `go test -count=1 -race ./...`

Run: `node --test web/tests/*.test.mjs`

Expected: all tests pass with caches bypassed for Go.

- [ ] **Step 4: Verify frontend asset and secret invariants**

Run: `rg -n 'access_token|Authorization: Bearer|management[_ -]?key|localStorage|sessionStorage' web internal/management README.md`

Expected: no production browser/management match; README may contain only the explicit statement that the plugin does not store a management key. Any test fixture token must live outside the searched production paths.

Run: `rg -n '/panel/assets/|Resources:.*\*|resource.*\*' . --glob '*.go' --glob '*.js' --glob '*.html'`

Expected: no wildcard asset registration.

- [ ] **Step 5: Build and inspect the amd64 shared library**

Run: `CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -trimpath -buildmode=c-shared -o /tmp/codex-window-reset.so .`

Run: `file /tmp/codex-window-reset.so && nm -D /tmp/codex-window-reset.so | rg 'cliproxy_plugin_init|cliproxyPluginCall|cliproxyPluginFree|cliproxyPluginShutdown'`

Expected: ELF 64-bit x86-64 shared object and all four ABI symbols are exported.

- [ ] **Step 6: Complete verification notes and commit**

Record command, date, outcome, and any local arm64-toolchain limitation in `docs/verification.md`; do not mark the arm64 acceptance row passed until either local or CI evidence exists.

```bash
git add docs/verification.md
git commit -m "docs: record release verification evidence"
```
