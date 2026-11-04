package balance

import "errors"

const LaysoBillingWebV1 = "layso_billing_web_v1"

// LaysoBillingPage is the observed logged-in wallet surface (Account balance).
// /billing is recharge history; /dashboard/usage Total spend is cumulative spend.
const LaysoBillingPage = "https://layso.ai/dashboard/models"

// WebConfig registers expected read-only source authority. These declarations
// are not proof of browser/profile mapping, a login, or account/key ownership.
// No parsing contract is available until the real page has been observed.
type WebConfig struct {
	Site          string             `json:"site"`
	AccountID     string             `json:"accountId"`
	TenantID      string             `json:"tenantId"`
	Role          string             `json:"role"`
	ExecutionUser string             `json:"executionUser"`
	Browser       WebBrowserIdentity `json:"browser"`
	Currency      string             `json:"currency"`
	ParsingRule   string             `json:"parsingRule"`
}

type WebBrowserIdentity struct {
	Family     string `json:"family"`
	Profile    string `json:"profile"`
	InstanceID string `json:"instanceId"`
}

// ValidateWebContract is separate from the API transport's URL/SSRF policy.
// It admits one fixed normal billing page, no caller-supplied executable code,
// selectors, navigation rules, or assertion that an unobserved parser works.
func ValidateWebContract(c *WebConfig) error {
	if c == nil || c.Site != "layso" || c.Currency != "USD" || c.Browser.Family != "microsoft-edge" || c.Browser.Profile != "Profile 1" {
		return errors.New("invalid web source contract")
	}
	for _, identity := range []string{c.AccountID, c.TenantID, c.Role, c.ExecutionUser, c.Browser.InstanceID, c.ParsingRule} {
		if len(identity) == 0 || len(identity) > 128 {
			return errors.New("invalid web source identity")
		}
		for _, ch := range identity {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' || ch == '.' || ch == ':' || ch == '@') {
				return errors.New("invalid web source identity")
			}
		}
	}
	return nil
}
