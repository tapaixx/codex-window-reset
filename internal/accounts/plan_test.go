package accounts

import (
	"encoding/json"
	"testing"
)

// Shape captured from a live CLIProxyAPI /v0/management/auth-files response:
// account_type carries the credential type, and the subscription tier is
// nested in the OAuth id_token.
const livePlanAuthFile = `{
  "auth_index": "58baed010f75dd6e",
  "account": "user@example.com",
  "account_type": "oauth",
  "provider": "codex",
  "type": "codex",
  "status": "active",
  "email": "user@example.com",
  "id_token": {
    "chatgpt_account_id": "541773e7-d581-469c-87fe-eabbca968d01",
    "plan_type": "team"
  },
  "disabled": false,
  "unavailable": false
}`

func TestPlanLabelReadsTheIDTokenTierNotTheCredentialType(t *testing.T) {
	var file AuthFile
	if err := json.Unmarshal([]byte(livePlanAuthFile), &file); err != nil {
		t.Fatal(err)
	}
	if file.PlanType != "team" {
		t.Fatalf("plan type=%q, want team", file.PlanType)
	}
	if got := planLabel(file); got != "team" {
		t.Fatalf("plan label=%q, want team (account_type %q must never be used as a tier)", got, file.AccountType)
	}
}

func TestPlanLabelPrefersAnExplicitHostLabel(t *testing.T) {
	file := AuthFile{PlanLabel: "Pro 20x", PlanType: "pro", AccountType: "oauth"}
	if got := planLabel(file); got != "Pro 20x" {
		t.Fatalf("plan label=%q, want the explicit host label", got)
	}
}

func TestPlanLabelIsEmptyWhenNoTierIsKnown(t *testing.T) {
	file := AuthFile{AccountType: "oauth"}
	if got := planLabel(file); got != "" {
		t.Fatalf("plan label=%q, want empty rather than the credential type", got)
	}
}
