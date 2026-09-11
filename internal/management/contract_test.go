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
	path, forbidden, err := findForbiddenCredentialBehavior(root)
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Fatalf("forbidden credential behavior %q in %s", forbidden, filepath.Join(root, path))
	}
}

func TestManagementCredentialScanUsesProductionBoundary(t *testing.T) {
	const forbiddenBehavior = "localStorage"

	t.Run("rejects production-like asset", func(t *testing.T) {
		root := credentialScanFixtureRoot(t)
		writeCredentialScanFixture(t, root, "web/modules/api.js", "const persisted = localStorage;")

		path, forbidden, err := findForbiddenCredentialBehavior(root)
		if err != nil {
			t.Fatal(err)
		}
		if forbidden != strings.ToLower(forbiddenBehavior) {
			t.Fatalf("forbidden behavior = %q, want %q", forbidden, strings.ToLower(forbiddenBehavior))
		}
		if filepath.ToSlash(path) != "web/modules/api.js" {
			t.Fatalf("forbidden behavior path = %q, want web/modules/api.js", filepath.ToSlash(path))
		}
	})

	t.Run("ignores test fixture", func(t *testing.T) {
		root := credentialScanFixtureRoot(t)
		writeCredentialScanFixture(t, root, "web/tests/api.test.mjs", "assert(localStorage);")

		path, forbidden, err := findForbiddenCredentialBehavior(root)
		if err != nil {
			t.Fatal(err)
		}
		if path != "" || forbidden != "" {
			t.Fatalf("test fixture was scanned: path=%q behavior=%q", path, forbidden)
		}
	})
}

func credentialScanFixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, assetPath := range assetPaths {
		name := strings.TrimPrefix(assetPath, "/")
		if assetPath == "/panel" {
			name = "panel.html"
		}
		writeCredentialScanFixture(t, root, filepath.Join("web", filepath.FromSlash(name)), "")
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "management"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeCredentialScanFixture(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func findForbiddenCredentialBehavior(root string) (string, string, error) {
	files, err := productionCredentialSourceFiles(root)
	if err != nil {
		return "", "", err
	}
	for _, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			return "", "", err
		}
		text := strings.ToLower(string(body))
		for _, forbidden := range credentialBehaviorTerms() {
			if strings.Contains(text, forbidden) {
				relative, err := filepath.Rel(root, path)
				if err != nil {
					return "", "", err
				}
				return relative, forbidden, nil
			}
		}
	}
	return "", "", nil
}

func productionCredentialSourceFiles(root string) ([]string, error) {
	files := make([]string, 0, len(assetPaths))
	for _, assetPath := range assetPaths {
		name := strings.TrimPrefix(assetPath, "/")
		if assetPath == "/panel" {
			name = "panel.html"
		}
		path := filepath.Join(root, "web", filepath.FromSlash(name))
		if _, err := os.Stat(path); err != nil {
			return nil, err
		}
		files = append(files, path)
	}

	managementRoot := filepath.Join(root, "internal", "management")
	if err := filepath.Walk(managementRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".go") || strings.HasSuffix(info.Name(), "_test.go") {
			return nil
		}
		files = append(files, path)
		return nil
	}); err != nil {
		return nil, err
	}
	return files, nil
}

func credentialBehaviorTerms() []string {
	return []string{
		"access" + "_token",
		"authorization: " + "bearer",
		"management " + "key",
		"local" + "storage",
		"session" + "storage",
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

func TestManagementResourcesServeEveryRegisteredAsset(t *testing.T) {
	router := NewRouter(contractRuntimeFixture(), contractAssets(t))
	expectedContentTypes := map[string]string{
		"/panel":                "text/html; charset=utf-8",
		"/styles.css":           "text/css; charset=utf-8",
		"/modules/api.js":       "text/javascript; charset=utf-8",
		"/modules/state.js":     "text/javascript; charset=utf-8",
		"/modules/accounts.js":  "text/javascript; charset=utf-8",
		"/modules/schedule.js":  "text/javascript; charset=utf-8",
		"/modules/simulator.js": "text/javascript; charset=utf-8",
		"/modules/history.js":   "text/javascript; charset=utf-8",
		"/modules/main.js":      "text/javascript; charset=utf-8",
	}
	for _, resource := range Registration("codex-window-reset-linux-amd64").Resources {
		wantContentType, ok := expectedContentTypes[resource.Path]
		if !ok {
			t.Fatalf("resource %q has no expected content type", resource.Path)
		}
		response := router.Handle(Request{
			Method: "GET",
			Path:   "/v0/resource/plugins/codex-window-reset-linux-amd64" + resource.Path,
		})
		if response.Status != 200 {
			t.Fatalf("resource %q status = %d, want 200: %s", resource.Path, response.Status, response.Body)
		}
		if got := response.Headers["Content-Type"]; got != wantContentType {
			t.Fatalf("resource %q content type = %q, want %q", resource.Path, got, wantContentType)
		}
		if response.ContentType != wantContentType {
			t.Fatalf("resource %q response content type = %q, want %q", resource.Path, response.ContentType, wantContentType)
		}
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
