package management

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/tapaixx/codex-window-reset/internal/domain"
)

// A CDN with no explicit directive applies its own heuristics. A cached panel
// keeps serving an older UI after an upgrade — its asset references no longer
// resolve and the page renders blank — while the origin stays healthy, so the
// fault is invisible from the server side.
func TestEveryManagementResponseForbidsCaching(t *testing.T) {
	assets := NewAssets(fstest.MapFS{"panel.html": {Data: []byte("<!doctype html>")}})
	router := NewRouter(testRuntime{status: domain.StatusView{Enabled: true}}, assets)

	cases := []struct {
		name string
		req  Request
	}{
		{"status", Request{Method: "GET", Path: "/v0/management/plugins/codex-window-reset/status"}},
		{"panel asset", Request{Method: "GET", Path: "/v0/resource/plugins/codex-window-reset/panel"}},
		{"unknown route error", Request{Method: "GET", Path: "/v0/management/plugins/codex-window-reset/nope"}},
		{"method not allowed", Request{Method: "DELETE", Path: "/v0/management/plugins/codex-window-reset/status"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := router.Handle(testCase.req)
			control := response.Headers["Cache-Control"]
			if !strings.Contains(control, "no-store") {
				t.Fatalf("Cache-Control=%q, want no-store (status %d)", control, response.StatusCode)
			}
			if response.Headers["Content-Type"] == "" {
				t.Fatal("Content-Type was dropped while adding cache headers")
			}
		})
	}
}

// The Allow header travels with a 405 and must survive the cache headers.
func TestExtraHeadersSurviveTheCacheDirectives(t *testing.T) {
	router := NewRouter(testRuntime{}, Assets{})
	response := router.Handle(Request{Method: "DELETE", Path: "/v0/management/plugins/codex-window-reset/status"})
	if response.Headers["Allow"] == "" {
		t.Fatalf("Allow header lost: %#v", response.Headers)
	}
	if !strings.Contains(response.Headers["Cache-Control"], "no-store") {
		t.Fatalf("Cache-Control=%q", response.Headers["Cache-Control"])
	}
}
