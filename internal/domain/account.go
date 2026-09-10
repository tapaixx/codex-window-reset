package domain

// AccountIdentity is the safe identity projection returned to management
// clients. Credentials and host-owned identifiers are intentionally absent.
type AccountIdentity struct {
	AccountKey     string `json:"account_key"`
	MaskedIdentity string `json:"masked_identity"`
	PlanLabel      string `json:"plan_label,omitempty"`
}

// AccountProjection and IdentityProjection are descriptive aliases for
// callers that name the same safe management projection differently.
type AccountProjection = AccountIdentity
type IdentityProjection = AccountIdentity
