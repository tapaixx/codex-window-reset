package domain

import "errors"

// ErrorCode is the stable machine-readable identifier for a domain failure.
type ErrorCode string

const (
	CodeConfigInvalid      ErrorCode = "config_invalid"
	CodeRevisionConflict   ErrorCode = "revision_conflict"
	CodeRunInProgress      ErrorCode = "run_in_progress"
	CodeAccountBusy        ErrorCode = "account_busy"
	CodeAccountDisabled    ErrorCode = "account_disabled"
	CodeAccountUnavailable ErrorCode = "account_unavailable"
	CodeGuardrailHold      ErrorCode = "guardrail_hold"
	CodeQuotaRefreshFailed ErrorCode = "quota_refresh_failed"
	CodeProbeFailed        ErrorCode = "probe_failed"
	CodeWindowUnverified   ErrorCode = "window_unverified"
	CodeStoreCorrupt       ErrorCode = "store_corrupt"
)

// Error is a sanitized, transport-independent domain error. Callers may map
// its fields to an HTTP error envelope without exposing upstream details.
type Error struct {
	Code          ErrorCode `json:"code"`
	Message       string    `json:"message"`
	Retryable     bool      `json:"retryable"`
	HTTPStatus    int       `json:"http_status,omitempty"`
	CorrelationID string    `json:"correlation_id,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// CodeOf returns the stable code carried by a domain error. It returns an
// empty code for ordinary errors or nil.
func CodeOf(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var domainErr *Error
	if errors.As(err, &domainErr) && domainErr != nil {
		return domainErr.Code
	}
	return ""
}
