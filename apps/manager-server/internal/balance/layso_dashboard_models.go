package balance

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// Observed 2026-11-04: remaining wallet lives on the logged-in dashboard models
// surface as Account balance → Credits:<decimal>. /billing is recharge history;
// /dashboard/usage Total spend is cumulative spend, not remaining.
const LaysoDashboardModelsRuleV1 = "layso-dashboard-models-account-balance-v1"

var (
	laysoAccountBalanceCredits = regexp.MustCompile(`(?is)Account balance</span>.*?aria-label="Credits:(-?(?:0|[1-9][0-9]{0,59})(?:\.[0-9]{1,30})?)"`)
	laysoAvatarAlt             = regexp.MustCompile(`(?i)<img[^>]*\balt="([^"]{1,128})"`)
	laysoCreditsLabel          = regexp.MustCompile(`^Credits:(-?(?:0|[1-9][0-9]{0,59})(?:\.[0-9]{1,30})?)$`)
)

// ParseLaysoDashboardModelsHTML reads BrowserSkill get-html JSON (or raw HTML).
// Amount comes from the Account balance card aria-label only — never from
// Cumulative usage / Total spend / recharge history Credits:* values.
func ParseLaysoDashboardModelsHTML(raw []byte) (WebPage, error) {
	html, err := laysoHTMLBody(raw)
	if err != nil {
		return WebPage{}, err
	}
	if strings.Contains(html, "Sign in / Sign up") && !strings.Contains(html, "Account balance") {
		return WebPage{}, errors.New("login surface")
	}
	m := laysoAccountBalanceCredits.FindStringSubmatch(html)
	if m == nil {
		return WebPage{}, errors.New("account balance credits missing")
	}
	amount := m[1]
	if !webMoney.MatchString(amount) {
		return WebPage{}, errors.New("invalid credits lexeme")
	}
	// Guard: the Credits aria-label must belong to the Account balance card,
	// not a later Cumulative usage block after the heading.
	idx := strings.Index(html, m[0])
	if idx < 0 {
		return WebPage{}, errors.New("account balance credits missing")
	}
	labelAt := idx + strings.Index(m[0], `aria-label="Credits:`)
	cumAfter := strings.Index(html[idx:], "Cumulative usage")
	if cumAfter >= 0 && idx+cumAfter < labelAt {
		return WebPage{}, errors.New("credits bound to cumulative usage")
	}
	// Site chrome includes alt="Logo"; the logged-in avatar alt is the account id
	// (observed 2026-11-04: h7w8). Never take the first img blindly.
	alt := ""
	for _, am := range laysoAvatarAlt.FindAllStringSubmatch(html, -1) {
		candidate := am[1]
		if candidate == "" || strings.EqualFold(candidate, "Logo") || !sessionToken.MatchString(candidate) {
			continue
		}
		alt = candidate
		break
	}
	if alt == "" {
		return WebPage{}, errors.New("account avatar missing")
	}
	// Observed console chrome: sidebar section label "Personal". User ID / role
	// text from Personal Center are not present on the models wallet surface.
	if !strings.Contains(html, "Personal</div>") && !strings.Contains(html, ">Personal<") {
		return WebPage{}, errors.New("personal console chrome missing")
	}
	tenant := "Personal"
	role := "Personal"
	return WebPage{
		URL:              LaysoBillingPage,
		AccountID:        alt,
		TenantID:         tenant,
		Role:             role,
		IdentityVerified: true,
		RoleVerified:     true,
		Currency:         "USD",
		Amount:           &amount,
	}, nil
}

func laysoHTMLBody(raw []byte) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > maxResponseBodySize {
		return "", errors.New("html budget")
	}
	if raw[0] == '{' {
		fields, ok := checklistNumbers(raw)
		if !ok {
			return "", errors.New("invalid html envelope")
		}
		htmlRaw, has := fields["html"]
		if !has {
			return "", errors.New("html field missing")
		}
		var html string
		if json.Unmarshal(htmlRaw, &html) != nil || html == "" {
			return "", errors.New("html field invalid")
		}
		return html, nil
	}
	return string(raw), nil
}

// Kept for tests that assert the Credits label shape without HTML.
func laysoCreditsAmount(label string) (string, bool) {
	m := laysoCreditsLabel.FindStringSubmatch(strings.TrimSpace(label))
	if m == nil {
		return "", false
	}
	return m[1], webMoney.MatchString(m[1])
}
