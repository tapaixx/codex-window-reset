// Package host contains the narrow, typed boundary to the CLIProxyAPI host.
//
// Host-owned credentials are intentionally not represented by this package's
// public JSON projections.  Authentication material is returned as bytes only
// by GetAuth and is consumed by an upstream operation immediately afterwards.
package host

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Caller is the host callback supplied by the CLIProxyAPI plugin ABI.  The
// operation names are part of the boundary contract and are deliberately
// kept in the adapter rather than scattered through callers.
type Caller func(context.Context, string, any, any) error

// API is the capability surface consumed by the rest of the plugin.
type API interface {
	ListAuthFiles(context.Context) ([]AuthFile, error)
	GetAuth(context.Context, string) (json.RawMessage, error)
	HTTPDo(context.Context, HTTPRequest) (HTTPResponse, error)
	Log(context.Context, string, string, map[string]any)
}

// AuthFile is the safe metadata returned by host.auth.list.  Email and
// AuthIndex are retained only inside the host/account boundary: neither is
// included when this type is JSON encoded.
type AuthFile struct {
	ID             string `json:"id,omitempty"`
	AuthIndex      string `json:"-"`
	Name           string `json:"name,omitempty"`
	Email          string `json:"-"`
	Provider       string `json:"provider,omitempty"`
	Type           string `json:"type,omitempty"`
	Label          string `json:"label,omitempty"`
	Status         string `json:"status,omitempty"`
	StatusMessage  string `json:"status_message,omitempty"`
	CredentialType string `json:"credential_type,omitempty"`
	Kind           string `json:"kind,omitempty"`
	AccountType    string `json:"account_type,omitempty"`
	Account        string `json:"account,omitempty"`
	Plan           string `json:"-"`
	PlanType       string `json:"plan_type,omitempty"`
	PlanLabel      string `json:"plan_label,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
	Disabled       bool   `json:"disabled"`
	Unavailable    bool   `json:"unavailable"`
}

// UnmarshalJSON accepts the snake-case and Go-style casing emitted by host
// versions in the wild.  It also accepts auth_index/index aliases used by
// older host adapters.  Only non-secret account metadata is copied.
func (f *AuthFile) UnmarshalJSON(data []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	*f = AuthFile{
		ID:             firstString(object, "id", "ID"),
		AuthIndex:      firstString(object, "auth_index", "authIndex", "AuthIndex", "index", "Index"),
		Name:           firstString(object, "name", "Name"),
		Email:          firstString(object, "email", "Email"),
		Provider:       firstString(object, "provider", "Provider"),
		Type:           firstString(object, "type", "Type"),
		Label:          firstString(object, "label", "Label"),
		Status:         firstString(object, "status", "Status"),
		StatusMessage:  firstString(object, "status_message", "statusMessage", "StatusMessage"),
		CredentialType: firstString(object, "credential_type", "credentialType", "CredentialType"),
		Kind:           firstString(object, "kind", "Kind"),
		AccountType:    firstString(object, "account_type", "accountType", "AccountType"),
		Account:        firstString(object, "account", "Account"),
		Plan:           firstString(object, "plan", "Plan"),
		PlanType:       nestedPlanType(object),
		PlanLabel:      firstString(object, "plan_label", "planLabel", "PlanLabel", "plan"),
		UpdatedAt:      firstString(object, "updated_at", "updatedAt", "UpdatedAt", "modified_at", "modifiedAt", "ModifiedAt"),
		Disabled:       firstBool(object, "disabled", "Disabled"),
		Unavailable:    firstBool(object, "unavailable", "Unavailable"),
	}
	return nil
}

// nestedPlanType reads the subscription tier out of the OAuth id_token, which
// is where the host actually carries it. The sibling account_type field is the
// credential type ("oauth") and names no tier at all.
func nestedPlanType(object map[string]json.RawMessage) string {
	for _, key := range []string{"id_token", "idToken", "IDToken"} {
		raw, ok := object[key]
		if !ok {
			continue
		}
		var token map[string]json.RawMessage
		if err := json.Unmarshal(raw, &token); err != nil {
			continue
		}
		if value := firstString(token, "plan_type", "planType", "PlanType", "plan"); value != "" {
			return value
		}
	}
	return ""
}

// HTTPRequest is a host-mediated outbound HTTP request.  Body is encoded as
// base64 by encoding/json, matching the host ABI's byte-buffer convention.
type HTTPRequest struct {
	Method  string              `json:"method"`
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    []byte              `json:"body,omitempty"`
}

// HTTPResponse is a host-mediated outbound HTTP response.  Its custom
// decoder accepts both snake_case and Go-style field casing, and accepts
// either a map of header slices or a map of scalar header values.
type HTTPResponse struct {
	StatusCode int                 `json:"status_code"`
	Headers    map[string][]string `json:"headers,omitempty"`
	Body       []byte              `json:"body,omitempty"`
}

func (r *HTTPResponse) UnmarshalJSON(data []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}

	status, err := firstInt(object, "status_code", "statusCode", "StatusCode")
	if err != nil {
		return fmt.Errorf("invalid host HTTP status: %w", err)
	}
	headers, err := decodeHeaders(firstRaw(object, "headers", "Headers"))
	if err != nil {
		return fmt.Errorf("invalid host HTTP headers: %w", err)
	}
	body, err := decodeBody(firstRaw(object, "body", "Body"))
	if err != nil {
		return fmt.Errorf("invalid host HTTP body: %w", err)
	}
	*r = HTTPResponse{StatusCode: status, Headers: headers, Body: body}
	return nil
}

// LogRequest is the typed payload sent to host.log.  Log is intentionally
// fire-and-forget because logging must never interfere with an operation.
type LogRequest struct {
	CorrelationID string         `json:"correlation_id,omitempty"`
	Message       string         `json:"message,omitempty"`
	Fields        map[string]any `json:"fields,omitempty"`
}

func firstRaw(object map[string]json.RawMessage, names ...string) json.RawMessage {
	for _, name := range names {
		if value, ok := object[name]; ok {
			return value
		}
	}
	return nil
}

func firstString(object map[string]json.RawMessage, names ...string) string {
	value := firstRaw(object, names...)
	if len(value) == 0 {
		return ""
	}
	var result string
	if json.Unmarshal(value, &result) == nil {
		return result
	}
	return ""
}

func firstBool(object map[string]json.RawMessage, names ...string) bool {
	value := firstRaw(object, names...)
	if len(value) == 0 {
		return false
	}
	var result bool
	if json.Unmarshal(value, &result) == nil {
		return result
	}
	var text string
	if json.Unmarshal(value, &text) == nil {
		return text == "1" || text == "true" || text == "TRUE" || text == "True"
	}
	return false
}

func firstInt(object map[string]json.RawMessage, names ...string) (int, error) {
	value := firstRaw(object, names...)
	if len(value) == 0 {
		return 0, nil
	}
	var result int
	if err := json.Unmarshal(value, &result); err == nil {
		return result, nil
	}
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return 0, err
	}
	var parsed int
	if _, err := fmt.Sscan(text, &parsed); err != nil {
		return 0, err
	}
	return parsed, nil
}

func decodeHeaders(value json.RawMessage) (map[string][]string, error) {
	if len(value) == 0 || string(value) == "null" {
		return nil, nil
	}
	var slices map[string][]string
	if err := json.Unmarshal(value, &slices); err == nil {
		return slices, nil
	}
	var scalars map[string]string
	if err := json.Unmarshal(value, &scalars); err != nil {
		return nil, err
	}
	result := make(map[string][]string, len(scalars))
	for key, item := range scalars {
		result[key] = []string{item}
	}
	return result, nil
}

func decodeBody(value json.RawMessage) ([]byte, error) {
	if len(value) == 0 || string(value) == "null" {
		return nil, nil
	}
	var encoded string
	if err := json.Unmarshal(value, &encoded); err == nil {
		decoded, decodeErr := base64.StdEncoding.DecodeString(encoded)
		if decodeErr == nil {
			return decoded, nil
		}
		// A host test double may provide a literal body instead of the ABI's
		// base64 representation.  Preserve that useful compatibility while
		// still preferring base64 whenever it is valid.
		return []byte(encoded), nil
	}
	var bytesValue []byte
	if err := json.Unmarshal(value, &bytesValue); err != nil {
		return nil, err
	}
	return bytesValue, nil
}
