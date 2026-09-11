package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/accounts"
	"github.com/tapaixx/codex-window-reset/internal/app"
	"github.com/tapaixx/codex-window-reset/internal/domain"
	"github.com/tapaixx/codex-window-reset/internal/host"
)

const (
	integrationPluginPath    = "/v0/management/plugins/codex-window-reset"
	integrationResourcePath  = "/v0/resource/plugins/codex-window-reset"
	integrationProbeURL      = "https://chatgpt.com/backend-api/codex/responses"
	integrationUsageURL      = "https://chatgpt.com/backend-api/wham/usage"
	integrationCreditsURL    = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
	integrationConsumeURL    = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume"
	integrationToken         = "fake-token-never-persist"
	integrationManagementKey = "management-secret-never-persist"
	integrationRawEmail      = "raw@example.com"
	integrationRawBody       = "raw-upstream-body-never-persist"
	integrationResetKey      = "33333333-3333-4333-8333-333333333333"
)

func TestStructuredLoggerAcceptsOnlySanitizedOutcomeFields(t *testing.T) {
	logger := &fieldBoundaryLogger{}
	logOutcome(logger, "correlation-1", "fingerprint-1", domain.CodeProbeFailed, 17, "upstream_error")
	if logger.invalidField != "" {
		t.Fatalf("structured logger emitted forbidden field %q", logger.invalidField)
	}
}

func TestCorruptConfigLeavesSanitizedManagementDiagnosticsReachable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &integrationHost{files: []host.AuthFile{{AuthIndex: "auth-one", Type: "codex", Email: integrationRawEmail}}}
	rt, err := newRuntime(fake, dir)
	if err != nil {
		t.Fatal(err)
	}
	rt.Start()
	installRuntimeForIntegrationTest(t, rt)

	status := callManagement(t, "GET", integrationPluginPath+"/status", nil, nil)
	if status.StatusCode != http.StatusOK || !strings.Contains(string(status.Body), string(domain.CodeStoreCorrupt)) || strings.Contains(string(status.Body), "{corrupt") {
		t.Fatalf("corrupt status = %d %s", status.StatusCode, status.Body)
	}
	accountsResponse := callManagement(t, "GET", integrationPluginPath+"/accounts", nil, nil)
	if accountsResponse.StatusCode != http.StatusOK || !strings.Contains(string(accountsResponse.Body), "acct-auth-one") {
		t.Fatalf("account diagnostics unavailable after config corruption: %d %s", accountsResponse.StatusCode, accountsResponse.Body)
	}
	refreshResponse := callManagement(t, "POST", integrationPluginPath+"/quota/refresh", map[string]any{
		"account_keys": []string{"acct-auth-one"},
	}, map[string][]string{"Content-Type": {"application/json"}})
	if refreshResponse.StatusCode != http.StatusOK || !strings.Contains(string(refreshResponse.Body), "acct-auth-one") {
		t.Fatalf("manual refresh unavailable after config corruption: %d %s", refreshResponse.StatusCode, refreshResponse.Body)
	}
}

func TestManagementDispatchEndToEndPreservesLifecycleAndSecretBoundaries(t *testing.T) {
	dir := t.TempDir()
	fake := &integrationHost{
		files: []host.AuthFile{{AuthIndex: "auth-one", Type: "codex", Email: integrationRawEmail, PlanLabel: "Plus"}},
		auth: map[string]json.RawMessage{
			"auth-one": json.RawMessage(fmt.Sprintf(`{"access_token":%q,"account_id":"upstream-one","email":%q}`, integrationToken, integrationRawEmail)),
		},
		consumeEntered: make(chan struct{}),
		releaseConsume: make(chan struct{}),
	}
	rt, err := newRuntime(fake, dir)
	if err != nil {
		t.Fatal(err)
	}
	rt.Start()
	installRuntimeForIntegrationTest(t, rt)

	// First startup is deliberately inert and has no activation fields.
	status := callManagement(t, "GET", integrationPluginPath+"/status", nil, nil)
	var statusEnvelope struct {
		OK     bool `json:"ok"`
		Result struct {
			Enabled    bool                 `json:"enabled"`
			StoreError domain.ErrorCode     `json:"store_error_code"`
			NextRuns   map[string]time.Time `json:"next_runs"`
		} `json:"result"`
	}
	decodeBody(t, status.Body, &statusEnvelope)
	if !statusEnvelope.OK || statusEnvelope.Result.Enabled || statusEnvelope.Result.StoreError != "" || len(statusEnvelope.Result.NextRuns) != 0 {
		t.Fatalf("startup was not inert: %s", status.Body)
	}

	accountsResponse := callManagement(t, "GET", integrationPluginPath+"/accounts", nil, nil)
	var accountsEnvelope struct {
		Result []accounts.Account `json:"result"`
	}
	decodeBody(t, accountsResponse.Body, &accountsEnvelope)
	if len(accountsEnvelope.Result) != 1 || accountsEnvelope.Result[0].MaskedIdentity != "r***@example.com" || accountsEnvelope.Result[0].AuthIndex != "" {
		t.Fatalf("unsafe account projection: %s", accountsResponse.Body)
	}

	scheduleResponse := callManagement(t, "GET", integrationPluginPath+"/schedule", nil, nil)
	var scheduleEnvelope struct {
		Result domain.Config `json:"result"`
	}
	decodeBody(t, scheduleResponse.Body, &scheduleEnvelope)
	if scheduleEnvelope.Result.Enabled || scheduleEnvelope.Result.PreheatLeadMinutes != nil || scheduleEnvelope.Result.PreheatSpanMinutes != nil || len(scheduleEnvelope.Result.ScheduledAccountKeys) != 0 {
		t.Fatalf("unsafe schedule defaults: %s", scheduleResponse.Body)
	}

	lead, span := 15, 15
	draft := scheduleEnvelope.Result
	draft.Enabled = true
	draft.PreheatLeadMinutes = &lead
	draft.PreheatSpanMinutes = &span
	draft.ScheduledAccountKeys = []string{"acct-auth-one"}
	updated := callManagement(t, "PUT", integrationPluginPath+"/schedule", draft, map[string][]string{"Content-Type": {"application/json"}})
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("schedule update status = %d: %s", updated.StatusCode, updated.Body)
	}
	var updatedEnvelope struct {
		Result domain.Config `json:"result"`
	}
	decodeBody(t, updated.Body, &updatedEnvelope)
	if !updatedEnvelope.Result.Enabled || updatedEnvelope.Result.Revision != draft.Revision+1 {
		t.Fatalf("schedule was not activated: %s", updated.Body)
	}

	stale := draft
	stale.Enabled = false
	conflict := callManagement(t, "PUT", integrationPluginPath+"/schedule", stale, map[string][]string{"Content-Type": {"application/json"}})
	if conflict.StatusCode != http.StatusConflict || !strings.Contains(string(conflict.Body), string(domain.CodeRevisionConflict)) {
		t.Fatalf("stale schedule was accepted: %d %s", conflict.StatusCode, conflict.Body)
	}

	statusOne := callManagement(t, "GET", integrationPluginPath+"/status", nil, nil)
	statusTwo := callManagement(t, "GET", integrationPluginPath+"/status", nil, nil)
	var firstStatus, secondStatus struct {
		Result struct {
			NextRuns map[string]time.Time `json:"next_runs"`
		} `json:"result"`
	}
	decodeBody(t, statusOne.Body, &firstStatus)
	decodeBody(t, statusTwo.Body, &secondStatus)
	if len(firstStatus.Result.NextRuns) == 0 || !mapsEqual(firstStatus.Result.NextRuns, secondStatus.Result.NextRuns) {
		t.Fatalf("next runs were not deterministic: %s / %s", statusOne.Body, statusTwo.Body)
	}

	probeResponse := callManagement(t, "POST", integrationPluginPath+"/probes", map[string]any{
		"account_keys":             []string{"acct-auth-one"},
		"acknowledge_quota_effect": true,
	}, map[string][]string{"Content-Type": {"application/json"}})
	if probeResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("manual probe status = %d: %s", probeResponse.StatusCode, probeResponse.Body)
	}
	waitForHistory(t, rt, 1)

	refreshResponse := callManagement(t, "POST", integrationPluginPath+"/quota/refresh", map[string]any{
		"account_keys": []string{"acct-auth-one"},
	}, map[string][]string{"Content-Type": {"application/json"}})
	if refreshResponse.StatusCode != http.StatusOK || !strings.Contains(string(refreshResponse.Body), "acct-auth-one") {
		t.Fatalf("quota refresh failed: %d %s", refreshResponse.StatusCode, refreshResponse.Body)
	}

	resetDone := make(chan abiManagementResponse, 1)
	go func() {
		resetDone <- callManagement(t, "POST", integrationPluginPath+"/quota/reset", map[string]any{
			"account_key":     "acct-auth-one",
			"idempotency_key": integrationResetKey,
		}, map[string][]string{"Content-Type": {"application/json"}})
	}()
	select {
	case <-fake.consumeEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("reset did not reach consume")
	}
	pending := callManagement(t, "GET", integrationPluginPath+"/reset-audit", nil, nil)
	if pending.StatusCode != http.StatusOK || !strings.Contains(string(pending.Body), `"outcome":"pending"`) {
		t.Fatalf("pending reset audit was not visible: %d %s", pending.StatusCode, pending.Body)
	}
	close(fake.releaseConsume)
	resetResponse := <-resetDone
	if resetResponse.StatusCode != http.StatusOK || !strings.Contains(string(resetResponse.Body), `"outcome":"succeeded"`) {
		t.Fatalf("final reset response = %d %s", resetResponse.StatusCode, resetResponse.Body)
	}
	resetCalls := fake.consumeCalls()
	replay := callManagement(t, "POST", integrationPluginPath+"/quota/reset", map[string]any{
		"account_key":     "acct-auth-one",
		"idempotency_key": integrationResetKey,
	}, map[string][]string{"Content-Type": {"application/json"}})
	if replay.StatusCode != http.StatusOK || fake.consumeCalls() != resetCalls {
		t.Fatalf("idempotent replay repeated consume: %d %s calls=%d/%d", replay.StatusCode, replay.Body, fake.consumeCalls(), resetCalls)
	}

	clearHistory := callManagement(t, "DELETE", integrationPluginPath+"/history", nil, nil)
	if clearHistory.StatusCode != http.StatusOK {
		t.Fatalf("history clear status = %d: %s", clearHistory.StatusCode, clearHistory.Body)
	}
	history := callManagement(t, "GET", integrationPluginPath+"/history", nil, nil)
	if history.StatusCode != http.StatusOK || strings.Contains(string(history.Body), "health_probe") {
		t.Fatalf("ordinary history was not cleared: %d %s", history.StatusCode, history.Body)
	}
	scheduleAfterHistory := callManagement(t, "GET", integrationPluginPath+"/schedule", nil, nil)
	var scheduleAfterHistoryEnvelope struct {
		Result domain.Config `json:"result"`
	}
	decodeBody(t, scheduleAfterHistory.Body, &scheduleAfterHistoryEnvelope)
	if scheduleAfterHistory.StatusCode != http.StatusOK || !scheduleAfterHistoryEnvelope.Result.Enabled || scheduleAfterHistoryEnvelope.Result.Revision != updatedEnvelope.Result.Revision {
		t.Fatalf("ordinary history clear changed configuration: %d %s", scheduleAfterHistory.StatusCode, scheduleAfterHistory.Body)
	}
	auditAfterHistory := callManagement(t, "GET", integrationPluginPath+"/reset-audit", nil, nil)
	if !strings.Contains(string(auditAfterHistory.Body), integrationResetKey) {
		t.Fatalf("ordinary history clear deleted reset audit: %s", auditAfterHistory.Body)
	}
	authCallsBeforeHistoryProbe := fake.authCalls()
	probeAfterHistory := callManagement(t, "POST", integrationPluginPath+"/probes", map[string]any{
		"account_keys":             []string{"acct-auth-one"},
		"acknowledge_quota_effect": true,
	}, map[string][]string{"Content-Type": {"application/json"}})
	if probeAfterHistory.StatusCode != http.StatusAccepted {
		t.Fatalf("host credential was not usable after history clear: %d %s", probeAfterHistory.StatusCode, probeAfterHistory.Body)
	}
	waitForHistory(t, rt, 1)
	remainingHistory, err := rt.ListHistory()
	if err != nil || len(remainingHistory) != 1 || remainingHistory[0].RequestOutcome != domain.RequestSucceeded || fake.authCalls() <= authCallsBeforeHistoryProbe {
		t.Fatalf("history clear crossed host credential boundary: records=%#v auth_calls=%d/%d err=%v", remainingHistory, fake.authCalls(), authCallsBeforeHistoryProbe, err)
	}

	clearAudit := callManagement(t, "DELETE", integrationPluginPath+"/reset-audit", nil, map[string][]string{"X-Confirmation": {"DELETE AUDIT"}})
	if clearAudit.StatusCode != http.StatusOK {
		t.Fatalf("audit clear status = %d: %s", clearAudit.StatusCode, clearAudit.Body)
	}

	asset := callManagement(t, "GET", integrationResourcePath+"/modules/main.js", nil, nil)
	if asset.StatusCode != http.StatusOK || asset.Headers["Content-Type"][0] != "text/javascript; charset=utf-8" {
		t.Fatalf("embedded asset response = %#v", asset)
	}

	assertIntegrationSecretsAbsent(t, dir, fake, status.Body, accountsResponse.Body, scheduleResponse.Body, updated.Body,
		conflict.Body, probeResponse.Body, refreshResponse.Body, pending.Body, resetResponse.Body, replay.Body, history.Body,
		scheduleAfterHistory.Body, probeAfterHistory.Body,
		auditAfterHistory.Body, clearAudit.Body, asset.Body)
}

func installRuntimeForIntegrationTest(t *testing.T, rt *app.Runtime) {
	t.Helper()
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
}

func callManagement(t *testing.T, method, path string, body any, headers map[string][]string) abiManagementResponse {
	t.Helper()
	payload := map[string]any{"method": method, "path": path}
	requestHeaders := map[string][]string{
		"X-Management-Key": {integrationManagementKey},
	}
	for key, values := range headers {
		requestHeaders[key] = append([]string(nil), values...)
	}
	payload["headers"] = requestHeaders
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload["body"] = base64.StdEncoding.EncodeToString(raw)
	}
	request, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	got, err := dispatch("management.handle", request)
	if err != nil {
		t.Fatal(err)
	}
	response, ok := got.(abiManagementResponse)
	if !ok {
		t.Fatalf("management result type = %T", got)
	}
	return response
}

func decodeBody(t *testing.T, body []byte, destination any) {
	t.Helper()
	if err := json.Unmarshal(body, destination); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}

func mapsEqual(left, right map[string]time.Time) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if !value.Equal(right[key]) {
			return false
		}
	}
	return true
}

func waitForHistory(t *testing.T, rt *app.Runtime, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		history, err := rt.ListHistory()
		if err == nil && len(history) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	history, _ := rt.ListHistory()
	t.Fatalf("history length = %d, want at least %d", len(history), want)
}

func assertIntegrationSecretsAbsent(t *testing.T, dir string, fake *integrationHost, payloads ...[]byte) {
	t.Helper()
	combined := make([]byte, 0)
	for _, payload := range payloads {
		combined = append(combined, payload...)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		combined = append(combined, data...)
	}
	for _, forbidden := range []string{integrationToken, integrationManagementKey, integrationRawEmail, integrationRawBody, "access_token", "Authorization", "Bearer"} {
		if strings.Contains(string(combined), forbidden) {
			t.Fatalf("secret %q crossed a persistence/return boundary: %s", forbidden, combined)
		}
	}
	for _, logEntry := range fake.logsSnapshot() {
		for _, forbidden := range []string{integrationToken, integrationManagementKey, integrationRawEmail, integrationRawBody, "access_token", "Authorization", "Bearer"} {
			if strings.Contains(logEntry, forbidden) {
				t.Fatalf("secret %q crossed the log boundary: %s", forbidden, logEntry)
			}
		}
	}
}

type integrationHost struct {
	mu             sync.Mutex
	files          []host.AuthFile
	auth           map[string]json.RawMessage
	requests       []host.HTTPRequest
	logs           []string
	consumeEntered chan struct{}
	releaseConsume chan struct{}
	consumeCount   int
	authCount      int
	consumeOnce    sync.Once
}

type fieldBoundaryLogger struct {
	invalidField string
}

func (*fieldBoundaryLogger) ListAuthFiles(context.Context) ([]host.AuthFile, error) { return nil, nil }
func (*fieldBoundaryLogger) GetAuth(context.Context, string) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}
func (*fieldBoundaryLogger) HTTPDo(context.Context, host.HTTPRequest) (host.HTTPResponse, error) {
	return host.HTTPResponse{}, errors.New("not implemented")
}
func (l *fieldBoundaryLogger) Log(_ context.Context, _ string, _ string, fields map[string]any) {
	for key := range fields {
		if strings.Contains(strings.ToLower(key), "token") ||
			strings.Contains(strings.ToLower(key), "auth") ||
			strings.Contains(strings.ToLower(key), "email") ||
			strings.Contains(strings.ToLower(key), "body") ||
			strings.Contains(strings.ToLower(key), "header") ||
			strings.Contains(strings.ToLower(key), "key") ||
			strings.Contains(strings.ToLower(key), "credential") {
			l.invalidField = key
		}
	}
}

func (h *integrationHost) ListAuthFiles(context.Context) ([]host.AuthFile, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]host.AuthFile(nil), h.files...), nil
}

func (h *integrationHost) GetAuth(_ context.Context, authIndex string) (json.RawMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.authCount++
	raw, ok := h.auth[authIndex]
	if !ok {
		return nil, errors.New("auth fixture unavailable")
	}
	return append(json.RawMessage(nil), raw...), nil
}

func (h *integrationHost) HTTPDo(ctx context.Context, request host.HTTPRequest) (host.HTTPResponse, error) {
	h.mu.Lock()
	h.requests = append(h.requests, cloneIntegrationRequest(request))
	h.mu.Unlock()
	switch request.URL {
	case integrationProbeURL:
		return host.HTTPResponse{StatusCode: http.StatusOK, Body: []byte(": " + integrationRawBody + "\n\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"O\"}\n\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"K\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n" +
			"data: [DONE]\n\n")}, nil
	case integrationUsageURL:
		return host.HTTPResponse{StatusCode: http.StatusOK, Body: integrationUsageFixture()}, nil
	case integrationCreditsURL:
		return host.HTTPResponse{StatusCode: http.StatusOK, Body: []byte(`{"available_count":2,"applicable_available_count":2,"credits":[{"id":"credit","reset_type":"codex_rate_limits","status":"available","expires_at":"2026-09-16T00:00:00Z"}]}`)}, nil
	case integrationConsumeURL:
		h.consumeOnce.Do(func() {
			if h.consumeEntered != nil {
				close(h.consumeEntered)
			}
		})
		if h.releaseConsume != nil {
			select {
			case <-h.releaseConsume:
			case <-ctx.Done():
				return host.HTTPResponse{}, ctx.Err()
			}
		}
		h.mu.Lock()
		h.consumeCount++
		h.mu.Unlock()
		return host.HTTPResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true}`)}, nil
	default:
		return host.HTTPResponse{StatusCode: http.StatusNotFound, Body: []byte(integrationRawBody)}, nil
	}
}

func (h *integrationHost) Log(_ context.Context, level, message string, fields map[string]any) {
	data, _ := json.Marshal(map[string]any{"level": level, "message": message, "fields": fields})
	h.mu.Lock()
	h.logs = append(h.logs, string(data))
	h.mu.Unlock()
}

func (h *integrationHost) consumeCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.consumeCount
}

func (h *integrationHost) authCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.authCount
}

func (h *integrationHost) logsSnapshot() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.logs...)
}

func cloneIntegrationRequest(request host.HTTPRequest) host.HTTPRequest {
	request.Body = append([]byte(nil), request.Body...)
	if request.Headers != nil {
		copyHeaders := make(map[string][]string, len(request.Headers))
		for key, values := range request.Headers {
			copyHeaders[key] = append([]string(nil), values...)
		}
		request.Headers = copyHeaders
	}
	return request
}

func integrationUsageFixture() []byte {
	now := time.Now().UTC()
	body, _ := json.Marshal(map[string]any{
		"rate_limit": map[string]any{
			"primary_window": map[string]any{
				"limit_window_seconds": 18000,
				"used_percent":         1,
				"reset_at":             now.Add(2 * time.Hour).Unix(),
			},
			"secondary_window": map[string]any{
				"limit_window_seconds": 604800,
				"used_percent":         1,
				"reset_at":             now.Add(7 * 24 * time.Hour).Unix(),
			},
		},
		"rate_limit_reset_credits": map[string]any{"available": 2},
	})
	return body
}
