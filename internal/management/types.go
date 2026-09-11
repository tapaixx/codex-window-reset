package management

import (
	"context"
	"strings"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/accounts"
	"github.com/tapaixx/codex-window-reset/internal/domain"
)

const DefaultPluginID = "codex-window-reset"

// Request is the small transport value passed from the host ABI to Router.
// Headers supports scalar test/adapter callers while Header accepts the
// standard multi-value representation used by HTTP bridges.
type Request struct {
	Method      string
	Path        string
	Headers     map[string]string
	Header      map[string][]string
	ContentType string
	Body        []byte
	Context     context.Context
}

// Response is intentionally independent of net/http so it can cross the
// native plugin boundary without importing an HTTP server implementation.
type Response struct {
	Status      int
	StatusCode  int
	ContentType string
	Headers     map[string]string
	Body        []byte
}

type Route struct {
	Method  string
	Path    string
	Name    string
	Handler string
}

type Resource struct {
	Path        string
	ContentType string
	Menu        string
}

type RegistrationResult struct {
	Routes    []Route
	Resources []Resource
}

// RuntimeAPI is the narrow management-facing projection of app.Runtime. It
// contains only sanitized domain values and operation methods; host
// authentication and credential material are outside this interface.
type RuntimeAPI interface {
	Status() domain.StatusView
	ListAccounts(context.Context) ([]accounts.Account, error)
	Schedule() domain.Config
	UpdateSchedule(context.Context, domain.Config) (domain.Config, error)
	Simulate(context.Context, domain.Config) (domain.SimulationResult, error)
	StartManualProbes(context.Context, []string, bool) (string, error)
	ListHistory() ([]domain.OperationRecord, error)
	ClearHistory() error
	ListQuota(context.Context) ([]domain.SnapshotView, error)
	RefreshQuotas(context.Context, []string) ([]domain.SnapshotView, error)
	ResetQuota(context.Context, string, string) (domain.ResetAudit, error)
	ListResetAudit() ([]domain.ResetAudit, error)
	ClearResetAudit(string) error
}

type statusResult struct {
	Enabled            bool                 `json:"enabled"`
	StoreErrorCode     domain.ErrorCode     `json:"store_error_code,omitempty"`
	NextRuns           map[string]time.Time `json:"next_runs,omitempty"`
	RunID              string               `json:"run_id,omitempty"`
	RunTotal           int                  `json:"run_total,omitempty"`
	RunCompleted       int                  `json:"run_completed,omitempty"`
	GuardrailHoldCount int                  `json:"guardrail_hold_count,omitempty"`
}

type envelope struct {
	OK     bool           `json:"ok"`
	Result any            `json:"result,omitempty"`
	Error  *errorEnvelope `json:"error,omitempty"`
}

type errorEnvelope struct {
	Code          domain.ErrorCode `json:"code"`
	Message       string           `json:"message"`
	Retryable     bool             `json:"retryable"`
	CorrelationID string           `json:"correlation_id"`
}

func (r Request) context() context.Context {
	if r.Context != nil {
		return r.Context
	}
	return context.Background()
}

func (r Request) header(name string) string {
	if strings.EqualFold(name, "Content-Type") && r.ContentType != "" {
		return r.ContentType
	}
	for key, value := range r.Headers {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			return value
		}
	}
	for key, values := range r.Header {
		if strings.EqualFold(strings.TrimSpace(key), name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}
