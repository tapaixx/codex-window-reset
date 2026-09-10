package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"runtime"
	"strings"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/accounts"
	"github.com/tapaixx/codex-window-reset/internal/domain"
	"github.com/tapaixx/codex-window-reset/internal/host"
)

const endpoint = "https://chatgpt.com/backend-api/codex/responses"

type Client struct {
	api host.API
}

var _ interface {
	Execute(context.Context, accounts.Account, string, time.Duration) domain.ProbeResult
} = (*Client)(nil)

// New constructs a probe client using the host-mediated HTTP and credential
// capabilities. The client never stores credential material between calls.
func New(api host.API) *Client { return &Client{api: api} }

type inputMessage struct {
	Role    string      `json:"role"`
	Content []inputPart `json:"content"`
}

type inputPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type requestBody struct {
	Model             string         `json:"model"`
	Instructions      string         `json:"instructions"`
	Input             []inputMessage `json:"input"`
	Stream            bool           `json:"stream"`
	Store             bool           `json:"store"`
	ParallelToolCalls bool           `json:"parallel_tool_calls"`
	Include           []string       `json:"include"`
	Reasoning         reasoning      `json:"reasoning"`
}

type reasoning struct {
	Effort string `json:"effort"`
}

func newRequestBody(model string) requestBody {
	return requestBody{
		Model:        model,
		Instructions: "Return exactly OK.",
		Input: []inputMessage{{
			Role:    "user",
			Content: []inputPart{{Type: "input_text", Text: "Reply with exactly OK"}},
		}},
		Stream:            true,
		Store:             false,
		ParallelToolCalls: true,
		Include:           []string{"reasoning.encrypted_content"},
		Reasoning:         reasoning{Effort: "low"},
	}
}

// Execute makes one real Codex request and returns only a sanitized outcome.
// Authentication material is extracted immediately before the HTTP call and
// is not retained after this method returns.
func (c *Client) Execute(ctx context.Context, account accounts.Account, model string, timeout time.Duration) (result domain.ProbeResult) {
	started := time.Now()
	defer func() {
		result.LatencyMS = time.Since(started).Milliseconds()
	}()

	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if c == nil || c.api == nil {
		return failedResult(domain.RequestNetworkError, 0, true)
	}
	if outcome, retry, ok := contextOutcome(callCtx); ok {
		return failedResult(outcome, 0, retry)
	}

	material, err := accounts.AuthMaterial(callCtx, c.api, account)
	if err != nil {
		if outcome, retry, ok := contextOutcome(callCtx); ok {
			return failedResult(outcome, 0, retry)
		}
		return failedResult(domain.RequestCredentialError, 0, false)
	}
	if outcome, retry, ok := contextOutcome(callCtx); ok {
		return failedResult(outcome, 0, retry)
	}

	body, err := json.Marshal(newRequestBody(model))
	if err != nil {
		return failedResult(domain.RequestResponseError, 0, false)
	}

	response, err := c.api.HTTPDo(callCtx, host.HTTPRequest{
		Method: "POST",
		URL:    endpoint,
		Headers: map[string][]string{
			"Authorization":      {"Bearer " + material.AccessToken},
			"Chatgpt-Account-Id": {material.AccountID},
			"Content-Type":       {"application/json"},
			"Accept":             {"text/event-stream"},
			"Originator":         {"codex-tui"},
			"User-Agent":         {userAgent()},
		},
		Body: body,
	})
	if response.StatusCode != 0 {
		result.HTTPStatus = response.StatusCode
	}
	if err != nil {
		if outcome, retry, ok := transportOutcome(callCtx, err); ok {
			return failedResultWithStatus(outcome, response.StatusCode, retry)
		}
		return failedResultWithStatus(domain.RequestNetworkError, response.StatusCode, true)
	}
	if outcome, retry, ok := contextOutcome(callCtx); ok {
		return failedResultWithStatus(outcome, response.StatusCode, retry)
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		outcome, retry := statusOutcome(response.StatusCode)
		return failedResultWithStatus(outcome, response.StatusCode, retry)
	}

	text, err := ParseCompleted(bytes.NewReader(response.Body))
	if err != nil {
		return failedResultWithStatus(domain.RequestResponseError, response.StatusCode, false)
	}
	if strings.TrimSpace(text) != "OK" {
		return failedResultWithStatus(domain.RequestUnexpectedOutput, response.StatusCode, false)
	}
	return domain.ProbeResult{
		Outcome:    domain.RequestSucceeded,
		HTTPStatus: response.StatusCode,
	}
}

func userAgent() string {
	return fmt.Sprintf("codex-window-reset/0.0.0-dev (Linux; %s)", runtime.GOARCH)
}

func contextOutcome(ctx context.Context) (domain.RequestOutcome, bool, bool) {
	if ctx == nil {
		return "", false, false
	}
	switch ctx.Err() {
	case context.DeadlineExceeded:
		return domain.RequestTimeout, true, true
	case context.Canceled:
		return domain.RequestNetworkError, false, true
	default:
		return "", false, false
	}
}

func transportOutcome(ctx context.Context, err error) (domain.RequestOutcome, bool, bool) {
	if errors.Is(err, context.DeadlineExceeded) || (ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		return domain.RequestTimeout, true, true
	}
	var timeoutErr net.Error
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return domain.RequestTimeout, true, true
	}
	if errors.Is(err, context.Canceled) || (ctx != nil && errors.Is(ctx.Err(), context.Canceled)) {
		return domain.RequestNetworkError, false, true
	}
	return "", false, false
}

func statusOutcome(status int) (domain.RequestOutcome, bool) {
	switch status {
	case 401:
		return domain.RequestUnauthorized, false
	case 403:
		return domain.RequestForbidden, false
	case 402:
		return domain.RequestPaymentRequired, false
	case 429:
		return domain.RequestRateLimited, true
	default:
		return domain.RequestUpstreamError, status >= 500 && status <= 599
	}
}

func failedResult(outcome domain.RequestOutcome, status int, retry bool) domain.ProbeResult {
	return failedResultWithStatus(outcome, status, retry)
}

func failedResultWithStatus(outcome domain.RequestOutcome, status int, retry bool) domain.ProbeResult {
	return domain.ProbeResult{
		Outcome:       outcome,
		HTTPStatus:    status,
		ErrorCode:     domain.CodeProbeFailed,
		RetryEligible: retry,
	}
}
