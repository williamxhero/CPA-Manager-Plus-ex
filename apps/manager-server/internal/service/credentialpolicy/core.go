package credentialpolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	mc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
)

// gateCapabilities follows the agreed CORE-GATE-CONTRACT.md v1. Readiness is
// never an operation ACK or proof of today's owner restriction.
type gateCapabilities struct {
	Version      int      `json:"version"`
	Ready        bool     `json:"ready"`
	Durable      bool     `json:"durable"`
	Applied      bool     `json:"applied"`
	GateRevision uint64   `json:"gateRevision"`
	Mode         string   `json:"mode"`
	Subjects     []string `json:"subjects"`
	Routes       []struct {
		Route     string `json:"route"`
		Supported bool   `json:"supported"`
		Reason    string `json:"reason"`
	} `json:"routes"`
}
type coreClient struct{ connection *mc.Service }

// Contact only the saved management connection. Refuse redirects and changed
// connection readback; never echo Core bodies, keys or transport errors.
func (c coreClient) request(ctx context.Context, method, path string, body, out any) string {
	if c.connection == nil {
		return "gate_connection_unavailable"
	}
	cfg, _, _, err := c.connection.ResolveManagerConfigWithSource(ctx)
	if err != nil {
		return "gate_connection_unavailable"
	}
	conn := cfg.CPAConnection
	u, err := url.Parse(cpa.NormalizeBaseURL(conn.CPABaseURL))
	if err != nil || conn.ManagementKey == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "http" && u.Scheme != "https" {
		return "gate_connection_unavailable"
	}
	var data []byte
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return "gate_contract_invalid"
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(u.String(), "/")+"/v8/management/inference-gates/"+path, bytes.NewReader(data))
	if err != nil {
		return "gate_connection_unavailable"
	}
	req.Header.Set("Authorization", "Bearer "+conn.ManagementKey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	res, err := client.Do(req)
	if err != nil {
		return "gate_core_unavailable"
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case 401, 403:
		return "gate_auth_error"
	case 404:
		return "gate_contract_unavailable"
	case 200:
	default:
		return "gate_core_unavailable"
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 || json.Unmarshal(raw, out) != nil {
		return "gate_contract_invalid"
	}
	current, _, _, err := c.connection.ResolveManagerConfigWithSource(ctx)
	if err != nil || current.CPAConnection != conn || current.UpdatedAtMS != cfg.UpdatedAtMS {
		return "gate_connection_changed"
	}
	return ""
}

func (c coreClient) capabilities(ctx context.Context, src cq.Source) (gateCapabilities, string) {
	var caps gateCapabilities
	if reason := c.request(ctx, "GET", "capabilities", nil, &caps); reason != "" {
		return caps, reason
	}
	if caps.Version != 1 {
		return caps, "gate_contract_invalid"
	}
	if caps.Mode != "standalone" {
		return caps, "gate_mode_unsupported"
	}
	if !caps.Ready || !caps.Durable || !caps.Applied {
		return caps, "gate_not_ready"
	}
	query := struct {
		Version int `json:"version"`
		cq.Binding
		AuthIndex     string `json:"auth_index"`
		SourceVersion string `json:"sourceVersion"`
		Route         string `json:"route"`
	}{1, src.Binding, src.AuthIndex, src.SourceVersion, "all-current-source"}
	var eligibility struct {
		Version int `json:"version"`
		cq.Binding
		AuthIndex     string `json:"auth_index"`
		SourceVersion string `json:"sourceVersion"`
		Ready         bool   `json:"ready"`
		Supported     bool   `json:"supported"`
		Routes        []struct {
			Route     string `json:"route"`
			Supported bool   `json:"supported"`
		} `json:"routes"`
	}
	if reason := c.request(ctx, "POST", "eligibility", query, &eligibility); reason != "" {
		return caps, reason
	}
	if eligibility.Version != 1 || !reflect.DeepEqual(eligibility.Binding, src.Binding) || eligibility.AuthIndex != src.AuthIndex || eligibility.SourceVersion != src.SourceVersion || len(eligibility.Routes) == 0 || len(eligibility.Routes) > 128 {
		return caps, "gate_eligibility_invalid"
	}
	if !eligibility.Ready || !eligibility.Supported {
		return caps, "gate_source_unsupported"
	}
	for _, route := range eligibility.Routes {
		if route.Route == "" || !route.Supported {
			return caps, "gate_source_unsupported"
		}
	}
	// Positive ledger/admission verification awaits the actual producer artifact.
	return caps, "gate_application_unverified"
}
