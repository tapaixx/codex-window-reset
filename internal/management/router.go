package management

import (
	"bytes"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"strconv"
	"strings"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/domain"
)

type Router struct {
	runtime RuntimeAPI
	assets  Assets
}

func NewRouter(runtime RuntimeAPI, assets Assets) *Router {
	return &Router{runtime: runtime, assets: assets}
}

func (r *Router) Handle(request Request) Response {
	correlationID := newCorrelationID()
	if endpoint, ok := managementEndpoint(request.Path); ok {
		return r.handleManagement(request, endpoint, correlationID)
	}
	if assetPath, ok := resourceAssetPath(request.Path); ok {
		if request.Method != "GET" {
			return errorResponseWithHeaders(methodError("GET"), correlationID, map[string]string{"Allow": "GET"})
		}
		body, contentType, err := r.assets.Read(assetPath)
		if err != nil {
			return errorResponse(domainError(domain.CodeConfigInvalid, 404, false, "resource was not found"), correlationID)
		}
		return Response{
			Status:      200,
			StatusCode:  200,
			ContentType: contentType,
			Headers:     map[string]string{"Content-Type": contentType},
			Body:        append([]byte(nil), body...),
		}
	}
	return errorResponse(domainError(domain.CodeConfigInvalid, 404, false, "route was not found"), correlationID)
}

func (r *Router) handleManagement(request Request, endpoint, correlationID string) Response {
	method := request.Method
	switch endpoint {
	case "/status":
		if method != "GET" {
			return errorResponseWithHeaders(methodError("GET"), correlationID, map[string]string{"Allow": "GET"})
		}
		if r.runtime == nil {
			return errorResponse(domainError(domain.CodeStoreCorrupt, 500, false, "runtime is unavailable"), correlationID)
		}
		view := r.runtime.Status()
		result := statusResult{
			Enabled:            view.Enabled,
			StoreErrorCode:     view.StoreErrorCode,
			RunID:              view.RunID,
			RunTotal:           view.RunTotal,
			RunCompleted:       view.RunCompleted,
			GuardrailHoldCount: view.GuardrailHoldCount,
		}
		if len(view.NextRuns) > 0 {
			result.NextRuns = make(map[string]time.Time, len(view.NextRuns))
			for key, instant := range view.NextRuns {
				result.NextRuns[key] = instant.UTC()
			}
		}
		return successResponse(result, correlationID)

	case "/upcoming":
		if method != "GET" {
			return errorResponseWithHeaders(methodError("GET"), correlationID, map[string]string{"Allow": "GET"})
		}
		if r.runtime == nil {
			return errorResponse(domainError(domain.CodeStoreCorrupt, 500, false, "runtime is unavailable"), correlationID)
		}
		return successResponse(r.runtime.Upcoming(), correlationID)

	case "/accounts":
		if method != "GET" {
			return errorResponseWithHeaders(methodError("GET"), correlationID, map[string]string{"Allow": "GET"})
		}
		if r.runtime == nil {
			return errorResponse(domainError(domain.CodeStoreCorrupt, 500, false, "runtime is unavailable"), correlationID)
		}
		result, err := r.runtime.ListAccounts(request.context())
		if err != nil {
			return errorResponse(err, correlationID)
		}
		return successResponse(result, correlationID)

	case "/schedule":
		if method == "GET" {
			if r.runtime == nil {
				return errorResponse(domainError(domain.CodeStoreCorrupt, 500, false, "runtime is unavailable"), correlationID)
			}
			return successResponse(r.runtime.Schedule(), correlationID)
		}
		if method != "PUT" {
			return errorResponseWithHeaders(methodError("GET, PUT"), correlationID, map[string]string{"Allow": "GET, PUT"})
		}
		var draft domain.Config
		if err := decodeJSON(request, &draft); err != nil {
			return errorResponse(err, correlationID)
		}
		if r.runtime == nil {
			return errorResponse(domainError(domain.CodeStoreCorrupt, 500, false, "runtime is unavailable"), correlationID)
		}
		result, err := r.runtime.UpdateSchedule(request.context(), draft)
		if err != nil {
			return errorResponse(err, correlationID)
		}
		return successResponse(result, correlationID)

	case "/simulate":
		if method != "POST" {
			return errorResponseWithHeaders(methodError("POST"), correlationID, map[string]string{"Allow": "POST"})
		}
		var draft domain.Config
		if err := decodeJSON(request, &draft); err != nil {
			return errorResponse(err, correlationID)
		}
		if r.runtime == nil {
			return errorResponse(domainError(domain.CodeStoreCorrupt, 500, false, "runtime is unavailable"), correlationID)
		}
		result, err := r.runtime.Simulate(request.context(), draft)
		if err != nil {
			return errorResponse(err, correlationID)
		}
		return successResponse(result, correlationID)

	case "/probes":
		if method != "POST" {
			return errorResponseWithHeaders(methodError("POST"), correlationID, map[string]string{"Allow": "POST"})
		}
		var input probeRequest
		if err := decodeJSON(request, &input); err != nil {
			return errorResponse(err, correlationID)
		}
		if !input.AcknowledgeQuotaEffect {
			return errorResponse(domainError(domain.CodeConfigInvalid, 400, false, "quota effect must be acknowledged"), correlationID)
		}
		if r.runtime == nil {
			return errorResponse(domainError(domain.CodeStoreCorrupt, 500, false, "runtime is unavailable"), correlationID)
		}
		runID, err := r.runtime.StartManualProbes(request.context(), input.AccountKeys, input.AllowUnavailable)
		if err != nil {
			return errorResponse(err, correlationID)
		}
		return successResponse(probeResponse{RunID: runID}, correlationID, 202)

	case "/history":
		if method == "GET" {
			if r.runtime == nil {
				return errorResponse(domainError(domain.CodeStoreCorrupt, 500, false, "runtime is unavailable"), correlationID)
			}
			result, err := r.runtime.ListHistory()
			if err != nil {
				return errorResponse(err, correlationID)
			}
			return successResponse(result, correlationID)
		}
		if method != "DELETE" {
			return errorResponseWithHeaders(methodError("GET, DELETE"), correlationID, map[string]string{"Allow": "GET, DELETE"})
		}
		if r.runtime == nil {
			return errorResponse(domainError(domain.CodeStoreCorrupt, 500, false, "runtime is unavailable"), correlationID)
		}
		if err := r.runtime.ClearHistory(); err != nil {
			return errorResponse(err, correlationID)
		}
		return successResponse(clearResponse{Cleared: true}, correlationID)

	case "/quota-snapshot":
		if method != "GET" {
			return errorResponseWithHeaders(methodError("GET"), correlationID, map[string]string{"Allow": "GET"})
		}
		if r.runtime == nil {
			return errorResponse(domainError(domain.CodeStoreCorrupt, 500, false, "runtime is unavailable"), correlationID)
		}
		result, err := r.runtime.ListQuota(request.context())
		if err != nil {
			return errorResponse(err, correlationID)
		}
		return successResponse(result, correlationID)

	case "/quota-refresh":
		if method != "POST" {
			return errorResponseWithHeaders(methodError("POST"), correlationID, map[string]string{"Allow": "POST"})
		}
		var input quotaRefreshRequest
		if err := decodeJSON(request, &input); err != nil {
			return errorResponse(err, correlationID)
		}
		if r.runtime == nil {
			return errorResponse(domainError(domain.CodeStoreCorrupt, 500, false, "runtime is unavailable"), correlationID)
		}
		result, err := r.runtime.RefreshQuotas(request.context(), input.AccountKeys)
		if err != nil {
			return errorResponse(err, correlationID)
		}
		return successResponse(result, correlationID)
	}
	return errorResponse(domainError(domain.CodeConfigInvalid, 404, false, "route was not found"), correlationID)
}

type probeRequest struct {
	AccountKeys            []string `json:"account_keys"`
	AcknowledgeQuotaEffect bool     `json:"acknowledge_quota_effect"`
	AllowUnavailable       bool     `json:"allow_unavailable"`
}

type quotaRefreshRequest struct {
	AccountKeys []string `json:"account_keys"`
}

type probeResponse struct {
	RunID string `json:"run_id"`
}

type clearResponse struct {
	Cleared bool `json:"cleared"`
}

func decodeJSON(request Request, destination any) error {
	if err := requireJSONContentType(request); err != nil {
		return err
	}
	trimmed := bytes.TrimSpace(request.Body)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return domainError(domain.CodeConfigInvalid, 400, false, "request body must contain one JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return domainError(domain.CodeConfigInvalid, 400, false, "request body is malformed JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return domainError(domain.CodeConfigInvalid, 400, false, "request body must contain one JSON value")
	}
	return nil
}

func requireJSONContentType(request Request) error {
	value := request.header("Content-Type")
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return domainError(domain.CodeConfigInvalid, 415, false, "content type must be application/json")
	}
	return nil
}

func successResponse(result any, correlationID string, status ...int) Response {
	code := 200
	if len(status) > 0 {
		code = status[0]
	}
	body, err := json.Marshal(envelope{OK: true, Result: result})
	if err != nil {
		return errorResponse(err, correlationID)
	}
	return Response{
		Status:      code,
		StatusCode:  code,
		ContentType: "application/json; charset=utf-8",
		Headers:     map[string]string{"Content-Type": "application/json; charset=utf-8"},
		Body:        body,
	}
}

func errorResponse(err error, correlationID string) Response {
	return errorResponseWithHeaders(err, correlationID, nil)
}

func errorResponseWithHeaders(err error, correlationID string, extra map[string]string) Response {
	code, message, retryable, status := managementError(err)
	body, marshalErr := json.Marshal(envelope{
		OK: false,
		Error: &errorEnvelope{
			Code:          code,
			Message:       message,
			Retryable:     retryable,
			CorrelationID: correlationID,
		},
	})
	if marshalErr != nil {
		body = []byte(`{"ok":false,"error":{"code":"store_corrupt","message":"management operation failed","retryable":true,"correlation_id":"` + correlationID + `"}}`)
		status = 500
	}
	headers := map[string]string{"Content-Type": "application/json; charset=utf-8"}
	for key, value := range extra {
		headers[key] = value
	}
	return Response{Status: status, StatusCode: status, ContentType: "application/json; charset=utf-8", Headers: headers, Body: body}
}

func managementError(err error) (domain.ErrorCode, string, bool, int) {
	if err == nil {
		return domain.CodeStoreCorrupt, "management operation failed", true, 500
	}
	var domainErr *domain.Error
	if errors.As(err, &domainErr) && domainErr != nil && domainErr.Code != "" {
		status := domainErr.HTTPStatus
		if status < 400 || status > 599 {
			status = defaultStatus(domainErr.Code)
		}
		message := domainErr.Message
		if message == "" {
			message = "management operation failed"
		}
		return domainErr.Code, message, domainErr.Retryable, status
	}
	return domain.CodeStoreCorrupt, "management operation failed", true, 500
}

func defaultStatus(code domain.ErrorCode) int {
	switch code {
	case domain.CodeConfigInvalid:
		return 400
	case domain.CodeRevisionConflict, domain.CodeRunInProgress, domain.CodeAccountBusy,
		domain.CodeAccountDisabled:
		return 409
	case domain.CodeAccountUnavailable:
		return 404
	case domain.CodeQuotaRefreshFailed:
		return 502
	default:
		return 500
	}
}

func domainError(code domain.ErrorCode, status int, retryable bool, message string) error {
	return &domain.Error{Code: code, HTTPStatus: status, Retryable: retryable, Message: message}
}

func methodError(allow string) error {
	return domainError(domain.CodeConfigInvalid, 405, false, "method is not allowed")
}

func managementEndpoint(value string) (string, bool) {
	value = trimPathDecorations(value)
	for _, prefix := range []string{"/v0/management/plugins/", "/management/plugins/", "/plugins/"} {
		if !strings.HasPrefix(value, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(value, prefix)
		separator := strings.IndexByte(remainder, '/')
		if separator <= 0 {
			return "", false
		}
		pluginID := remainder[:separator]
		if NormalizePluginID(pluginID) != pluginID {
			return "", false
		}
		endpoint := remainder[separator:]
		if endpoint == "/" || strings.Contains(endpoint, "//") {
			return "", false
		}
		return endpoint, true
	}
	return "", false
}

func resourceAssetPath(value string) (string, bool) {
	value = trimPathDecorations(value)
	if strings.HasPrefix(value, "/v0/resource/plugins/") || strings.HasPrefix(value, "/resource/plugins/") || strings.HasPrefix(value, "/plugins/") {
		prefix := "/plugins/"
		if strings.HasPrefix(value, "/v0/resource/plugins/") {
			prefix = "/v0/resource/plugins/"
		} else if strings.HasPrefix(value, "/resource/plugins/") {
			prefix = "/resource/plugins/"
		}
		remainder := strings.TrimPrefix(value, prefix)
		separator := strings.IndexByte(remainder, '/')
		if separator <= 0 {
			return "", false
		}
		pluginID := remainder[:separator]
		if NormalizePluginID(pluginID) != pluginID {
			return "", false
		}
		return remainder[separator:], true
	}
	if strings.HasPrefix(value, "/panel") || strings.HasPrefix(value, "/styles.css") || strings.HasPrefix(value, "/modules/") {
		return value, true
	}
	return "", false
}

func trimPathDecorations(value string) string {
	if index := strings.IndexByte(value, '?'); index >= 0 {
		value = value[:index]
	}
	if index := strings.IndexByte(value, '#'); index >= 0 {
		value = value[:index]
	}
	return value
}

func newCorrelationID() string {
	var raw [16]byte
	if _, err := cryptorand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return "request-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
}
