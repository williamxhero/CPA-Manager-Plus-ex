package balance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"regexp"
	"time"
)

// BrowserSkillCommand is the external CLI process boundary. Never supplied by
// management HTTP input. Tests replace this boundary, not the host/store.
type BrowserSkillCommand func(context.Context, ...string) ([]byte, error)

type WebRequest struct {
	Source    WebConfig
	Authority string
}

// WebPage is produced by a host-installed, observed-page parsing contract.
// The default registry is empty: no LAYSO DOM/labels have been observed yet.
type WebPage struct {
	URL              string
	AccountID        string
	TenantID         string
	Role             string
	IdentityVerified bool
	RoleVerified     bool
	Currency         string
	Amount           *string
}

type WebEnrollment struct {
	Authority             string
	Source                WebConfig
	ProfileVerified       bool
	ExecutionUserVerified bool
	ParserVerified        bool
	ProofReference        string
	EvidenceKind          string
}

type BrowserSkillOptions struct {
	Run        BrowserSkillCommand
	Enrollment func(WebRequest) (WebEnrollment, bool)
	Rules      map[string]func([]byte) (WebPage, error)
}

type WebReader interface {
	SupportsRule(string) bool
	Ready(WebRequest) bool
	Query(context.Context, WebRequest) Observation
}

// WebEvidence contains only a redacted receipt, never raw page text or secrets.
// It makes no assertion of key ownership or shared-wallet membership.
type WebEvidence struct {
	Authority    string `json:"authority"`
	IdentityHash string `json:"identityHash"`
	ProofHash    string `json:"proofHash"`
	ParsingRule  string `json:"parsingRule"`
	Verification string `json:"verification"`
}

type BrowserSkill struct {
	options BrowserSkillOptions
	gate    chan struct{}
}

// Options are trusted host startup dependencies, never management input. An
// empty default registry/enrollment fails closed before invoking the CLI.
func NewBrowserSkill(options BrowserSkillOptions) *BrowserSkill {
	rules := make(map[string]func([]byte) (WebPage, error), len(options.Rules))
	for id, parse := range options.Rules {
		rules[id] = parse
	}
	options.Rules = rules
	if options.Run == nil {
		options.Run = runBrowserSkill
	}
	return &BrowserSkill{options: options, gate: make(chan struct{}, 1)}
}
func (b *BrowserSkill) SupportsRule(rule string) bool {
	return b != nil && rule != "unverified" && b.options.Rules[rule] != nil
}
func (b *BrowserSkill) enrollment(q WebRequest) (WebEnrollment, bool) {
	if b == nil || ValidateWebContract(&q.Source) != nil || !b.SupportsRule(q.Source.ParsingRule) || b.options.Enrollment == nil || len(q.Authority) != 64 {
		return WebEnrollment{}, false
	}
	e, ok := b.options.Enrollment(q)
	ok = ok && e.Authority == q.Authority && e.Source == q.Source && e.ProfileVerified && e.ExecutionUserVerified && e.ParserVerified && e.ProofReference != "" && (e.EvidenceKind == "verified_browser" || e.EvidenceKind == "controlled_fixture")
	return e, ok
}
func (b *BrowserSkill) Ready(q WebRequest) bool { _, ok := b.enrollment(q); return ok }

var webMoney = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,119})(\.[0-9]{1,120})?$`)
var sessionToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

// An own-session hint must come from one unambiguous top-level field in a
// complete, bounded start response. Never guess from partial/nested JSON or
// choose between duplicate IDs that could refer to a different task.
func boundedOwnSessionID(raw []byte) string {
	if len(raw) > maxResponseBodySize {
		return ""
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return ""
	}
	var id string
	found := false
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return ""
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return ""
		}
		if key == "session_id" {
			if found || json.Unmarshal(value, &id) != nil {
				return ""
			}
			found = true
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		return ""
	}
	if _, err = decoder.Token(); err != io.EOF {
		return ""
	}
	if !found || !sessionToken.MatchString(id) {
		return ""
	}
	return id
}

func webDigest(value any) string {
	raw, _ := json.Marshal(value)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func (b *BrowserSkill) Query(ctx context.Context, q WebRequest) (out Observation) {
	out.CapabilityStatus = "supported"
	fail := func(status, code string) Observation {
		return Observation{CapabilityStatus: "supported", QueryStatus: status, ErrorCode: code}
	}
	enrolled, ok := b.enrollment(q)
	if !ok {
		return fail("unavailable", "WEB_SOURCE_UNVERIFIED")
	}
	// The management host already supplies a hard budget. Add the standalone
	// default only for callers without a deadline; never silently shorten it.
	var cancel context.CancelFunc
	if _, bounded := ctx.Deadline(); bounded {
		ctx, cancel = context.WithCancel(ctx)
	} else {
		ctx, cancel = context.WithTimeout(ctx, defaultQueryTimeout)
	}
	defer cancel()
	select {
	case b.gate <- struct{}{}:
		defer func() { <-b.gate }()
	case <-ctx.Done():
		return fail("network_error", "WEB_TIMEOUT")
	}
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
		// Enrollment may be revoked while queued or between CLI operations.
		// Cleanup alone remains allowed after revocation.
		cleanup := len(args) >= 2 && args[0] == "session" && args[1] == "stop"
		if !cleanup {
			current, verified := b.enrollment(q)
			if !verified || current != enrolled {
				return nil, errors.New("browser enrollment changed")
			}
		}
		raw, err := b.options.Run(commandCtx, args...)
		if len(raw) > remaining {
			// A successful start may already have created our session. Retain
			// only its validated ID for cleanup; the over-budget result still
			// fails and no raw response escapes this boundary. Never infer an
			// ID from other commands or parse beyond the per-command bound.
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
		if browser.ID == q.Source.Browser.InstanceID && !browser.Unresponsive {
			matched++
		}
	}
	if matched != 1 {
		return fail("unavailable", "WEB_BROWSER_UNAVAILABLE")
	}
	raw, err = command(work, "session", "start", "--json", "--browser", q.Source.Browser.InstanceID, "--no-focus")
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
			// Fresh cancellation state, SAME hard deadline. Stop only our own
			// returned session, including a partially failed session start.
			cleanup, stop := context.WithDeadline(context.WithoutCancel(ctx), deadline)
			defer stop()
			if _, e := command(cleanup, "session", "stop", session.ID); e != nil {
				out = fail("network_error", "WEB_CLEANUP_FAILED")
				return
			}
			if current, verified := b.enrollment(q); out.QueryStatus == "success" && (!verified || current != enrolled) {
				out = fail("auth_error", "WEB_ENROLLMENT_CHANGED")
			}
		}()
	}
	if err != nil || sessionErr != nil || !sessionToken.MatchString(session.ID) || session.Browser != "" && session.Browser != q.Source.Browser.InstanceID {
		return fail("unavailable", "WEB_SESSION_UNAVAILABLE")
	}
	if _, err = command(work, "navigate", LaysoBillingPage, "--session", session.ID); err != nil {
		return fail("network_error", "WEB_NAVIGATION_FAILED")
	}
	var page WebPage
	for i := 0; i < 2; i++ {
		raw, err = command(work, "observe", "--session", session.ID, "--json", "--max-tokens", "12000")
		if err != nil {
			return fail("network_error", "WEB_READ_FAILED")
		}
		page, err = b.options.Rules[q.Source.ParsingRule](raw)
		if err != nil {
			return fail("parse_error", "WEB_PAGE_CHANGED")
		}
		if !page.IdentityVerified || !page.RoleVerified {
			return fail("auth_error", "WEB_IDENTITY_UNVERIFIED")
		}
		if page.URL != LaysoBillingPage || page.AccountID != q.Source.AccountID || page.TenantID != q.Source.TenantID || page.Role != q.Source.Role {
			return fail("auth_error", "WEB_IDENTITY_MISMATCH")
		}
	}
	current, verified := b.enrollment(q)
	if !verified || current != enrolled {
		return fail("auth_error", "WEB_ENROLLMENT_CHANGED")
	}
	if page.Currency != q.Source.Currency || page.Amount == nil || !webMoney.MatchString(*page.Amount) {
		return fail("parse_error", "WEB_INVALID_AMOUNT")
	}
	if work.Err() != nil {
		return fail("network_error", "WEB_TIMEOUT")
	}
	receipt := &WebEvidence{Authority: q.Authority, IdentityHash: webDigest(q.Source), ProofHash: webDigest(enrolled.ProofReference), ParsingRule: q.Source.ParsingRule, Verification: enrolled.EvidenceKind}
	out = Observation{CapabilityStatus: "supported", QueryStatus: "success", Semantic: "web_account_balance", AmountDecimal: *page.Amount, Currency: page.Currency, ObservedAtMS: time.Now().UnixMilli(), WebEvidence: receipt}
	out.EvidenceHash = webDigest(struct {
		Receipt          *WebEvidence
		Amount, Currency string
		At               int64
	}{receipt, out.AmountDecimal, out.Currency, out.ObservedAtMS})
	return out
}

type browserOutput struct{ bytes.Buffer }

func (b *browserOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > maxResponseBodySize {
		return 0, errors.New("browser output exceeds budget")
	}
	return b.Buffer.Write(p)
}
func runBrowserSkill(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "bsk", args...)
	cmd.WaitDelay = 250 * time.Millisecond
	var output browserOutput
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if err != nil {
		err = errors.New("browser command unavailable")
	}
	return output.Bytes(), err
}

// No eval, storage, cookies, private APIs, login, account switching, user-tab
// borrowing, daemon restart, alternate browser selector or browser backend.
