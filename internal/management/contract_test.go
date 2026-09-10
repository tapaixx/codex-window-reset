package management

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/accounts"
	"github.com/tapaixx/codex-window-reset/internal/domain"
)

const contractPluginPath = "/v0/management/plugins/codex-window-reset-linux-arm64"

func TestManagementRoutesReturnExpectedSuccessStatus(t *testing.T) {
	router := NewRouter(contractRuntimeFixture(), contractAssets(t))
	validConfig := domain.DefaultConfig()
	validResetKey := "11111111-1111-4111-8111-111111111111"
	cases := []struct {
		name   string
		method string
		path   string
		body   string
		head   map[string]string
		want   int
	}{
		{name: "status", method: "GET", path: "/status", want: 200},
		{name: "accounts", method: "GET", path: "/accounts", want: 200},
		{name: "get schedule", method: "GET", path: "/schedule", want: 200},
		{name: "put schedule", method: "PUT", path: "/schedule", body: mustJSON(t, validConfig), head: jsonHeaders(), want: 200},
		{name: "simulate", method: "POST", path: "/simulate", body: mustJSON(t, validConfig), head: jsonHeaders(), want: 200},
		{name: "probes", method: "POST", path: "/probes", body: `{"account_keys":["acct-a"],"acknowledge_quota_effect":true}`, head: jsonHeaders(), want: 202},
		{name: "history", method: "GET", path: "/history", want: 200},
		{name: "clear history", method: "DELETE", path: "/history", want: 200},
		{name: "quota", method: "GET", path: "/quota", want: 200},
		{name: "quota refresh", method: "POST", path: "/quota/refresh", body: `{"account_keys":["acct-a"]}`, head: jsonHeaders(), want: 200},
		{name: "quota reset", method: "POST", path: "/quota/reset", body: `{"account_key":"acct-a","idempotency_key":"` + validResetKey + `"}`, head: jsonHeaders(), want: 200},
		{name: "audit", method: "GET", path: "/reset-audit", want: 200},
		{name: "clear audit", method: "DELETE", path: "/reset-audit", head: map[string]string{"X-Confirmation": "DELETE AUDIT"}, want: 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := Request{Method: tc.method, Path: contractPluginPath + tc.path, Headers: tc.head, Body: []byte(tc.body)}
			response := router.Handle(request)
			if response.Status != tc.want {
				t.Fatalf("status = %d, want %d: %s", response.Status, tc.want, response.Body)
			}
			if got := response.Headers["Content-Type"]; got != "application/json; charset=utf-8" {
				t.Fatalf("content type = %q", got)
			}
		})
	}
}

func TestManagementRoutesRejectWrongMethods(t *testing.T) {
	router := NewRouter(contractRuntimeFixture(), Assets{})
	cases := []struct {
		method string
		path   string
	}{
		{method: "POST", path: "/status"},
		{method: "POST", path: "/accounts"},
		{method: "POST", path: "/schedule"},
		{method: "GET", path: "/simulate"},
		{method: "GET", path: "/probes"},
		{method: "POST", path: "/history"},
		{method: "POST", path: "/quota"},
		{method: "GET", path: "/quota/refresh"},
		{method: "GET", path: "/quota/reset"},
		{method: "POST", path: "/reset-audit"},
	}
	for _, tc := range cases {
		response := router.Handle(Request{Method: tc.method, Path: contractPluginPath + tc.path})
		assertErrorResponse(t, response, 405)
	}
}

func TestManagementBodyRoutesRejectMalformedAndWrongContentType(t *testing.T) {
	router := NewRouter(contractRuntimeFixture(), Assets{})
	paths := []struct {
		method string
		path   string
	}{
		{method: "PUT", path: "/schedule"},
		{method: "POST", path: "/simulate"},
		{method: "POST", path: "/probes"},
		{method: "POST", path: "/quota/refresh"},
		{method: "POST", path: "/quota/reset"},
	}
	for _, tc := range paths {
		t.Run(tc.method+tc.path+" malformed", func(t *testing.T) {
			response := router.Handle(Request{
				Method:  tc.method,
				Path:    contractPluginPath + tc.path,
				Headers: jsonHeaders(),
				Body:    []byte("{"),
			})
			assertErrorResponse(t, response, 400)
		})
		t.Run(tc.method+tc.path+" content type", func(t *testing.T) {
			response := router.Handle(Request{
				Method:  tc.method,
				Path:    contractPluginPath + tc.path,
				Headers: map[string]string{"Content-Type": "text/plain"},
				Body:    []byte("{}"),
			})
			assertErrorResponse(t, response, 415)
		})
	}
}

func TestManagementRejectsUnknownJSONFieldsAndPassesExactAuditConfirmation(t *testing.T) {
	fixture := contractRuntimeFixture()
	router := NewRouter(fixture, Assets{})
	unknown := router.Handle(Request{
		Method:  "POST",
		Path:    contractPluginPath + "/quota/reset",
		Headers: jsonHeaders(),
		Body:    []byte(`{"account_key":"acct-a","idempotency_key":"11111111-1111-4111-8111-111111111111","extra":true}`),
	})
	assertErrorResponse(t, unknown, 400)
	if fixture.resetCalls != 0 {
		t.Fatalf("reset was called for an unknown field")
	}

	response := router.Handle(Request{
		Method:  "DELETE",
		Path:    contractPluginPath + "/reset-audit",
		Headers: map[string]string{"X-Confirmation": "DELETE AUDIT"},
	})
	if response.Status != 200 || fixture.confirmation != "DELETE AUDIT" {
		t.Fatalf("confirmation = %q, status=%d", fixture.confirmation, response.Status)
	}
}

func TestManagementResponsesAndAssetsContainNoCredentialMaterial(t *testing.T) {
	router := NewRouter(contractRuntimeFixture(), contractAssets(t))
	requests := []Request{
		{Method: "GET", Path: contractPluginPath + "/accounts"},
		{Method: "GET", Path: contractPluginPath + "/quota"},
		{Method: "GET", Path: contractPluginPath + "/history"},
		{Method: "GET", Path: contractPluginPath + "/reset-audit"},
		{Method: "GET", Path: "/v0/resource/plugins/codex-window-reset-linux-arm64/panel"},
	}
	for _, request := range requests {
		response := router.Handle(request)
		if strings.Contains(string(response.Body), "sk-secret") ||
			strings.Contains(string(response.Body), "raw@example.com") ||
			strings.Contains(string(response.Body), "management-secret") ||
			strings.Contains(string(response.Body), "raw-auth-json") {
			t.Fatalf("credential material leaked in %s: %s", request.Path, response.Body)
		}
	}

	root := repositoryRoot(t)
	for _, path := range []string{
		filepath.Join(root, "web"), filepath.Join(root, "internal", "management"),
	} {
		var files []string
		if err := filepath.Walk(path, func(name string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && !strings.HasSuffix(name, "_test.go") {
				files = append(files, name)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, name := range files {
			body, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			text := strings.ToLower(string(body))
			for _, forbidden := range []string{
				"access" + "_token",
				"authorization: " + "bearer",
				"management " + "key",
				"local" + "storage",
				"session" + "storage",
			} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("forbidden credential behavior %q in %s", forbidden, name)
				}
			}
		}
	}
}

func TestResourceRegistrationUsesExactContentTypes(t *testing.T) {
	registration := Registration("codex-window-reset-linux-amd64")
	for _, resource := range registration.Resources {
		want, ok := assetTypes[resource.Path]
		if !ok || resource.ContentType != want {
			t.Fatalf("resource %#v has content type %q, want %q", resource, resource.ContentType, want)
		}
	}
	for _, invalid := range []string{
		"/v0/resource/plugins/codex-window-reset-linux-amd64/modules/missing.js",
		"/v0/resource/plugins/codex-window-reset-linux-amd64/modules/api.js/extra",
	} {
		response := NewRouter(contractRuntimeFixture(), contractAssets(t)).Handle(Request{Method: "GET", Path: invalid})
		assertErrorResponse(t, response, 404)
	}
}

func TestPluginIDExtractionAcceptsHostFieldVariantsAndRejectsAmbiguousPaths(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "canonical", value: "/v0/resource/plugins/codex-window-reset-linux-amd64", want: "codex-window-reset-linux-amd64"},
		{name: "slash", value: "/v0/resource/plugins/codex-window-reset-linux-amd64/extra", want: DefaultPluginID},
		{name: "query", value: "/v0/resource/plugins/codex-window-reset-linux-amd64?x=1", want: DefaultPluginID},
		{name: "whitespace", value: " /v0/resource/plugins/codex-window-reset-linux-amd64", want: DefaultPluginID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := PluginIDFromResourceBasePath(tc.value); got != tc.want {
				t.Fatalf("plugin id = %q, want %q", got, tc.want)
			}
		})
	}
	fields := map[string]string{"resourceBasePath": "/v0/resource/plugins/codex-window-reset-linux-arm64"}
	if got := PluginIDFromResourceFields(fields); got != "codex-window-reset-linux-arm64" {
		t.Fatalf("field variant plugin id = %q", got)
	}
}

func contractRuntimeFixture() *contractRuntime {
	return &contractRuntime{
		status: domain.StatusView{Enabled: false, NextRuns: map[string]time.Time{}},
		accounts: []accounts.Account{{
			Key: "acct-a", AuthIndex: "fixture-auth", MaskedIdentity: "r***@example.com", PlanLabel: "Pro",
		}},
		quota:   []domain.SnapshotView{{Snapshot: domain.UsageSnapshot{AccountKey: "acct-a"}}},
		history: []domain.OperationRecord{{AccountKey: "acct-a", MaskedIdentity: "r***@example.com"}},
		audit:   []domain.ResetAudit{{AccountKey: "acct-a", MaskedIdentity: "r***@example.com", Outcome: domain.ResetSucceeded}},
	}
}

type contractRuntime struct {
	status       domain.StatusView
	accounts     []accounts.Account
	quota        []domain.SnapshotView
	history      []domain.OperationRecord
	audit        []domain.ResetAudit
	resetCalls   int
	confirmation string
}

func (r *contractRuntime) Status() domain.StatusView { return r.status }
func (r *contractRuntime) ListAccounts(context.Context) ([]accounts.Account, error) {
	return r.accounts, nil
}
func (r *contractRuntime) Schedule() domain.Config { return domain.DefaultConfig() }
func (r *contractRuntime) UpdateSchedule(_ context.Context, config domain.Config) (domain.Config, error) {
	config.Revision++
	return config, nil
}
func (r *contractRuntime) Simulate(context.Context, domain.Config) (domain.SimulationResult, error) {
	return domain.SimulationResult{}, nil
}
func (r *contractRuntime) StartManualProbes(context.Context, []string, bool) (string, error) {
	return "run-contract", nil
}
func (r *contractRuntime) ListHistory() ([]domain.OperationRecord, error) { return r.history, nil }
func (r *contractRuntime) ClearHistory() error                            { return nil }
func (r *contractRuntime) ListQuota(context.Context) ([]domain.SnapshotView, error) {
	return r.quota, nil
}
func (r *contractRuntime) RefreshQuotas(context.Context, []string) ([]domain.SnapshotView, error) {
	return r.quota, nil
}
func (r *contractRuntime) ResetQuota(context.Context, string, string) (domain.ResetAudit, error) {
	r.resetCalls++
	return domain.ResetAudit{Outcome: domain.ResetSucceeded}, nil
}
func (r *contractRuntime) ListResetAudit() ([]domain.ResetAudit, error) { return r.audit, nil }
func (r *contractRuntime) ClearResetAudit(confirmation string) error {
	r.confirmation = confirmation
	return nil
}

func jsonHeaders() map[string]string { return map[string]string{"Content-Type": "application/json"} }

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func contractAssets(t *testing.T) Assets {
	t.Helper()
	return NewAssets(os.DirFS(filepath.Join(repositoryRoot(t), "web")))
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}
