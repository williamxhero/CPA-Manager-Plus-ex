package balance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// NewAPIBrowserSessionV1 is the conter_proj browser_session seam for NewAPI-
// shaped hosts that expose account quota only through an already-logged-in
// same-origin browser session (e.g. Top-API). Cookies never leave the browser;
// CPA observes native newapi_quota only — never a UI ×7 display rate.
// Distinct from layso_billing_web_v1: this adapter may evaluate a fixed same-
// origin fetch to /api/user/self. LAYSO remains observe-only.
const NewAPIBrowserSessionV1 = "newapi-browser-session-v1"
const NewAPIBrowserSessionAuthV1 = "browser-session-cookie-v1"
const NewAPIBrowserSessionPath = "/api/user/self"
const NewAPIBrowserSessionDashboard = "/dashboard"

// BrowserSessionConfig pins origin + dashboard + BrowserSkill instance. It is
// not proof of login, key ownership, or shared-wallet membership.
type BrowserSessionConfig struct {
	Origin            string `json:"origin"`
	DashboardPath     string `json:"dashboardPath"`
	BrowserInstanceID string `json:"browserInstanceId"`
}

var browserSessionOrigins = map[string]struct{}{
	"https://api-top.com": {},
}

func ValidateBrowserSessionContract(c *BrowserSessionConfig, newAPI *NewAPIConfig) error {
	if c == nil {
		return errors.New("browser session contract required")
	}
	if ValidateNewAPIContract(NewAPIBrowserSessionV1, newAPI) != nil {
		return errors.New("invalid newapi contract")
	}
	u, err := url.Parse(c.Origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil || u.Opaque != "" || strings.Contains(c.Origin, "#") {
		return errors.New("invalid browser session origin")
	}
	origin := "https://" + u.Hostname()
	if _, ok := browserSessionOrigins[origin]; !ok {
		return errors.New("browser session origin not enrolled")
	}
	if c.Origin != origin {
		return errors.New("browser session origin must be exact https host")
	}
	if c.DashboardPath != NewAPIBrowserSessionDashboard {
		return errors.New("fixed dashboard path required")
	}
	if !sessionToken.MatchString(c.BrowserInstanceID) {
		return errors.New("invalid browser instance")
	}
	return nil
}

func queryBrowserSession(ctx context.Context, t QueryTarget, run BrowserSkillCommand) Observation {
	out := Observation{QueryID: uuid.NewString(), ObservedAtMS: time.Now().UnixMilli(), CapabilityStatus: "needs_configuration", QueryStatus: "needs_configuration", ErrorCode: "BROWSER_SESSION_REQUIRED"}
	fail := func(status, code string) Observation {
		out.QueryStatus = status
		out.ErrorCode = code
		if status == "unsupported" || status == "needs_configuration" || status == "unavailable" {
			out.CapabilityStatus = status
		} else {
			out.CapabilityStatus = "supported"
		}
		return out
	}
	if t.NewAPI == nil || t.BrowserSession == nil || ValidateBrowserSessionContract(t.BrowserSession, t.NewAPI) != nil {
		return out
	}
	if t.Currency != "" || t.UnitMultiplier != "" {
		return fail("needs_configuration", "NEWAPI_UNIT_PROOF_REQUIRED")
	}
	if !validNewAPIConversion(t.NewAPI.Conversion, t.NewAPI.NativeUnit) {
		return fail("needs_configuration", "INVALID_CONVERSION_PROFILE")
	}
	endpoint := t.BrowserSession.Origin + NewAPIBrowserSessionPath
	if t.EndpointURL != "" && t.EndpointURL != endpoint {
		return fail("needs_configuration", "INVALID_ENDPOINT")
	}
	t.EndpointURL = endpoint
	if ValidateAdapterSource(NewAPIBrowserSessionV1, t.EndpointURL, t.PrivateTargets) != nil {
		return fail("needs_configuration", "INVALID_ENDPOINT")
	}
	if run == nil {
		return fail("unavailable", "WEB_BROWSER_UNAVAILABLE")
	}
	out.CapabilityStatus = "supported"
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = defaultQueryTimeout
	}
	if timeout > maxQueryTimeout {
		timeout = maxQueryTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	reserve := time.Until(deadline) / 10
	if reserve > time.Second {
		reserve = time.Second
	}
	work, cancelWork := context.WithDeadline(ctx, deadline.Add(-reserve))
	defer cancelWork()

	remaining := maxResponseBodySize
	var ownSessionHint string
	command := func(commandCtx context.Context, args ...string) ([]byte, error) {
		raw, err := run(commandCtx, args...)
		if len(raw) > remaining {
			if len(args) >= 2 && args[0] == "session" && args[1] == "start" {
				ownSessionHint = boundedOwnSessionID(raw)
			}
			remaining = 0
			return nil, errors.New("output budget exceeded")
		}
		remaining -= len(raw)
		if commandCtx.Err() != nil {
			return raw, commandCtx.Err()
		}
		var envelope struct {
			OK *bool `json:"ok"`
		}
		if json.Unmarshal(raw, &envelope) == nil && envelope.OK != nil && !*envelope.OK {
			err = errors.New("browser operation failed")
		}
		return raw, err
	}

	raw, err := command(work, "browsers", "--json")
	var browsers []struct {
		ID           string `json:"instance_id"`
		Unresponsive bool   `json:"unresponsive"`
	}
	if err != nil || json.Unmarshal(raw, &browsers) != nil {
		return fail("unavailable", "WEB_BROWSER_UNAVAILABLE")
	}
	matched := 0
	for _, browser := range browsers {
		if browser.ID == t.BrowserSession.BrowserInstanceID && !browser.Unresponsive {
			matched++
		}
	}
	if matched != 1 {
		return fail("unavailable", "WEB_BROWSER_UNAVAILABLE")
	}
	raw, err = command(work, "session", "start", "--json", "--browser", t.BrowserSession.BrowserInstanceID, "--no-focus")
	var session struct {
		ID      string `json:"session_id"`
		Browser string `json:"browser_instance_id"`
	}
	sessionErr := json.Unmarshal(raw, &session)
	if ownSessionHint != "" {
		session.ID = ownSessionHint
	} else {
		session.ID = boundedOwnSessionID(raw)
	}
	if sessionToken.MatchString(session.ID) {
		defer func() {
			cleanup, stop := context.WithDeadline(context.WithoutCancel(ctx), deadline)
			defer stop()
			if _, e := command(cleanup, "session", "stop", session.ID); e != nil {
				out = fail("network_error", "WEB_CLEANUP_FAILED")
			}
		}()
	}
	if err != nil || sessionErr != nil || !sessionToken.MatchString(session.ID) || session.Browser != "" && session.Browser != t.BrowserSession.BrowserInstanceID {
		return fail("unavailable", "WEB_SESSION_UNAVAILABLE")
	}
	dashboard := t.BrowserSession.Origin + t.BrowserSession.DashboardPath
	navRaw, err := command(work, "navigate", dashboard, "--session", session.ID, "--wait-until", "domcontentloaded", "--json")
	if err != nil {
		return fail("network_error", "WEB_NAVIGATION_FAILED")
	}
	var navigated struct {
		FinalURL string `json:"final_url"`
	}
	if json.Unmarshal(navRaw, &navigated) != nil || !sameOrigin(navigated.FinalURL, t.BrowserSession.Origin) {
		return fail("auth_error", "WEB_ORIGIN_MISMATCH")
	}
	expr := browserSessionExpression(t.BrowserSession.Origin, t.NewAPI.UserID, deadline)
	evalRaw, err := command(work, "evaluate", expr, "--session", session.ID, "--json")
	if err != nil {
		return fail("network_error", "WEB_READ_FAILED")
	}
	quota, status, code := parseBrowserSessionEvaluate(evalRaw, t.BrowserSession.Origin, t.NewAPI.UserID)
	if code != "" {
		return fail(status, code)
	}
	out.NativeQuota = &NewAPINativeQuota{Unit: "newapi_quota", State: "finite", Remaining: &quota}
	out.Semantic = "account_wallet"
	out.QueryStatus = "success"
	out.ErrorCode = ""
	out.ObservedAtMS = time.Now().UnixMilli()
	return applyNewAPIConversion(out, t.NewAPI.Conversion)
}

func sameOrigin(pageURL, origin string) bool {
	u, err := url.Parse(pageURL)
	if err != nil || u.User != nil {
		return false
	}
	return u.Scheme+"://"+u.Host == origin
}

func browserSessionExpression(origin, userID string, deadline time.Time) string {
	options, _ := json.Marshal(map[string]any{
		"origin":    origin,
		"path":      NewAPIBrowserSessionPath,
		"userId":    userID,
		"timeoutMs": max(1, int(time.Until(deadline)/time.Millisecond)),
	})
	return fmt.Sprintf(`(async () => {
            const c = %s;
            const end = Date.now() + c.timeoutMs;
            const safe = status => ({status, origin: location.origin});
            const isLogin = () => /\/(login|sign-in|signin)(\/|$)/i.test(location.pathname)
                || !!document.querySelector('input[type="password"]');
            while (Date.now() < end) {
                if (location.origin !== c.origin) return safe('origin');
                if (isLogin()) return safe('login');
                const app = document.querySelector('#root, #app') || document.body;
                if (document.readyState === 'complete' && app?.innerText?.trim()) break;
                await new Promise(resolve => setTimeout(resolve, Math.min(150, Math.max(1, end - Date.now()))));
            }
            if (Date.now() >= end) return safe('timeout');
            const controller = new AbortController();
            const timer = setTimeout(() => controller.abort(), Math.max(1, end - Date.now()));
            try {
                if (location.origin !== c.origin) return safe('origin');
                const response = await fetch(c.path, {
                    credentials: 'include', headers: {'New-Api-User': c.userId, 'Accept': 'application/json'},
                    signal: controller.signal, redirect: 'error',
                });
                if (location.origin !== c.origin || new URL(response.url).origin !== c.origin) return safe('origin');
                if (!response.ok) return safe(response.status === 401 ? 'login' : 'http');
                let payload;
                try { payload = await response.json(); } catch { return safe('field'); }
                if (payload?.success !== true) return safe('business');
                if (String(payload?.data?.id) !== c.userId) return safe('identity');
                const balance = payload?.data?.quota;
                if (!['number', 'string'].includes(typeof balance) || String(balance).trim() === ''
                        || !Number.isFinite(Number(balance))) return safe('numeric');
                return {status: 'ok', origin: location.origin, http_status: response.status,
                    success: true, user_id: String(payload.data.id), balance: String(balance)};
            } catch (error) {
                return safe(error.name === 'AbortError' ? 'timeout' : 'network');
            } finally { clearTimeout(timer); }
        })()`, string(options))
}

func parseBrowserSessionEvaluate(raw []byte, origin, userID string) (quota, status, code string) {
	// BrowserSkill evaluate wraps page results as {ok:true,value:{...}}.
	if outer, ok := checklistNumbers(raw); ok {
		if value, hasValue := outer["value"]; hasValue {
			if okRaw, hasOK := outer["ok"]; hasOK {
				var okFlag bool
				if json.Unmarshal(okRaw, &okFlag) != nil || !okFlag {
					return "", "parse_error", "INVALID_NEWAPI_RESPONSE"
				}
			}
			raw = value
		}
	}
	fields, ok := checklistNumbers(raw)
	if !ok {
		return "", "parse_error", "INVALID_NEWAPI_RESPONSE"
	}
	statusRaw, hasStatus := fields["status"]
	var statusValue string
	if !hasStatus || json.Unmarshal(statusRaw, &statusValue) != nil {
		return "", "parse_error", "INVALID_NEWAPI_RESPONSE"
	}
	safe := map[string]string{
		"login": "WEB_LOGIN_REQUIRED", "timeout": "WEB_TIMEOUT", "network": "UPSTREAM_UNAVAILABLE",
		"http": "UPSTREAM_STATUS", "business": "INVALID_NEWAPI_RESPONSE", "identity": "NEWAPI_ACCOUNT_MISMATCH",
		"numeric": "INVALID_NATIVE_QUOTA", "field": "INVALID_NEWAPI_RESPONSE", "origin": "WEB_ORIGIN_MISMATCH",
	}
	if statusValue != "ok" {
		if code = safe[statusValue]; code == "" {
			code = "WEB_READ_FAILED"
		}
		status = "auth_error"
		if statusValue == "timeout" || statusValue == "network" || statusValue == "http" {
			status = "network_error"
		}
		if statusValue == "numeric" || statusValue == "field" || statusValue == "business" {
			status = "parse_error"
		}
		return "", status, code
	}
	originRaw, hasOrigin := fields["origin"]
	var gotOrigin string
	if !hasOrigin || json.Unmarshal(originRaw, &gotOrigin) != nil || gotOrigin != origin {
		return "", "auth_error", "WEB_ORIGIN_MISMATCH"
	}
	userRaw, hasUser := fields["user_id"]
	var gotUser string
	if !hasUser || json.Unmarshal(userRaw, &gotUser) != nil || gotUser != userID {
		return "", "parse_error", "NEWAPI_ACCOUNT_MISMATCH"
	}
	balRaw, hasBal := fields["balance"]
	if !hasBal {
		return "", "parse_error", "INVALID_NATIVE_QUOTA"
	}
	quota, ok = newAPIInt(bytes.TrimSpace(balRaw))
	if !ok {
		return "", "parse_error", "INVALID_NATIVE_QUOTA"
	}
	return quota, "success", ""
}
