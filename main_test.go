package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/accounts"
	"github.com/tapaixx/codex-window-reset/internal/app"
	"github.com/tapaixx/codex-window-reset/internal/domain"
	"github.com/tapaixx/codex-window-reset/internal/host"
)

func TestDispatchRegistersManagementAndDynamicResources(t *testing.T) {
	installTestRuntime(t)
	got, err := dispatch("management.register", []byte(`{"resource_base_path":"/v0/resource/plugins/codex-window-reset-linux-amd64"}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte("/plugins/codex-window-reset-linux-amd64/status")) || !bytes.Contains(encoded, []byte("/modules/main.js")) {
		t.Fatalf("%s", encoded)
	}
}

func TestShutdownStopsSchedulerAndClearsRuntime(t *testing.T) {
	rt := installTestRuntime(t)
	cliproxyPluginShutdown()
	if currentRuntime() != nil || !rt.Stopped() {
		t.Fatal("runtime still active")
	}
}

func TestDispatchRejectsMissingRuntimeAndUnsupportedMethod(t *testing.T) {
	clearTestRuntime()
	if _, err := dispatch("management.handle", []byte(`{"method":"GET","path":"/status"}`)); err == nil {
		t.Fatal("management dispatch succeeded without a runtime")
	}
	if _, err := dispatch("not-supported", []byte(`{}`)); err == nil {
		t.Fatal("unsupported dispatch method succeeded")
	}
}

func TestABIValidationRejectsMissingOrWrongHostCapabilities(t *testing.T) {
	cases := []struct {
		name       string
		host       uint32
		plugin     uint32
		hostCall   bool
		hostFree   bool
		wantAccept bool
	}{
		{name: "valid zero output version", host: abiVersion, plugin: 0, hostCall: true, hostFree: true, wantAccept: true},
		{name: "valid explicit output version", host: abiVersion, plugin: abiVersion, hostCall: true, hostFree: true, wantAccept: true},
		{name: "wrong host version", host: abiVersion + 1, plugin: 0, hostCall: true, hostFree: true},
		{name: "wrong plugin version", host: abiVersion, plugin: abiVersion + 1, hostCall: true, hostFree: true},
		{name: "missing host call", host: abiVersion, plugin: 0, hostCall: false, hostFree: true},
		{name: "missing host free", host: abiVersion, plugin: 0, hostCall: true, hostFree: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validABI(tc.host, tc.plugin, tc.hostCall, tc.hostFree); got != tc.wantAccept {
				t.Fatalf("validABI() = %t, want %t", got, tc.wantAccept)
			}
		})
	}
}

func TestExportedInitRejectsNilAndWrongABI(t *testing.T) {
	clearTestRuntime()
	tests := []struct {
		name       string
		host       uint32
		plugin     uint32
		withCall   int
		withFree   int
		wantStatus int
	}{
		{name: "nil host", wantStatus: 1},
		{name: "nil plugin", host: abiVersion, wantStatus: 1},
		{name: "wrong host version", host: abiVersion + 1, withCall: 1, withFree: 1, wantStatus: 1},
		{name: "wrong plugin version", host: abiVersion, plugin: abiVersion + 1, withCall: 1, withFree: 1, wantStatus: 1},
		{name: "missing host call", host: abiVersion, withFree: 1, wantStatus: 1},
		{name: "missing host free", host: abiVersion, withCall: 1, wantStatus: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := initPluginForTest(test.name != "nil host", test.name != "nil plugin", test.host, test.plugin, test.withCall != 0, test.withFree != 0)
			if got != test.wantStatus {
				t.Fatalf("init status = %d, want %d", got, test.wantStatus)
			}
			if currentRuntime() != nil {
				t.Fatal("rejected init installed a runtime")
			}
		})
	}
}

func TestExportedCallAllocatesAndFreesPluginBuffer(t *testing.T) {
	status, raw := callPluginForTest("management.register", []byte(`{"resource_base_path":"/v0/resource/plugins/codex-window-reset"}`))
	if status != 0 {
		t.Fatalf("plugin call status = %d", status)
	}
	if len(raw) == 0 {
		t.Fatal("plugin call returned an empty C buffer")
	}
	if !bytes.Contains(raw, []byte(`/plugins/codex-window-reset/status`)) {
		t.Fatalf("registration response = %s", raw)
	}
}

func TestHostCallerCopiesResponseAndAlwaysReleasesHostBuffer(t *testing.T) {
	clearTestRuntime()
	t.Chdir(t.TempDir())
	if got := initResponseHostForTest(); got != 0 {
		t.Fatalf("init status = %d", got)
	}
	resetHostBoundaryCountsForTest()
	var response host.AuthListResponse
	if err := hostCaller(context.Background(), "host.auth.list", map[string]any{}, &response); err != nil {
		t.Fatal(err)
	}
	calls, frees := hostBoundaryCountsForTest()
	if calls != 1 || frees != 1 {
		t.Fatalf("host boundary calls=%d frees=%d, want one of each", calls, frees)
	}
	if response.Files == nil {
		t.Fatal("host response was not decoded after the C buffer copy")
	}
	shutdownPluginForTest()
}

func TestExportedInitStopsPreviousRuntime(t *testing.T) {
	clearTestRuntime()
	t.Chdir(t.TempDir())
	if got := initPluginForTest(true, true, abiVersion, 0, true, true); got != 0 {
		t.Fatalf("first init status = %d", got)
	}
	first := currentRuntime()
	if first == nil {
		t.Fatal("first init did not install a runtime")
	}
	if got := initPluginForTest(true, true, abiVersion, 0, true, true); got != 0 {
		t.Fatalf("second init status = %d", got)
	}
	second := currentRuntime()
	if second == nil || second == first {
		t.Fatal("second init did not replace the runtime")
	}
	if !first.Stopped() {
		t.Fatal("first runtime was not stopped before replacement")
	}
	shutdownPluginForTest()
	if currentRuntime() != nil || !second.Stopped() {
		t.Fatal("shutdown did not stop and clear the second runtime")
	}
}

func TestDispatchAcceptsAllResourceBasePathFieldCasing(t *testing.T) {
	for name, request := range map[string]string{
		"snake":  `{"resource_base_path":"/v0/resource/plugins/codex-window-reset-linux-arm64"}`,
		"camel":  `{"resourceBasePath":"/v0/resource/plugins/codex-window-reset-linux-arm64"}`,
		"pascal": `{"ResourceBasePath":"/v0/resource/plugins/codex-window-reset-linux-arm64"}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := dispatch("management.register", []byte(request))
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(encoded, []byte("/plugins/codex-window-reset-linux-arm64/status")) {
				t.Fatalf("registration omitted dynamic plugin path: %s", encoded)
			}
		})
	}
}

func TestDispatchManagementHandleReturnsRouterResponse(t *testing.T) {
	installTestRuntime(t)
	got, err := dispatch("management.handle", []byte(`{"method":"GET","path":"/v0/management/plugins/codex-window-reset/status"}`))
	if err != nil {
		t.Fatal(err)
	}
	response, ok := got.(abiManagementResponse)
	if !ok {
		t.Fatalf("response type = %T", got)
	}
	if response.StatusCode != 200 || !bytes.Contains(response.Body, []byte(`"enabled":false`)) {
		t.Fatalf("unexpected management response: %#v", response)
	}
}

func TestPluginLifecycleStartRecoversPendingAuditsOnlyOnce(t *testing.T) {
	audit := &countingAudit{}
	deps := testDependencies()
	deps.Audit = audit
	rt, err := app.New(deps)
	if err != nil {
		t.Fatal(err)
	}
	runtimeSlot.Lock()
	if runtimeSlot.runtime != nil {
		runtimeSlot.runtime.Stop()
	}
	runtimeSlot.runtime = rt
	runtimeSlot.Unlock()
	t.Cleanup(func() {
		runtimeSlot.Lock()
		if runtimeSlot.runtime == rt {
			rt.Stop()
			runtimeSlot.runtime = nil
		}
		runtimeSlot.Unlock()
	})

	if _, err := dispatch("plugin.register", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := dispatch("plugin.reconfigure", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if got := audit.recoverCount(); got != 1 {
		t.Fatalf("pending-audit recovery ran %d times, want once", got)
	}
}

func TestRepeatedRuntimeInstallationStopsPreviousRuntime(t *testing.T) {
	first := installTestRuntime(t)
	second := installTestRuntime(t)
	if !first.Stopped() || currentRuntime() != second {
		t.Fatal("repeated runtime installation left the old runtime active")
	}
	cliproxyPluginShutdown()
}

func installTestRuntime(t *testing.T) *app.Runtime {
	t.Helper()
	deps := testDependencies()
	rt, err := app.New(deps)
	if err != nil {
		t.Fatal(err)
	}
	runtimeSlot.Lock()
	if runtimeSlot.runtime != nil {
		runtimeSlot.runtime.Stop()
	}
	runtimeSlot.runtime = rt
	runtimeSlot.Unlock()
	t.Cleanup(func() {
		runtimeSlot.Lock()
		if runtimeSlot.runtime == rt {
			rt.Stop()
			runtimeSlot.runtime = nil
		}
		runtimeSlot.Unlock()
	})
	return rt
}

func clearTestRuntime() {
	runtimeSlot.Lock()
	if runtimeSlot.runtime != nil {
		runtimeSlot.runtime.Stop()
		runtimeSlot.runtime = nil
	}
	runtimeSlot.Unlock()
}

func testDependencies() app.Dependencies {
	return app.Dependencies{
		Accounts: &testAccounts{items: []accounts.Account{{Key: "acct-test", AuthIndex: "idx-test", MaskedIdentity: "t***@example.com"}}},
		Probe:    testProbe{},
		Quota:    &testQuota{},
		Config:   &testConfig{config: domain.DefaultConfig()},
		History:  &testHistory{},
		State:    &testState{},
		Audit:    &testAudit{},
		Clock:    testClock{now: time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)},
		IDs:      func() string { return "test-id" },
	}
}

type testClock struct{ now time.Time }

func (c testClock) Now() time.Time                             { return c.now }
func (testClock) AfterFunc(time.Duration, func()) domain.Timer { return testTimer{} }

type testTimer struct{}

func (testTimer) Stop() bool { return true }

type testAccounts struct {
	mu    sync.Mutex
	items []accounts.Account
}

func (s *testAccounts) List(context.Context) ([]accounts.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]accounts.Account(nil), s.items...), nil
}

func (s *testAccounts) Find(_ context.Context, key string) (accounts.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.items {
		if item.Key == key {
			return item, nil
		}
	}
	return accounts.Account{}, errors.New("not found")
}

type testProbe struct{}

func (testProbe) Execute(context.Context, accounts.Account, string, time.Duration) domain.ProbeResult {
	return domain.ProbeResult{Outcome: domain.RequestSucceeded}
}

type testQuota struct{}

func (*testQuota) Refresh(context.Context, accounts.Account) (domain.UsageSnapshot, error) {
	return domain.UsageSnapshot{}, nil
}
func (*testQuota) Get(string, time.Time) (domain.SnapshotView, bool) {
	return domain.SnapshotView{}, false
}
func (*testQuota) Reset(context.Context, accounts.Account, string) (domain.ResetHTTPResult, error) {
	return domain.ResetHTTPResult{}, nil
}

type testConfig struct {
	mu     sync.Mutex
	config domain.Config
}

func (s *testConfig) Load() (domain.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config, nil
}
func (s *testConfig) Save(config domain.Config) error {
	s.mu.Lock()
	s.config = config
	s.mu.Unlock()
	return nil
}

type testHistory struct{}

func (*testHistory) Load() ([]domain.OperationRecord, error) { return []domain.OperationRecord{}, nil }
func (*testHistory) Append(domain.OperationRecord) error     { return nil }
func (*testHistory) Clear() error                            { return nil }

type testState struct {
	mu    sync.Mutex
	state domain.RuntimeState
}

func (s *testState) Load() (domain.RuntimeState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneTestState(s.state), nil
}
func (s *testState) Save(state domain.RuntimeState) error {
	s.mu.Lock()
	s.state = cloneTestState(state)
	s.mu.Unlock()
	return nil
}
func (s *testState) Update(mutate func(*domain.RuntimeState) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := cloneTestState(s.state)
	if err := mutate(&state); err != nil {
		return err
	}
	s.state = state
	return nil
}

func cloneTestState(state domain.RuntimeState) domain.RuntimeState {
	if state.Occurrences == nil {
		state.Occurrences = map[string]domain.OccurrenceState{}
	}
	if state.NextRuns == nil {
		state.NextRuns = map[string]time.Time{}
	}
	if state.GuardrailHolds == nil {
		state.GuardrailHolds = map[string]domain.GuardrailHold{}
	}
	return state
}

type testAudit struct{}

func (*testAudit) Load() ([]domain.ResetAudit, error) { return []domain.ResetAudit{}, nil }
func (*testAudit) FindByKey(string) (domain.ResetAudit, bool, error) {
	return domain.ResetAudit{}, false, nil
}
func (*testAudit) AppendPending(domain.ResetAudit) error { return nil }
func (*testAudit) Replace(domain.ResetAudit) error       { return nil }
func (*testAudit) DeleteBefore(time.Time) error          { return nil }
func (*testAudit) RecoverPending(time.Time) error        { return nil }
func (*testAudit) Clear() error                          { return nil }

type countingAudit struct {
	testAudit
	mu      sync.Mutex
	recover int
}

func (a *countingAudit) RecoverPending(time.Time) error {
	a.mu.Lock()
	a.recover++
	a.mu.Unlock()
	return nil
}

func (a *countingAudit) recoverCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.recover
}
