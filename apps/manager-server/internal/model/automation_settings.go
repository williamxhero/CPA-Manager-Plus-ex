package model

// AutomationSettings stores UI-managed account-processing-policy overrides.
// Nil fields mean "not configured in DB" and fall back to startup config unless
// the corresponding environment variable explicitly locks the value.
//
// JSON field names use the business-facing account-processing-policy vocabulary
// (auth issue queue / auth issue auto-disable) to match the HTTP route and UI.
// The underlying config.json keys and environment variables keep their
// original names, which are surfaced separately.
// Removed and unknown fields are retained by the settings repository, not
// interpreted here or mapped to any per-credential policy.
type AutomationSettings struct {
	AccountActionsEnabled     *bool `json:"authIssueQueueEnabled,omitempty"`
	AccountActionsAutoDisable *bool `json:"authIssueAutoDisableEnabled,omitempty"`
	UpdatedAtMS               int64 `json:"updatedAtMs,omitempty"`
}

// AutomationSettingsPreservation is a database-only migration receipt. The
// original document may contain private unknown fields: never expose or log it.
// A retained global flag is historical data, not per-subject policy authority.
type AutomationSettingsPreservation struct {
	Version             int               `json:"version"`
	SourceKey           string            `json:"sourceKey"`
	Strategy            string            `json:"strategy"`
	PerSubjectMapping   string            `json:"perSubjectMapping"`
	Fields              map[string]string `json:"fields"`
	OriginalValue       string            `json:"originalValue"`
	OriginalUpdatedAtMS int64             `json:"originalUpdatedAtMs"`
	PreservedAtMS       int64             `json:"preservedAtMs"`
}
