package resourcecapability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
)

func validQuotaSourceHeaders(snapshot Snapshot, raw []json.RawMessage) bool {
	if raw == nil || len(raw) > 10000 {
		return false
	}
	seen := map[string]bool{}
	for _, data := range raw {
		var source struct {
			Scope     Scope    `json:"scope"`
			Targets   []Target `json:"targets"`
			AuthIndex string   `json:"auth_index"`
			Protocol  string   `json:"protocol"`
			State     string   `json:"state"`
		}
		if json.Unmarshal(data, &source) != nil || source.Scope != snapshot.Scope || len(source.Targets) == 0 || len(source.Targets) > 64 || !oneOf(source.State, "ready", "unsupported", "needs_configuration") {
			return false
		}
		if source.AuthIndex != "" {
			if !runtimeIndex.MatchString(source.AuthIndex) || seen[source.AuthIndex] {
				return false
			}
			seen[source.AuthIndex] = true
		}
		for _, target := range source.Targets {
			found := false
			for _, row := range snapshot.Resources {
				if target == row.Target {
					found = true
					if source.State == "ready" && (row.Identity != "valid" || row.Readiness != "ready" || source.Protocol == "codex-oauth-wham" && row.Family != "codex" || source.Protocol == "opencode-go-usage" && row.Family != "opencode-go") {
						return false
					}
				}
			}
			if !found {
				return false
			}
		}
		if source.State == "ready" && (source.AuthIndex == "" || !oneOf(source.Protocol, "codex-oauth-wham", "opencode-go-usage")) {
			return false
		}
	}
	return true
}

// CredentialQuotaRequest contacts only two explicit v8 operations on the saved
// Core connection. It cannot choose provider destinations or export credentials.
func (s *Service) CredentialQuotaRequest(ctx context.Context, query any, out any) error {
	if s.connection == nil {
		return errors.New("quota connection unavailable")
	}
	cfg, _, _, err := s.connection.ResolveManagerConfigWithSource(ctx)
	if err != nil {
		return errors.New("quota connection unavailable")
	}
	conn := cfg.CPAConnection
	if conn.CPABaseURL == "" || conn.ManagementKey == "" {
		return errors.New("quota connection unavailable")
	}
	path, method := "sources", "GET"
	var data []byte
	if query != nil {
		path = "query"
		method = "POST"
		data, err = json.Marshal(query)
		if err != nil {
			return errors.New("invalid quota query")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(cpa.NormalizeBaseURL(conn.CPABaseURL), "/")+"/v8/management/credential-quota/"+path, bytes.NewReader(data))
	if err != nil {
		return errors.New("quota connection unavailable")
	}
	req.Header.Set("Authorization", "Bearer "+conn.ManagementKey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	res, err := client.Do(req)
	if err != nil {
		return errors.New("quota core unavailable")
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil || len(b) > 2<<20 {
		return errors.New("quota response invalid")
	}
	current, _, _, err := s.connection.ResolveManagerConfigWithSource(ctx)
	if err != nil || current.CPAConnection != conn || current.UpdatedAtMS != cfg.UpdatedAtMS {
		return errors.New("quota connection changed")
	}
	if res.StatusCode != 200 || json.Unmarshal(b, out) != nil {
		return errors.New("quota core unavailable")
	}
	return nil
}
