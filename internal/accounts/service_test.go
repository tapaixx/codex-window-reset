package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/tapaixx/codex-window-reset/internal/host"
)

type fakeHost struct {
	files    []host.AuthFile
	raw      json.RawMessage
	listErr  error
	getErr   error
	getIndex string
}

func (f *fakeHost) ListAuthFiles(context.Context) ([]host.AuthFile, error) {
	return f.files, f.listErr
}

func (f *fakeHost) GetAuth(_ context.Context, index string) (json.RawMessage, error) {
	f.getIndex = index
	return f.raw, f.getErr
}

func (*fakeHost) HTTPDo(context.Context, host.HTTPRequest) (host.HTTPResponse, error) {
	return host.HTTPResponse{}, nil
}

func (*fakeHost) Log(context.Context, string, string, map[string]any) {}

func TestAccountProjectionExposesAuthenticatedOperationalMetadataButNeverToken(t *testing.T) {
	raw := json.RawMessage(`{"access_token":"secret","account_id":"acct-upstream"}`)
	view, err := Project(AuthFile{AuthIndex: "7", Email: "alice@example.com", AccountID: "acct-upstream", Provider: "codex", UpdatedAt: "2026-09-11T01:02:03Z"}, raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(view)
	if bytes.Contains(encoded, []byte("secret")) {
		t.Fatalf("leaked: %s", encoded)
	}
	for _, forbidden := range []string{"access_token", "fixture-token"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("encoded account contains %q: %s", forbidden, encoded)
		}
	}
	for _, required := range []string{`"email":"alice@example.com"`, `"auth_index":"7"`, `"account_prefix":"acct-upstream"`, `"configuration_updated_at":"2026-09-11T01:02:03Z"`} {
		if !bytes.Contains(encoded, []byte(required)) {
			t.Fatalf("encoded account missing %s: %s", required, encoded)
		}
	}
}

func TestListFiltersCodexAndRetainsUnavailableAndDisabled(t *testing.T) {
	fake := &fakeHost{files: []host.AuthFile{
		{AuthIndex: "1", Email: "alice@example.com", Provider: "codex", PlanLabel: "Pro"},
		{AuthIndex: "2", Email: "bob@example.com", Type: "codex", Unavailable: true},
		{AuthIndex: "3", Email: "carol@example.com", Provider: "gemini"},
		{AuthIndex: "4", Email: "dave@example.com", Provider: "codex", Disabled: true},
	}}
	got, err := NewService(fake).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d accounts: %#v", len(got), got)
	}
	if !got[1].Unavailable || !got[2].Disabled {
		t.Fatalf("status flags were not retained: %#v", got)
	}
	if got[0].Key != "acct-1" || got[0].MaskedIdentity != "a***@example.com" || got[0].PlanLabel != "Pro" {
		t.Fatalf("projection = %#v", got[0])
	}
	if got[0].Fingerprint != fingerprint("acct-1") {
		t.Fatalf("fingerprint = %q, want %q", got[0].Fingerprint, fingerprint("acct-1"))
	}
	if got[0].Key == "alice@example.com" {
		t.Fatal("email became account key")
	}
}

func TestListRejectsContradictoryAuthoritativeType(t *testing.T) {
	fake := &fakeHost{files: []host.AuthFile{
		{AuthIndex: "1", Provider: "codex", Type: "anthropic"},
		{AuthIndex: "2", Provider: "anthropic", Type: "codex"},
	}}
	got, err := NewService(fake).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Key != "acct-2" {
		t.Fatalf("authoritative type filtering = %#v", got)
	}
}

func TestPlanLabelNeverFallsBackToIdentityMetadata(t *testing.T) {
	view, err := Project(AuthFile{
		AuthIndex: "7",
		Provider:  "codex",
		Name:      "alice@example.com",
		Label:     "alice@example.com",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if view.PlanLabel != "" || bytes.Contains(encoded, []byte("alice@example.com")) {
		t.Fatalf("identity metadata leaked through plan label: %s", encoded)
	}
}

func TestFindReturnsStableAccountByKey(t *testing.T) {
	fake := &fakeHost{files: []host.AuthFile{{AuthIndex: "7", Email: "alice@example.com", Provider: "codex"}}}
	service := NewService(fake)
	got, err := service.Find(context.Background(), "acct-7")
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "acct-7" || got.AuthIndex != "7" {
		t.Fatalf("found account = %#v", got)
	}
	if _, err := service.Find(context.Background(), "acct-missing"); err == nil || !strings.Contains(err.Error(), "account not found") {
		t.Fatalf("missing account error = %v", err)
	}
}

func TestAuthMaterialParsesOnlyAtCallBoundary(t *testing.T) {
	fake := &fakeHost{raw: json.RawMessage(`{"access_token":"fixture-token","account_id":"acct-upstream"}`)}
	account := Account{Key: "acct-7", AuthIndex: "7"}
	material, err := AuthMaterial(context.Background(), fake, account)
	if err != nil {
		t.Fatal(err)
	}
	if material.AccessToken != "fixture-token" || material.AccountID != "acct-upstream" || fake.getIndex != "7" {
		t.Fatalf("material = %#v, get index = %q", material, fake.getIndex)
	}
	encoded, _ := json.Marshal(material)
	if len(encoded) != 2 || string(encoded) != "{}" {
		t.Fatalf("material JSON = %s", encoded)
	}
}

func TestAuthMaterialReturnsSanitizedCredentialError(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"account_id":"acct-upstream"}`),
		json.RawMessage(`{"access_token":"fixture-token"}`),
		json.RawMessage(`{"access_token":"fixture-token","account_id":"acct-upstream",}`),
	} {
		fake := &fakeHost{raw: raw}
		_, err := AuthMaterial(context.Background(), fake, Account{AuthIndex: "7"})
		if err == nil || !strings.Contains(err.Error(), "credential_error") {
			t.Fatalf("raw %s: error = %v", raw, err)
		}
		if strings.Contains(err.Error(), "fixture-token") || strings.Contains(err.Error(), "acct-upstream") || strings.Contains(err.Error(), "access_token") {
			t.Fatalf("raw credential leaked in error %q", err)
		}
	}

	fake := &fakeHost{getErr: errors.New(`host returned fixture-token`)}
	_, err := AuthMaterial(context.Background(), fake, Account{AuthIndex: "7"})
	if err == nil || !strings.Contains(err.Error(), "credential_error") || strings.Contains(err.Error(), "fixture-token") {
		t.Fatalf("host error was not sanitized: %v", err)
	}
}

func TestSecretBearingFieldsCarryJSONDashTags(t *testing.T) {
	for typ, field := range map[string]string{"Material": "AccessToken"} {
		var tag reflect.StructTag
		switch typ {
		case "Account":
			typOf := reflect.TypeOf(Account{})
			fieldOf, ok := typOf.FieldByName(field)
			if !ok {
				t.Fatalf("%s.%s missing", typ, field)
			}
			tag = fieldOf.Tag
		case "Material":
			typOf := reflect.TypeOf(Material{})
			fieldOf, ok := typOf.FieldByName(field)
			if !ok {
				t.Fatalf("%s.%s missing", typ, field)
			}
			tag = fieldOf.Tag
		}
		if got := tag.Get("json"); got != "-" {
			t.Fatalf("%s.%s json tag = %q, want -", typ, field, got)
		}
	}
}

func TestAccountAndErrorJSONNeverExposeIdentityOrCredentialFixtures(t *testing.T) {
	account := Account{Key: "acct-7", AuthIndex: "7", MaskedIdentity: "a***@example.com"}
	encodedAccount, err := json.Marshal(account)
	if err != nil {
		t.Fatal(err)
	}
	err = &credentialError{}
	encodedError, marshalErr := json.Marshal(err)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	joined := string(encodedAccount) + string(encodedError)
	for _, forbidden := range []string{"alice@example.com", "access_token", "fixture-token", "raw credential"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("JSON contains forbidden %q: %s", forbidden, joined)
		}
	}
}

// The host's account and id fields carry the operator's address — account is
// the address itself and id embeds it in a filename. Feeding either into the
// account prefix put an address into a field the panel masks as an opaque
// token, and the prefix mask reveals a head and a tail: "alex_***.com" beside
// a masked "a***@qq.com" reconstructs the whole address.
func TestAccountPrefixNeverCarriesTheOperatorAddress(t *testing.T) {
	raw := json.RawMessage(`{"access_token":"secret"}`)
	file := AuthFile{
		AuthIndex: "7",
		Email:     "alex_nnn@qq.com",
		Account:   "alex_nnn@qq.com",
		ID:        "codex-03c103fb-alex_nnn@qq.com-team.json",
		Provider:  "codex",
	}
	view, err := Project(file, raw)
	if err != nil {
		t.Fatal(err)
	}
	if view.AccountPrefix != "" {
		t.Fatalf("account prefix = %q, want empty when no account identifier is available", view.AccountPrefix)
	}
	encoded, _ := json.Marshal(view)
	for _, leak := range []string{"alex_nnn", "qq.com", "alex_"} {
		if bytes.Contains(encoded, []byte(`"account_prefix"`)) && bytes.Contains(encoded, []byte(leak)) {
			// The email itself is a declared field; only the prefix must be clean.
			if bytes.Contains([]byte(view.AccountPrefix), []byte(leak)) {
				t.Fatalf("account prefix leaked %q: %s", leak, encoded)
			}
		}
	}
}

func TestAccountPrefixUsesTheIDTokenAccountIdentifier(t *testing.T) {
	raw := json.RawMessage(`{"access_token":"secret"}`)
	file := AuthFile{AuthIndex: "7", Email: "alex_nnn@qq.com", Account: "alex_nnn@qq.com", AccountID: "541773e7-d581-469c-87fe-eabbca968d01", Provider: "codex"}
	view, err := Project(file, raw)
	if err != nil {
		t.Fatal(err)
	}
	if view.AccountPrefix != "541773e7-d581-46" {
		t.Fatalf("account prefix = %q, want the truncated account identifier", view.AccountPrefix)
	}
}
