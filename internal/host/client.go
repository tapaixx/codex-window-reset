package host

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

const (
	opAuthList = "host.auth.list"
	opAuthGet  = "host.auth.get"
	opHTTPDo   = "host.http.do"
	opLog      = "host.log"
)

var errNilCaller = errors.New("host caller is nil")
var errEmptyAuth = errors.New("empty auth document")

// AuthListResponse is the host.auth.list result envelope.
type AuthListResponse struct {
	Files []AuthFile `json:"files"`
}

// AuthGetResponse is the host.auth.get result envelope.  Hosts have used all
// three names for the payload over time; the adapter accepts each one.
type AuthGetResponse struct {
	JSON json.RawMessage `json:"json"`
	Auth json.RawMessage `json:"auth"`
	Data json.RawMessage `json:"data"`
}

// Client adapts the untyped ABI callback into the typed host API used by the
// rest of the plugin.
type Client struct {
	caller Caller
}

var _ API = (*Client)(nil)

// NewClient constructs a host adapter around the injected ABI callback.
func NewClient(caller Caller) *Client { return &Client{caller: caller} }

// New is a short constructor alias for callers that use package-level service
// constructors consistently.
func New(caller Caller) *Client { return NewClient(caller) }

func (c *Client) call(ctx context.Context, operation string, request, response any) error {
	if c == nil || c.caller == nil {
		return errNilCaller
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return c.caller(ctx, operation, request, response)
}

// ListAuthFiles returns host-owned account metadata.  It does not read the
// credential directory directly.
func (c *Client) ListAuthFiles(ctx context.Context) ([]AuthFile, error) {
	var response AuthListResponse
	if err := c.call(ctx, opAuthList, map[string]any{}, &response); err != nil {
		return nil, err
	}
	if response.Files == nil {
		response.Files = []AuthFile{}
	}
	return response.Files, nil
}

// GetAuth obtains one credential document through the host boundary.  The
// returned bytes are copied once so callers never retain host-owned storage.
func (c *Client) GetAuth(ctx context.Context, authIndex string) (json.RawMessage, error) {
	var response AuthGetResponse
	if err := c.call(ctx, opAuthGet, map[string]any{"auth_index": authIndex}, &response); err != nil {
		return nil, err
	}
	raw := response.JSON
	if len(raw) == 0 {
		raw = response.Auth
	}
	if len(raw) == 0 {
		raw = response.Data
	}
	if len(raw) == 0 {
		return nil, errEmptyAuth
	}
	return append(json.RawMessage(nil), raw...), nil
}

// HTTPDo performs one host-mediated outbound request and copies response
// buffers before returning them to the caller.
func (c *Client) HTTPDo(ctx context.Context, request HTTPRequest) (HTTPResponse, error) {
	var response HTTPResponse
	if err := c.call(ctx, opHTTPDo, request, &response); err != nil {
		return HTTPResponse{}, err
	}
	response.Body = append([]byte(nil), response.Body...)
	if response.Headers != nil {
		headers := make(map[string][]string, len(response.Headers))
		for key, values := range response.Headers {
			headers[key] = append([]string(nil), values...)
		}
		response.Headers = headers
	}
	return response, nil
}

// Log forwards a structured event to the host.  Logging is best effort: a
// logging failure must not change the outcome of the operation being logged.
func (c *Client) Log(ctx context.Context, level, message string, fields map[string]any) {
	request := map[string]any{
		"level":   safeLogLevel(level),
		"message": "codex-window-reset",
		"fields":  safeLogFields(fields),
	}
	_ = c.call(ctx, opLog, request, nil)
}

func safeLogLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug", "info", "warn", "error":
		return strings.ToLower(strings.TrimSpace(level))
	default:
		return "info"
	}
}

func safeLogFields(fields map[string]any) map[string]any {
	allowed := map[string]struct{}{
		"correlation_id":      {},
		"account_fingerprint": {},
		"error_code":          {},
		"latency_ms":          {},
		"http_category":       {},
	}
	safe := make(map[string]any, len(allowed))
	for key, value := range fields {
		if _, ok := allowed[key]; ok {
			safe[key] = value
		}
	}
	return safe
}
