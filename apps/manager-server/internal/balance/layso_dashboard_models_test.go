package balance_test

import (
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
)

func TestParseLaysoDashboardModelsAccountBalance(t *testing.T) {
	html := `<div>Personal</div><ul></ul>` +
		`<img alt="Logo"/><img alt="h7w8" data-slot="avatar-image" class="x"/>` +
		`<span>Cumulative usage</span><span aria-label="Credits:317.68"><span aria-hidden="true">Credits:317.68</span></span>` +
		`<article><span>Account balance</span><div class="analytics-stat-value">` +
		`<span aria-label="Credits:46.92"><span aria-hidden="true">Credits:46.92</span></span></div>` +
		`<div>Available for all model calls</div></article>`
	page, err := balance.ParseLaysoDashboardModelsHTML([]byte(`{"html":` + quoteJSON(html) + `}`))
	if err != nil {
		t.Fatal(err)
	}
	if page.URL != balance.LaysoBillingPage || page.AccountID != "h7w8" || page.TenantID != "Personal" || page.Role != "Personal" {
		t.Fatalf("identity: %+v", page)
	}
	if page.Amount == nil || *page.Amount != "46.92" || page.Currency != "USD" || !page.IdentityVerified || !page.RoleVerified {
		t.Fatalf("wallet: %+v", page)
	}
}

func TestParseLaysoDashboardModelsRejectsCumulativeAsWallet(t *testing.T) {
	html := `<div>Personal</div><img alt="h7w8"/>` +
		`<span>Cumulative usage</span><span>Account balance</span>` +
		`<span aria-label="Credits:317.68"></span>`
	// Force the only Credits aria-label to sit after a later Cumulative marker
	// by placing Cumulative after Account balance heading but before label.
	html = `<div>Personal</div><img alt="h7w8"/><span>Account balance</span>` +
		`<span>Cumulative usage</span><span aria-label="Credits:317.68"></span>`
	if _, err := balance.ParseLaysoDashboardModelsHTML([]byte(html)); err == nil {
		t.Fatal("accepted cumulative credits as account balance")
	}
}

func TestParseLaysoDashboardModelsRejectsLogin(t *testing.T) {
	html := `<dialog>Sign in / Sign up</dialog><form>Password</form>`
	if _, err := balance.ParseLaysoDashboardModelsHTML([]byte(html)); err == nil {
		t.Fatal("login surface accepted")
	}
}

func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
