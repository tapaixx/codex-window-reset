package management

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/accounts"
	"github.com/tapaixx/codex-window-reset/internal/domain"
)

func TestRegistrationDeclaresEveryExactRouteAndAsset(t *testing.T) {
	got := Registration("codex-window-reset-linux-amd64")
	assertRouteSet(t, got.Routes, map[string]string{
		"GET /plugins/codex-window-reset-linux-amd64/status":         "",
		"GET /plugins/codex-window-reset-linux-amd64/accounts":       "",
		"GET /plugins/codex-window-reset-linux-amd64/schedule":       "",
		"PUT /plugins/codex-window-reset-linux-amd64/schedule":       "",
		"POST /plugins/codex-window-reset-linux-amd64/simulate":      "",
		"POST /plugins/codex-window-reset-linux-amd64/probes":        "",
		"GET /plugins/codex-window-reset-linux-amd64/history":        "",
		"DELETE /plugins/codex-window-reset-linux-amd64/history":     "",
		"GET /plugins/codex-window-reset-linux-amd64/quota":          "",
		"POST /plugins/codex-window-reset-linux-amd64/quota/refresh": "",
		"POST /plugins/codex-window-reset-linux-amd64/quota/reset":   "",
		"GET /plugins/codex-window-reset-linux-amd64/reset-audit":    "",
		"DELETE /plugins/codex-window-reset-linux-amd64/reset-audit": "",
	})
	assertResourcePaths(t, got.Resources, []string{
		"/panel", "/styles.css", "/modules/api.js", "/modules/state.js",
		"/modules/accounts.js", "/modules/schedule.js", "/modules/simulator.js",
		"/modules/history.js", "/modules/main.js", "/modules/dashboard.js",
	})
	if got.Resources[0].Menu == "" {
		t.Fatal("panel must be the menu resource")
	}
	for _, asset := range got.Resources[1:] {
		if asset.Menu != "" {
			t.Fatalf("asset leaked into menu: %#v", asset)
		}
	}
}

func TestRouterStatusUsesSuccessEnvelope(t *testing.T) {
	router := NewRouter(testRuntime{status: domain.StatusView{Enabled: false}}, Assets{})
	response := router.Handle(Request{
		Method: "GET",
		Path:   "/v0/management/plugins/codex-window-reset-linux-amd64/status",
	})
	if response.Status != 200 {
		t.Fatalf("status = %d, want 200: %s", response.Status, response.Body)
	}
	if got := response.Headers["Content-Type"]; got != "application/json; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
	if got := string(response.Body); got != `{"ok":true,"result":{"enabled":false}}` {
		t.Fatalf("body = %s", got)
	}
}

func TestRouterRejectsUndeclaredAssetWithErrorEnvelope(t *testing.T) {
	router := NewRouter(testRuntime{}, Assets{})
	response := router.Handle(Request{
		Method: "GET",
		Path:   "/v0/resource/plugins/codex-window-reset-linux-amd64/modules/missing.js",
	})
	assertErrorResponse(t, response, 404)
}

func TestRouterErrorsHaveStableEnvelope(t *testing.T) {
	router := NewRouter(testRuntime{}, Assets{})
	cases := []Request{
		{Method: "POST", Path: "/v0/management/plugins/codex-window-reset-linux-amd64/status"},
		{Method: "PUT", Path: "/v0/management/plugins/codex-window-reset-linux-amd64/schedule", Headers: map[string]string{"Content-Type": "application/json"}, Body: []byte("{")},
	}
	for _, request := range cases {
		response := router.Handle(request)
		assertErrorResponse(t, response, 0)
	}
}

func assertErrorResponse(t *testing.T, response Response, wantStatus int) {
	t.Helper()
	if wantStatus != 0 && response.Status != wantStatus {
		t.Fatalf("status = %d, want %d: %s", response.Status, wantStatus, response.Body)
	}
	if response.Status < 400 {
		t.Fatalf("status = %d, want error: %s", response.Status, response.Body)
	}
	if got := response.Headers["Content-Type"]; got != "application/json; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
	var envelope struct {
		OK    bool `json:"ok"`
		Error struct {
			Code          string `json:"code"`
			Message       string `json:"message"`
			Retryable     *bool  `json:"retryable"`
			CorrelationID string `json:"correlation_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body, &envelope); err != nil {
		t.Fatalf("decode error envelope: %v; body=%s", err, response.Body)
	}
	if envelope.OK || envelope.Error.Code == "" || envelope.Error.Message == "" || envelope.Error.Retryable == nil || envelope.Error.CorrelationID == "" {
		t.Fatalf("incomplete error envelope: %#v", envelope)
	}
}

func assertRouteSet(t *testing.T, got []Route, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("route count = %d, want %d", len(got), len(want))
	}
	for _, route := range got {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; !ok {
			t.Fatalf("unexpected route %q", key)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("missing routes: %#v", want)
	}
}

func assertResourcePaths(t *testing.T, got []Resource, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("resource count = %d, want %d", len(got), len(want))
	}
	for index, path := range want {
		if got[index].Path != path {
			t.Fatalf("resource[%d].Path = %q, want %q", index, got[index].Path, path)
		}
	}
}

type testRuntime struct {
	status domain.StatusView
}

func (r testRuntime) Status() domain.StatusView { return r.status }

func (testRuntime) ListAccounts(context.Context) ([]accounts.Account, error) { return nil, nil }
func (testRuntime) Schedule() domain.Config                                  { return domain.DefaultConfig() }
func (testRuntime) UpdateSchedule(context.Context, domain.Config) (domain.Config, error) {
	return domain.Config{}, nil
}
func (testRuntime) Simulate(context.Context, domain.Config) (domain.SimulationResult, error) {
	return domain.SimulationResult{}, nil
}
func (testRuntime) StartManualProbes(context.Context, []string, bool) (string, error) {
	return "", nil
}
func (testRuntime) ListHistory() ([]domain.OperationRecord, error)           { return nil, nil }
func (testRuntime) ClearHistory() error                                      { return nil }
func (testRuntime) ListQuota(context.Context) ([]domain.SnapshotView, error) { return nil, nil }
func (testRuntime) RefreshQuotas(context.Context, []string) ([]domain.SnapshotView, error) {
	return nil, nil
}
func (testRuntime) ResetQuota(context.Context, string, string) (domain.ResetAudit, error) {
	return domain.ResetAudit{}, nil
}
func (testRuntime) ListResetAudit() ([]domain.ResetAudit, error) { return nil, nil }
func (testRuntime) ClearResetAudit(string) error                 { return nil }

var _ = time.Time{}
