package probe

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/accounts"
	"github.com/tapaixx/codex-window-reset/internal/domain"
	"github.com/tapaixx/codex-window-reset/internal/host"
)

type probeTestHost struct {
	raw      json.RawMessage
	response host.HTTPResponse
	httpErr  error
	request  host.HTTPRequest
}

func (*probeTestHost) ListAuthFiles(context.Context) ([]host.AuthFile, error) {
	return nil, nil
}

func (f *probeTestHost) GetAuth(context.Context, string) (json.RawMessage, error) {
	return f.raw, nil
}

func (f *probeTestHost) HTTPDo(_ context.Context, request host.HTTPRequest) (host.HTTPResponse, error) {
	f.request = request
	return f.response, f.httpErr
}

func (*probeTestHost) Log(context.Context, string, string, map[string]any) {}

func TestExecuteBuildsConfigurableModelRequest(t *testing.T) {
	fake := &probeTestHost{
		raw:      json.RawMessage(`{"access_token":"secret-token","account_id":"account-id"}`),
		response: host.HTTPResponse{StatusCode: 200, Body: []byte(okEvent)},
	}

	got := New(fake).Execute(context.Background(), accounts.Account{AuthIndex: "auth-index"}, "gpt-custom", time.Second)
	if got.Outcome != domain.RequestSucceeded || got.HTTPStatus != 200 || got.RetryEligible {
		t.Fatalf("result = %#v", got)
	}

	if fake.request.Method != "POST" || fake.request.URL != endpoint {
		t.Fatalf("request target = %#v", fake.request)
	}
	if got := fake.request.Headers["Authorization"]; len(got) != 1 || got[0] != "Bearer secret-token" {
		t.Fatalf("authorization header = %#v", got)
	}
	if got := fake.request.Headers["Chatgpt-Account-Id"]; len(got) != 1 || got[0] != "account-id" {
		t.Fatalf("account header = %#v", got)
	}
	if got := fake.request.Headers["Content-Type"]; len(got) != 1 || got[0] != "application/json" {
		t.Fatalf("content type header = %#v", got)
	}
	if got := fake.request.Headers["Accept"]; len(got) != 1 || got[0] != "text/event-stream" {
		t.Fatalf("accept header = %#v", got)
	}
	if got := fake.request.Headers["Originator"]; len(got) != 1 || got[0] != "codex-tui" {
		t.Fatalf("originator header = %#v", got)
	}
	userAgent := fake.request.Headers["User-Agent"]
	if len(userAgent) != 1 || !strings.Contains(userAgent[0], "Linux") || !strings.Contains(userAgent[0], runtime.GOARCH) {
		t.Fatalf("user agent = %#v", userAgent)
	}

	var body struct {
		Model        string `json:"model"`
		Instructions string `json:"instructions"`
		Input        []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
		Stream            bool     `json:"stream"`
		Store             bool     `json:"store"`
		ParallelToolCalls bool     `json:"parallel_tool_calls"`
		Include           []string `json:"include"`
		Reasoning         struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	if err := json.Unmarshal(fake.request.Body, &body); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	if body.Model != "gpt-custom" || body.Instructions != "Return exactly OK." || !body.Stream || body.Store || !body.ParallelToolCalls {
		t.Fatalf("request options = %#v", body)
	}
	if len(body.Include) != 1 || body.Include[0] != "reasoning.encrypted_content" || body.Reasoning.Effort != "low" {
		t.Fatalf("request reasoning = %#v", body)
	}
	if len(body.Input) != 1 || body.Input[0].Role != "user" || len(body.Input[0].Content) != 1 || body.Input[0].Content[0].Type != "input_text" || body.Input[0].Content[0].Text != "Reply with exactly OK" {
		t.Fatalf("request input = %#v", body.Input)
	}
}

func TestExecuteMapsHTTPOutcomes(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   domain.RequestOutcome
		retry  bool
	}{
		{name: "success", status: 200, want: domain.RequestSucceeded},
		{name: "unauthorized", status: 401, want: domain.RequestUnauthorized},
		{name: "forbidden", status: 403, want: domain.RequestForbidden},
		{name: "payment required", status: 402, want: domain.RequestPaymentRequired},
		{name: "rate limited", status: 429, want: domain.RequestRateLimited, retry: true},
		{name: "upstream error", status: 500, want: domain.RequestUpstreamError, retry: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &probeTestHost{
				raw:      json.RawMessage(`{"access_token":"secret-token","account_id":"account-id"}`),
				response: host.HTTPResponse{StatusCode: tt.status, Body: []byte(okEvent)},
			}
			got := New(fake).Execute(context.Background(), accounts.Account{AuthIndex: "auth-index"}, "gpt-custom", time.Second)
			if got.Outcome != tt.want || got.RetryEligible != tt.retry || got.HTTPStatus != tt.status {
				t.Fatalf("result = %#v, want outcome %q retry=%t status=%d", got, tt.want, tt.retry, tt.status)
			}
		})
	}
}

func TestExecuteMapsTransportAndResponseFailures(t *testing.T) {
	tests := []struct {
		name    string
		httpErr error
		body    string
		want    domain.RequestOutcome
		retry   bool
	}{
		{name: "timeout", httpErr: context.DeadlineExceeded, want: domain.RequestTimeout, retry: true},
		{name: "timeout network error", httpErr: probeTimeoutError{}, want: domain.RequestTimeout, retry: true},
		{name: "network error", httpErr: errors.New("connection refused"), want: domain.RequestNetworkError, retry: true},
		{name: "caller cancellation", httpErr: context.Canceled, want: domain.RequestNetworkError},
		{name: "malformed terminal JSON", body: "data: {\"type\":\"response.completed\"\n", want: domain.RequestResponseError},
		{name: "a differently worded completion still opened the window", body: `data: {"type":"response.output_text.done","text":"NOT OK"}` + "\ndata: {\"type\":\"response.completed\"}\n", want: domain.RequestSucceeded},
		{name: "a completion with no text at all", body: `data: {"type":"response.output_text.done","text":"   "}` + "\ndata: {\"type\":\"response.completed\"}\n", want: domain.RequestUnexpectedOutput},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := tt.body
			if body == "" {
				body = okEvent
			}
			fake := &probeTestHost{
				raw:      json.RawMessage(`{"access_token":"secret-token","account_id":"account-id"}`),
				response: host.HTTPResponse{StatusCode: 200, Body: []byte(body)},
				httpErr:  tt.httpErr,
			}
			got := New(fake).Execute(context.Background(), accounts.Account{AuthIndex: "auth-index"}, "gpt-custom", time.Second)
			if got.Outcome != tt.want || got.RetryEligible != tt.retry {
				t.Fatalf("result = %#v, want outcome %q retry=%t", got, tt.want, tt.retry)
			}
		})
	}
}

type probeTimeoutError struct{}

func (probeTimeoutError) Error() string   { return "timed out" }
func (probeTimeoutError) Timeout() bool   { return true }
func (probeTimeoutError) Temporary() bool { return true }

func TestExecuteMapsCredentialFailureWithoutCallingHTTP(t *testing.T) {
	fake := &probeTestHost{raw: json.RawMessage(`{"account_id":"account-id"}`)}
	got := New(fake).Execute(context.Background(), accounts.Account{AuthIndex: "auth-index"}, "gpt-custom", time.Second)
	if got.Outcome != domain.RequestCredentialError || got.RetryEligible || fake.request.URL != "" {
		t.Fatalf("result = %#v, request = %#v", got, fake.request)
	}
}

const okEvent = `data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"OK"}]}]}}` + "\n"
