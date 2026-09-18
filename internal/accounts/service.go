// Package accounts discovers Codex Accounts through the host adapter and
// exposes only safe identity projections to management callers.
package accounts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/tapaixx/codex-window-reset/internal/host"
)

// AuthFile is retained as an alias so account projection tests and consumers
// can use the host metadata type from this package without duplicating it.
type AuthFile = host.AuthFile

// Account is the authenticated management projection. It contains the same
// non-secret operational identity metadata shown by CLIProxyAPI's own panel,
// but never credential material.
type Account struct {
	Key                 string `json:"account_key"`
	AuthIndex           string `json:"auth_index"`
	Email               string `json:"email,omitempty"`
	AccountPrefix       string `json:"account_prefix,omitempty"`
	ConfigurationUpdate string `json:"configuration_updated_at,omitempty"`
	MaskedIdentity      string `json:"masked_identity"`
	PlanLabel           string `json:"plan_label,omitempty"`
	Disabled            bool   `json:"disabled"`
	Unavailable         bool   `json:"unavailable"`
	Fingerprint         string `json:"fingerprint"`
}

// Material is short-lived upstream credential material.  It must not cross a
// JSON or persistence boundary.
type Material struct {
	AccessToken string `json:"-"`
	AccountID   string `json:"-"`
}

// Service discovers account metadata through host.API.  It deliberately has
// no credential cache: availability and disabled state are host-owned and are
// read again whenever account discovery is requested.
type Service struct {
	api host.API
}

var (
	errNilAPI          = errors.New("account host API is nil")
	errAccountNotFound = errors.New("account not found")
)

// CredentialError is the only error returned when credential retrieval or
// parsing fails.  It intentionally contains no wrapped host error, raw JSON,
// token, or account identity.
type CredentialError struct{}

func (*CredentialError) Error() string { return "credential_error" }

// credentialError is kept as a package-local alias for tests and for code
// that wants a concrete sanitized error without exporting implementation
// details.
type credentialError = CredentialError

// NewService constructs an account discovery service.
func NewService(api host.API) *Service { return &Service{api: api} }

// New is a short constructor alias.
func New(api host.API) *Service { return NewService(api) }

// List discovers and safely projects all Codex credentials.  Host-unavailable
// and disabled accounts remain in the result so the Operator can see and
// manage their persistent selection state.
func (s *Service) List(ctx context.Context) ([]Account, error) {
	if s == nil || s.api == nil {
		return nil, errNilAPI
	}
	files, err := s.api.ListAuthFiles(ctx)
	if err != nil {
		return nil, err
	}
	accounts := make([]Account, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if !isCodex(file) || strings.TrimSpace(file.AuthIndex) == "" {
			continue
		}
		account, projectErr := Project(file, nil)
		if projectErr != nil {
			// A malformed host entry cannot be addressed safely.  Ignore it
			// while retaining all valid entries for diagnostics.
			continue
		}
		if _, duplicate := seen[account.Key]; duplicate {
			continue
		}
		seen[account.Key] = struct{}{}
		accounts = append(accounts, account)
	}
	return accounts, nil
}

// Find discovers the current account list and returns one stable key match.
func (s *Service) Find(ctx context.Context, key string) (Account, error) {
	accounts, err := s.List(ctx)
	if err != nil {
		return Account{}, err
	}
	for _, account := range accounts {
		if account.Key == key {
			return account, nil
		}
	}
	return Account{}, errAccountNotFound
}

// Project converts host account metadata into a safe management projection.
// raw is accepted for compatibility with host list implementations that
// provide a companion document; it is deliberately ignored so credentials
// can never become part of the returned Account value.
func Project(file AuthFile, raw json.RawMessage) (Account, error) {
	_ = raw
	index := strings.TrimSpace(file.AuthIndex)
	if index == "" {
		return Account{}, &CredentialError{}
	}
	key := accountKey(index)
	return Account{
		Key:                 key,
		AuthIndex:           index,
		Email:               strings.TrimSpace(file.Email),
		AccountPrefix:       accountPrefix(file),
		ConfigurationUpdate: strings.TrimSpace(file.UpdatedAt),
		MaskedIdentity:      maskEmail(file.Email),
		PlanLabel:           planLabel(file),
		Disabled:            file.Disabled,
		Unavailable:         file.Unavailable,
		Fingerprint:         fingerprint(key),
	}, nil
}

// accountPrefix returns an opaque account identifier and nothing else.
//
// Account and ID are deliberately absent. Both carry the operator's email —
// Account is the address itself and ID embeds it in a filename — so using them
// here put an address into a field the panel masks as an opaque token. The
// prefix mask reveals a head and a tail, which together with the masked email
// beside it reconstructed the address and defeated the masking entirely.
func accountPrefix(file AuthFile) string {
	value := strings.TrimSpace(file.AccountID)
	if value == "" {
		return ""
	}
	if len(value) > 16 {
		return value[:16]
	}
	return value
}

// AuthMaterial is the sole credential extraction point.  Callers should
// invoke it immediately before an upstream request and discard the returned
// value as soon as that request completes.
func AuthMaterial(ctx context.Context, api host.API, account Account) (Material, error) {
	if api == nil || strings.TrimSpace(account.AuthIndex) == "" {
		return Material{}, &CredentialError{}
	}
	raw, err := api.GetAuth(ctx, account.AuthIndex)
	if err != nil {
		return Material{}, &CredentialError{}
	}
	token, accountID, ok := parseCredential(raw)
	if !ok {
		return Material{}, &CredentialError{}
	}
	return Material{AccessToken: token, AccountID: accountID}, nil
}

func accountKey(authIndex string) string { return "acct-" + authIndex }

func fingerprint(key string) string {
	digest := sha256.Sum256([]byte(key))
	return hex.EncodeToString(digest[:])[:12]
}

func maskEmail(email string) string {
	email = strings.TrimSpace(email)
	at := strings.LastIndexByte(email, '@')
	if at <= 0 || at == len(email)-1 {
		return "***"
	}
	local := email[:at]
	first, _ := utf8.DecodeRuneInString(local)
	if first == utf8.RuneError {
		return "***"
	}
	return string(first) + "***@" + email[at+1:]
}

func isCodex(file AuthFile) bool {
	// Prefer the host's explicit credential type over the progressively more
	// generic provider/kind aliases. This prevents a contradictory entry such
	// as type=anthropic, provider=codex from entering Codex discovery.
	for _, value := range []string{file.Type, file.CredentialType, file.Provider, file.Kind} {
		if value = strings.TrimSpace(value); value != "" {
			return strings.EqualFold(value, "codex")
		}
	}
	return false
}

func planLabel(file AuthFile) string {
	// Only plan-specific host metadata is safe for the management projection.
	// Generic Label and Name values may be credential filenames or identities.
	// AccountType is deliberately absent: the host sets it to the credential
	// type ("oauth"), so using it as a fallback labelled every subscription
	// tier "oauth". The tier lives in the OAuth id_token as plan_type.
	for _, value := range []string{file.PlanLabel, file.Plan, file.PlanType} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func parseCredential(raw json.RawMessage) (string, string, bool) {
	if len(raw) == 0 {
		return "", "", false
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", "", false
	}
	token := findString(object, "access_token", "accessToken", "AccessToken", "oauth_token", "oauthToken", "token")
	accountID := findString(object, "account_id", "accountId", "AccountID", "chatgpt_account_id", "chatgptAccountId")
	for _, nestedName := range []string{"tokens", "oauth", "credential", "auth", "data"} {
		nestedRaw := firstRaw(object, nestedName)
		if len(nestedRaw) == 0 {
			continue
		}
		var nested map[string]json.RawMessage
		if json.Unmarshal(nestedRaw, &nested) != nil {
			continue
		}
		if token == "" {
			token = findString(nested, "access_token", "accessToken", "AccessToken", "oauth_token", "oauthToken", "token")
		}
		if accountID == "" {
			accountID = findString(nested, "account_id", "accountId", "AccountID", "chatgpt_account_id", "chatgptAccountId")
		}
	}
	if token == "" || accountID == "" {
		return "", "", false
	}
	return token, accountID, true
}

func firstRaw(object map[string]json.RawMessage, names ...string) json.RawMessage {
	for _, name := range names {
		if value, ok := object[name]; ok {
			return value
		}
	}
	return nil
}

func findString(object map[string]json.RawMessage, names ...string) string {
	for _, name := range names {
		value := firstRaw(object, name)
		if len(value) == 0 {
			continue
		}
		var result string
		if json.Unmarshal(value, &result) == nil && strings.TrimSpace(result) != "" {
			return result
		}
	}
	return ""
}
