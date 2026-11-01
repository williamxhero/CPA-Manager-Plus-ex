package resourcecapability

import (
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
)

// Negotiated is read evidence only. Neither capability flags nor catalog identity
// are an applied gate ACK. Legacy support fields remain unchanged.
type Negotiated struct {
	CredentialQuery   Capability       `json:"credentialQuery"`
	BindingCapability *Capability      `json:"bindingCapability,omitempty"`
	Bindings          []RuntimeBinding `json:"bindings,omitempty"`
	Snapshot          *Snapshot        `json:"snapshot,omitempty"`
	ContractVersion   int              `json:"contractVersion"`
	Identity          Capability       `json:"identity"`
	Query             Capability       `json:"query"`
	IndependentGate   Capability       `json:"independentGate"`
	PolicyLocked      bool             `json:"policyLocked"`

	// Request-local authenticated projection. Never serialized or reused by a
	// later Inventory call; the deferred connection CAS can discard it entirely.
	credentialQuotaSources json.RawMessage
}

// CredentialQuotaSourceProjection returns an isolated copy of the source
// envelope verified against this inventory. It performs no I/O and exposes no
// write access to the retained evidence. Consumers must still validate their
// complete source/binding/protocol contract, not infer readiness from headers.
func (n *Negotiated) CredentialQuotaSourceProjection() json.RawMessage {
	if n == nil {
		return nil
	}
	return append(json.RawMessage(nil), n.credentialQuotaSources...)
}

type coreCapabilities struct {
	CredentialQuery *struct {
		Version        int  `json:"version"`
		Sources        bool `json:"sources"`
		ScopedQuery    bool `json:"scoped_query"`
		SourceBindings bool `json:"source_bindings"`
	} `json:"credential_query"`
	Scope        *Scope `json:"scope"`
	Version      int    `json:"version"`
	Inventory    bool   `json:"inventory"`
	Durable      bool   `json:"durable"`
	Gate         bool   `json:"gate"`
	Query        bool   `json:"query"`
	PolicyLocked bool   `json:"policy_locked"`
	Reason       string `json:"reason"`
}

// This is one request-local read projection, not an authority cache. Validate
// both existing projections and their exact join before advertising this view;
// credentialquota.Sources still validates its complete consumer contract too.
type quotaSourceBindingView struct {
	Version  int               `json:"version"`
	Sources  []json.RawMessage `json:"sources"`
	Bindings []RuntimeBinding  `json:"bindings"`
}

func (v quotaSourceBindingView) valid(snapshot Snapshot) bool {
	if v.Version != 1 || !validBindings(snapshot, v.Bindings) || !validQuotaSourceHeaders(snapshot, v.Sources) {
		return false
	}
	byIndex := make(map[string]RuntimeBinding, len(v.Bindings))
	for _, binding := range v.Bindings {
		byIndex[binding.AuthIndex] = binding
	}
	seen := make(map[string]bool, len(v.Bindings))
	for _, raw := range v.Sources {
		var source struct {
			Scope     Scope    `json:"scope"`
			Targets   []Target `json:"targets"`
			AuthIndex string   `json:"auth_index"`
			State     string   `json:"state"`
			Reason    string   `json:"reason"`
		}
		if json.Unmarshal(raw, &source) != nil {
			return false
		}
		if source.AuthIndex == "" {
			if source.State != "unsupported" || source.Reason != "runtime_binding_unavailable" {
				return false
			}
			continue
		}
		binding, ok := byIndex[source.AuthIndex]
		if !ok || seen[source.AuthIndex] || binding.Scope != source.Scope || !reflect.DeepEqual(binding.Targets, source.Targets) {
			return false
		}
		seen[source.AuthIndex] = true
	}
	return len(seen) == len(v.Bindings)
}

func unavailable(reason string) *Negotiated {
	return &Negotiated{CredentialQuery: Capability{"unknown", "credential_query_contract_unverified"}, Identity: Capability{"unknown", reason}, Query: Capability{"unknown", "query_contract_unverified"}, IndependentGate: Capability{"unavailable", "consumer_contract_unconfigured"}, PolicyLocked: true}
}

// Inventory contacts only the saved/env-resolved Core management connection.
// It never reads configuration/provider credentials or falls back to v0 APIs.
func (s *Service) Inventory(ctx context.Context, bindings ...bool) (out *Negotiated) {
	result := unavailable("connection_unconfigured")
	if s.connection == nil {
		return result
	}
	cfg, _, _, err := s.connection.ResolveManagerConfigWithSource(ctx)
	if err != nil {
		return unavailable("connection_unavailable")
	}
	conn := cfg.CPAConnection
	if conn.CPABaseURL == "" || conn.ManagementKey == "" {
		return result
	}
	// Re-resolve after I/O, including negative responses. A late result from a
	// replaced connection cannot be presented as the current instance's state.
	defer func() {
		current, _, _, err := s.connection.ResolveManagerConfigWithSource(ctx)
		if err != nil {
			out = unavailable("connection_unavailable")
			return
		}
		if current.CPAConnection != conn || current.UpdatedAtMS != cfg.UpdatedAtMS {
			out = unavailable("connection_changed")
		}
	}()
	var caps coreCapabilities
	if reason := readCore(ctx, conn.CPABaseURL, conn.ManagementKey, "resource-capabilities", &caps); reason != "" {
		return unavailable(reason)
	}
	if caps.Version != 1 {
		return unavailable("unsupported_contract")
	}
	result.ContractVersion = 1
	if !caps.Inventory || !caps.Durable {
		switch caps.Reason {
		case "not_enabled", "unsupported_mode", "unsupported_storage", "unsupported_filesystem", "not_ready":
			result.Identity = Capability{"unavailable", caps.Reason}
		default:
			result.Identity = Capability{"unknown", "inventory_unavailable"}
		}
	}
	if caps.Inventory && caps.Durable {
		var snapshot Snapshot
		if reason := readCore(ctx, conn.CPABaseURL, conn.ManagementKey, "resources", &snapshot); reason != "" {
			return unavailable(reason)
		}
		if snapshot.ContractVersion != 1 || !snapshot.Complete || caps.Scope == nil || snapshot.Scope != *caps.Scope {
			return unavailable("stale_inventory")
		}
		if !snapshot.valid() {
			return unavailable("invalid_contract")
		}
		if caps.CredentialQuery != nil && caps.CredentialQuery.SourceBindings {
			if caps.CredentialQuery.Version != 1 || !caps.CredentialQuery.Sources || !caps.CredentialQuery.ScopedQuery {
				return unavailable("unsupported_source_binding_contract")
			}
			var view quotaSourceBindingView
			var projection json.RawMessage
			if reason := readCore(ctx, conn.CPABaseURL, conn.ManagementKey, "credential-quota/sources", &projection); reason != "" {
				return unavailable(reason)
			}
			if json.Unmarshal(projection, &view) != nil || !view.valid(snapshot) {
				return unavailable("invalid_source_binding_view")
			}
			// Close AFTER the complete combined view. A capability/scope change
			// cannot repair a bad advertised view with a second legacy read.
			var current coreCapabilities
			if reason := readCore(ctx, conn.CPABaseURL, conn.ManagementKey, "resource-capabilities", &current); reason != "" {
				return unavailable(reason)
			}
			if current.Scope == nil || *current.Scope != snapshot.Scope || !current.Inventory || !current.Durable || current.Version != 1 || current.CredentialQuery == nil || *current.CredentialQuery != *caps.CredentialQuery {
				return unavailable("stale_source_binding_view")
			}
			result.Identity = Capability{"available", "durable_inventory"}
			result.CredentialQuery = Capability{"available", "scoped_credential_query_v1"}
			result.credentialQuotaSources = append(json.RawMessage(nil), projection...)
			if len(bindings) > 0 && bindings[0] {
				result.BindingCapability = &Capability{"available", "host_issued_bindings"}
				result.Bindings = view.Bindings
			}
			result.Snapshot = &snapshot
			result.Query = Capability{"unavailable", "query_consumer_unconfigured"}
			return result
		}
		if len(bindings) > 0 && bindings[0] {
			var observed struct {
				Bindings []RuntimeBinding `json:"bindings"`
			}
			reason := readCore(ctx, conn.CPABaseURL, conn.ManagementKey, "resource-bindings", &observed)
			if reason != "" {
				result.BindingCapability = &Capability{"unavailable", reason}
			} else if !validBindings(snapshot, observed.Bindings) {
				result.BindingCapability = &Capability{"unavailable", "invalid_binding_contract"}
			} else {
				result.BindingCapability = &Capability{"available", "host_issued_bindings"}
				result.Bindings = observed.Bindings
			}
		}
		var current coreCapabilities
		if reason := readCore(ctx, conn.CPABaseURL, conn.ManagementKey, "resource-capabilities", &current); reason != "" {
			return unavailable(reason)
		}
		if current.Scope == nil || *current.Scope != snapshot.Scope || !current.Inventory || !current.Durable || current.Version != 1 {
			return unavailable("stale_inventory")
		}
		result.Identity = Capability{"available", "durable_inventory"}
		if caps.CredentialQuery != nil && caps.CredentialQuery.Version == 1 && caps.CredentialQuery.Sources && caps.CredentialQuery.ScopedQuery && current.CredentialQuery != nil && *caps.CredentialQuery == *current.CredentialQuery {
			var query struct {
				Version int               `json:"version"`
				Sources []json.RawMessage `json:"sources"`
			}
			var projection json.RawMessage
			if readCore(ctx, conn.CPABaseURL, conn.ManagementKey, "credential-quota/sources", &projection) == "" && json.Unmarshal(projection, &query) == nil && query.Version == 1 && validQuotaSourceHeaders(snapshot, query.Sources) {
				result.CredentialQuery = Capability{"available", "scoped_credential_query_v1"}
				result.credentialQuotaSources = append(json.RawMessage(nil), projection...)
			}
		}
		result.Snapshot = &snapshot
		result.Query = Capability{"unavailable", "query_consumer_unconfigured"}
	}
	return result
}

func readCore(ctx context.Context, base, key, path string, out any) string {
	u, err := url.Parse(cpa.NormalizeBaseURL(base))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "connection_unavailable"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(u.String(), "/")+"/v8/management/"+path, nil)
	if err != nil {
		return "connection_unavailable"
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	res, err := client.Do(req)
	if err != nil {
		return "core_unavailable"
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return "core_auth_error"
	}
	if res.StatusCode == 404 {
		return "unsupported_contract"
	}
	if res.StatusCode != 200 {
		return "core_unavailable"
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 || json.Unmarshal(data, out) != nil {
		return "invalid_contract"
	}
	return ""
}
