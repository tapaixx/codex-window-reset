package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/accounts"
	"github.com/tapaixx/codex-window-reset/internal/app"
	"github.com/tapaixx/codex-window-reset/internal/domain"
	"github.com/tapaixx/codex-window-reset/internal/host"
	"github.com/tapaixx/codex-window-reset/internal/simulate"
	"github.com/tapaixx/codex-window-reset/internal/testabi"
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
	if !bytes.Contains(encoded, []byte("/plugins/codex-window-reset-linux-amd64/status")) || !bytes.Contains(encoded, []byte(`"Path":"/panel"`)) || bytes.Contains(encoded, []byte("/modules/main.js")) {
		t.Fatalf("%s", encoded)
	}
}

func TestEmbeddedPanelShowsTheBuildVersion(t *testing.T) {
	prior := pluginVersion
	t.Cleanup(func() { pluginVersion = prior })
	for _, version := range []string{"0.0.9", "2.3.4-rc.1"} {
		pluginVersion = version
		panel, _, err := embeddedManagementAssets().Read("/panel")
		if err != nil {
			t.Fatal(err)
		}
		want := []byte(`<span class="version">v` + version + `</span>`)
		if !bytes.Contains(panel, want) {
			t.Fatalf("served panel does not display build version %s", version)
		}
	}
}

func TestResolveDataDirUsesASiblingOfThePluginInstallDirectory(t *testing.T) {
	root := t.TempDir()
	dataDir := resolveDataDir(root)
	legacyDir := filepath.Join(root, "codex-window-reset")
	if dataDir == legacyDir {
		t.Fatalf("data dir must not be the plugin's own install directory: %s", dataDir)
	}
	if dataDir != legacyDir+dataDirSuffix {
		t.Fatalf("data dir = %s, want %s", dataDir, legacyDir+dataDirSuffix)
	}
}

func TestResolveDataDirMigratesFilesLeftByAnOlderInstall(t *testing.T) {
	root := t.TempDir()
	legacyDir := filepath.Join(root, "codex-window-reset")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range legacyDataFiles {
		if err := os.WriteFile(filepath.Join(legacyDir, name), []byte(`{"marker":"`+name+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A plugin-store reinstall of a prior release recreates the legacy
	// directory alongside the .so it just extracted; that unrelated file
	// must survive the migration.
	if err := os.WriteFile(filepath.Join(legacyDir, "codex-window-reset.so"), []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}

	dataDir := resolveDataDir(root)
	for _, name := range legacyDataFiles {
		migrated, err := os.ReadFile(filepath.Join(dataDir, name))
		if err != nil {
			t.Fatalf("%s did not migrate: %v", name, err)
		}
		if string(migrated) != `{"marker":"`+name+`"}` {
			t.Fatalf("%s migrated with wrong content: %s", name, migrated)
		}
		if _, err := os.Stat(filepath.Join(legacyDir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s was not removed from the legacy directory: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(legacyDir, "codex-window-reset.so")); err != nil {
		t.Fatalf("unrelated plugin file was disturbed by migration: %v", err)
	}
}

func TestResolveDataDirMigrationIsIdempotentAndNeverOverwritesNewData(t *testing.T) {
	root := t.TempDir()
	legacyDir := filepath.Join(root, "codex-window-reset")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "config.json"), []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}

	first := resolveDataDir(root)
	if got, err := os.ReadFile(filepath.Join(first, "config.json")); err != nil || string(got) != "legacy" {
		t.Fatalf("first migration = %q, %v", got, err)
	}

	// Simulate the Operator saving new configuration after the migration.
	if err := os.WriteFile(filepath.Join(first, "config.json"), []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A later reinstall recreates the legacy directory with a stale copy of
	// old data next to the freshly extracted binary; it must never win.
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "config.json"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	second := resolveDataDir(root)
	if second != first {
		t.Fatalf("data dir changed across calls: %s vs %s", first, second)
	}
	if got, err := os.ReadFile(filepath.Join(second, "config.json")); err != nil || string(got) != "current" {
		t.Fatalf("current data was overwritten by a later reinstall: %q, %v", got, err)
	}
}

// Optional fixture export lets browser acceptance use the actual embedded Go
// response, including the linker-injected version and single-page bundling.
func TestExportEmbeddedPanelForBrowser(t *testing.T) {
	directory := os.Getenv("CWR_BROWSER_FIXTURE_DIR")
	if directory == "" {
		t.Skip("browser fixture export was not requested")
	}
	panel, _, err := embeddedManagementAssets().Read("/panel")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "panel.html"), panel, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := domain.DefaultConfig()
	// A non-cycle-aligned work end catches the missing 16:30 renewal; the
	// previous 19:00 fixture only exercised equality at the nominal anchor.
	cfg.WorkPeriods[len(cfg.WorkPeriods)-1].End = "18:00"
	lead, span := 120, 60
	cfg.PreheatLeadMinutes, cfg.PreheatSpanMinutes = &lead, &span
	cfg.ScheduledAccountKeys = []string{"example"}
	result, err := (simulate.Service{}).Run(cfg, time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "simulation.json"), data, 0o600); err != nil {
		t.Fatal(err)
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
			got := testabi.Init(test.name != "nil host", test.name != "nil plugin", test.host, test.plugin, test.withCall != 0, test.withFree != 0)
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
	status, raw := testabi.Call("management.register", []byte(`{"resource_base_path":"/v0/resource/plugins/codex-window-reset"}`))
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
	if got := testabi.InitResponseHost(); got != 0 {
		t.Fatalf("init status = %d", got)
	}
	testabi.ResetHostBoundaryCounts()
	var response host.AuthListResponse
	if err := hostCaller(context.Background(), "host.auth.list", map[string]any{}, &response); err != nil {
		t.Fatal(err)
	}
	calls, frees := testabi.HostBoundaryCounts()
	if calls != 1 || frees != 1 {
		t.Fatalf("host boundary calls=%d frees=%d, want one of each", calls, frees)
	}
	if response.Files == nil {
		t.Fatal("host response was not decoded after the C buffer copy")
	}
	testabi.Shutdown()
}

func TestExportedInitStopsPreviousRuntime(t *testing.T) {
	clearTestRuntime()
	t.Chdir(t.TempDir())
	if got := testabi.Init(true, true, abiVersion, 0, true, true); got != 0 {
		t.Fatalf("first init status = %d", got)
	}
	first := currentRuntime()
	if first == nil {
		t.Fatal("first init did not install a runtime")
	}
	if got := testabi.Init(true, true, abiVersion, 0, true, true); got != 0 {
		t.Fatalf("second init status = %d", got)
	}
	second := currentRuntime()
	if second == nil || second == first {
		t.Fatal("second init did not replace the runtime")
	}
	if !first.Stopped() {
		t.Fatal("first runtime was not stopped before replacement")
	}
	testabi.Shutdown()
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

func TestManagementTransportFailureReturnsRetryableCorrelatedEnvelope(t *testing.T) {
	clearTestRuntime()
	status, raw := testabi.Call("management.handle", []byte(`{"method":"GET","path":"/v0/management/plugins/codex-window-reset/status"}`))
	if status == 0 {
		t.Fatal("management transport failure unexpectedly succeeded")
	}
	var envelope struct {
		OK    bool `json:"ok"`
		Error *struct {
			Code          string `json:"code"`
			Retryable     bool   `json:"retryable"`
			CorrelationID string `json:"correlation_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("transport failure body = %s: %v", raw, err)
	}
	if envelope.OK || envelope.Error == nil || envelope.Error.Code != "plugin_error" || !envelope.Error.Retryable || envelope.Error.CorrelationID == "" {
		t.Fatalf("unstable transport failure envelope: %s", raw)
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
