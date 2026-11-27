// Package balancematch discovers a monetary source without starting a balance
// job, observing a balance, or participating in any pause policy.
package balancematch

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
)

type Request struct {
	Binding officialbalance.Binding `json:"binding"`
	Apply   bool                    `json:"apply,omitempty"`
}

type Result struct {
	Status      string      `json:"status"`
	Adapter     string      `json:"adapter,omitempty"`
	Endpoint    string      `json:"endpoint,omitempty"`
	Confidence  int         `json:"confidence"`
	Evidence    *Evidence   `json:"evidence,omitempty"`
	Candidates  []Candidate `json:"candidates"`
	Reason      string      `json:"reason,omitempty"`
	Applied     bool        `json:"applied"`
	ApplyReason string      `json:"applyReason,omitempty"`
}

type Candidate struct {
	Adapter  string   `json:"adapter"`
	Evidence Evidence `json:"evidence"`
}

type Evidence struct {
	Source    string    `json:"source"`
	Host      string    `json:"host"`
	Adapter   string    `json:"adapter"`
	URL       string    `json:"url"`
	Status    int       `json:"status"` // 0 means no HTTP response (including fingerprints).
	Fields    []string  `json:"matchedFields"`
	Timestamp time.Time `json:"timestamp"`
	Score     int       `json:"score"`
	Reason    string    `json:"reason,omitempty"`
}

const threshold = 90

var candidates = []struct{ adapter, path, exact, suffix string }{
	{"deepseek", "/user/balance", "api.deepseek.com", "deepseek.com"},
	{"deepinfra", "/payment/checklist", "api.deepinfra.com", "deepinfra.com"},
	{balance.OpenAICompatUsageV1, balance.OpenAICompatUsagePath, "wawapii.com", ""},
}

type Options struct {
	Network       balance.NetworkOptions
	MaxConcurrent int
	Deadline      time.Duration
	HostInterval  time.Duration
}

type Matcher struct {
	official *officialbalance.Service
	options  Options
	slots    chan struct{}
	hosts    hostLimiter
}

func New(official *officialbalance.Service, options Options) *Matcher {
	if options.MaxConcurrent <= 0 {
		options.MaxConcurrent = 4
	}
	if options.Deadline <= 0 {
		options.Deadline = 10 * time.Second
	}
	if options.HostInterval <= 0 {
		options.HostInterval = 250 * time.Millisecond
	}
	return &Matcher{official: official, options: options, slots: make(chan struct{}, options.MaxConcurrent)}
}

func unmatched(reason string) Result {
	return Result{Status: "unmatched", Reason: reason, Candidates: []Candidate{}}
}

func (m *Matcher) Match(ctx context.Context, req Request) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, m.options.Deadline)
	defer cancel()
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	default:
		return unmatched("match_busy"), nil
	}
	access, err := m.official.ResolveMatchAccess(ctx, req.Binding)
	if errors.Is(err, officialbalance.ErrMatchAccess) {
		return unmatched("access_unavailable"), nil
	}
	if err != nil {
		return Result{}, err
	}
	out := m.detect(ctx, access.BaseURL, access.Credential)
	if !req.Apply || out.Status != "matched" {
		return out, nil
	}
	if access.Credential == "" {
		out.ApplyReason = "credential_unavailable"
		return out, nil
	}
	if err := m.official.ConfigureMatch(ctx, access, out.Adapter, out.Endpoint); err != nil {
		if errors.Is(err, officialbalance.ErrMatchManual) {
			out.ApplyReason = "manual_configuration"
			return out, nil
		}
		return Result{}, err
	}
	out.Applied = true
	return out, nil
}

// Detect is the matching seam for an already-resolved access; HTTP callers
// must use Match so a client cannot supply either the probe URL or credential.
func (m *Matcher) Detect(ctx context.Context, baseURL, credential string) Result {
	ctx, cancel := context.WithTimeout(ctx, m.options.Deadline)
	defer cancel()
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	default:
		return unmatched("match_busy")
	}
	return m.detect(ctx, baseURL, credential)
}

func (m *Matcher) detect(ctx context.Context, baseURL, credential string) Result {
	origin, host := sourceOrigin(baseURL)
	// Do not reflect userinfo, arbitrary paths or a credential embedded in a
	// misconfigured origin. Field names and failure reasons below are static.
	if origin == "" || credential != "" && strings.Contains(strings.ToLower(origin), strings.ToLower(credential)) {
		return unmatched("invalid_base_url")
	}
	for _, c := range candidates {
		if host == c.exact || c.suffix != "" && strings.HasSuffix(host, "."+c.suffix) {
			return resultFrom(Evidence{Source: "fingerprint", Host: host, Adapter: c.adapter, URL: origin + c.path, Fields: []string{}, Timestamp: time.Now().UTC(), Score: 100}, []Candidate{})
		}
	}
	if credential == "" {
		return unmatched("credential_unavailable")
	}
	lease := m.hosts.acquire(ctx, host)
	out := unmatched("no_usable_candidate")
	if lease == nil {
		out.Reason = "probe_budget_exhausted"
		return out
	}
	defer m.hosts.release(lease)
	var best Evidence
	leaders := 0
	for _, c := range candidates {
		e := Evidence{Source: "probe", Host: host, Adapter: c.adapter, URL: origin + c.path, Fields: []string{}, Timestamp: time.Now().UTC()}
		if !lease.wait(ctx, m.options.HostInterval) {
			e.Reason = "probe_budget_exhausted"
		} else {
			e.Timestamp = time.Now().UTC()
			p := balance.ProbeMonetarySource(ctx, c.adapter, e.URL, credential, m.options.Network)
			e.Status, e.Fields, e.Reason = p.Status, p.Fields, p.Reason
			if len(p.Fields) != 0 {
				e.Score = 80 // successful status and valid monetary contract
				if p.JSON {
					e.Score += 20
				}
			} else if p.Status == http.StatusOK {
				e.Score = 20 // successful status alone is not usable
			}
		}
		out.Candidates = append(out.Candidates, Candidate{Adapter: c.adapter, Evidence: e})
		if e.Score > best.Score {
			best, leaders = e, 1
		} else if e.Score == best.Score {
			leaders++
		}
	}
	switch {
	case ctx.Err() != nil:
		out.Reason = "probe_budget_exhausted"
	case len(best.Fields) == 0:
	case best.Score < threshold:
		out.Reason = "low_confidence"
	case leaders != 1:
		out.Reason = "tied_candidates"
	default:
		return resultFrom(best, out.Candidates)
	}
	return out
}

func resultFrom(e Evidence, candidates []Candidate) Result {
	return Result{Status: "matched", Adapter: e.Adapter, Endpoint: e.URL, Confidence: e.Score, Evidence: &e, Candidates: candidates}
}

func sourceOrigin(raw string) (string, string) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || strings.Contains(u.Hostname(), "%") {
		return "", ""
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if ip, err := netip.ParseAddr(host); err == nil {
		host = ip.Unmap().String()
	}
	authority := host
	if port := u.Port(); port != "" && port != "443" {
		authority = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		authority = "[" + host + "]"
	}
	if host == "" {
		return "", ""
	}
	return (&url.URL{Scheme: "https", Host: authority}).String(), host
}
