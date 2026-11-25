// Package providerautostate forwards Core's read-only automatic-pause projection
// to the panel. It never derives state of its own: every field the panel sees is
// either Core's value or an explicit failure.
package providerautostate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	managerconfigsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
)

// Stable machine codes shared with the panel. They describe the read failure
// itself, never a fabricated empty projection.
const (
	KindUnavailable = "provider_auto_state_unavailable"
	KindAuth        = "core_auth_error"
	KindUnreachable = "core_unavailable"
	KindInvalid     = "core_response_invalid"
)

const (
	corePath      = "/v8/management/inference-gates/auto-stop"
	coreBodyLimit = 2 << 20
	coreErrorSize = 4 << 10
)

// Projection is Core's envelope with its field names untouched. readiness and
// revision are kept as raw JSON so this service cannot re-interpret them.
type Projection struct {
	Version   int             `json:"version"`
	Ready     json.RawMessage `json:"ready"`
	Revision  json.RawMessage `json:"revision,omitempty"`
	Providers json.RawMessage `json:"providers"`
}

// Failure is a classified read failure. Kind is the stable code the panel maps to
// an HTTP status; Reason records the upstream cause verbatim.
type Failure struct {
	Kind   string
	Reason string
}

func (f *Failure) Error() string { return f.Kind + ": " + f.Reason }

type Service struct {
	connection *managerconfigsvc.Service
	timeout    time.Duration
}

func New(connection *managerconfigsvc.Service) *Service {
	return &Service{connection: connection, timeout: 15 * time.Second}
}

// Read returns Core's automatic-pause projection for the saved connection.
func (s *Service) Read(ctx context.Context) (Projection, error) {
	if s == nil || s.connection == nil {
		return Projection{}, &Failure{KindUnreachable, "connection_unconfigured"}
	}
	cfg, _, _, err := s.connection.ResolveManagerConfigWithSource(ctx)
	if err != nil {
		return Projection{}, &Failure{KindUnreachable, "connection_unavailable"}
	}
	key := strings.TrimSpace(cfg.CPAConnection.ManagementKey)
	u, err := url.Parse(cpa.NormalizeBaseURL(cfg.CPAConnection.CPABaseURL))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || key == "" {
		return Projection{}, &Failure{KindUnreachable, "connection_unconfigured"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(u.String(), "/")+corePath, nil)
	if err != nil {
		return Projection{}, &Failure{KindUnreachable, "connection_unconfigured"}
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: s.timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	res, err := client.Do(req)
	if err != nil {
		reason := "core_unreachable"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			reason = "core_timeout"
		}
		return Projection{}, &Failure{KindUnreachable, reason}
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		// Core rejected Manager's management key. This is an upstream fault, not
		// the panel's own authorization failure.
		return Projection{}, &Failure{KindAuth, "core_auth_error"}
	case res.StatusCode == http.StatusServiceUnavailable:
		return Projection{}, &Failure{KindUnavailable, coreErrorReason(res.Body)}
	case res.StatusCode != http.StatusOK:
		return Projection{}, &Failure{KindUnreachable, fmt.Sprintf("core_status_%d", res.StatusCode)}
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, coreBodyLimit+1))
	if err != nil || len(data) > coreBodyLimit {
		return Projection{}, &Failure{KindInvalid, "unreadable_body"}
	}
	return decode(data)
}

// coreErrorReason preserves Core's own error code when it publishes one.
func coreErrorReason(body io.Reader) string {
	const fallback = "gate_unavailable"
	var envelope struct {
		Error string `json:"error"`
	}
	data, err := io.ReadAll(io.LimitReader(body, coreErrorSize))
	if err != nil || json.Unmarshal(data, &envelope) != nil || envelope.Error == "" {
		return fallback
	}
	return envelope.Error
}

func decode(data []byte) (Projection, error) {
	var envelope Projection
	if json.Unmarshal(data, &envelope) != nil {
		return Projection{}, &Failure{KindInvalid, "invalid_json"}
	}
	if envelope.Version != 1 {
		return Projection{}, &Failure{KindInvalid, "unsupported_contract_version"}
	}
	ready := bytes.TrimSpace(envelope.Ready)
	if string(ready) != "true" && string(ready) != "false" {
		return Projection{}, &Failure{KindInvalid, "ready_missing"}
	}
	// "Not ready" is an upstream fault. Forwarding it would let the panel read a
	// stale page as if Core had cleared every automatic pause.
	if string(ready) != "true" {
		return Projection{}, &Failure{KindInvalid, "ready_not_true"}
	}
	providers := bytes.TrimSpace(envelope.Providers)
	if len(providers) == 0 || providers[0] != '[' {
		return Projection{}, &Failure{KindInvalid, "providers_missing"}
	}
	if reason := validateProviders(providers); reason != "" {
		return Projection{}, &Failure{KindInvalid, reason}
	}
	return Projection{
		Version:   envelope.Version,
		Ready:     ready,
		Revision:  bytes.TrimSpace(envelope.Revision),
		Providers: providers,
	}, nil
}

// Provider states Core may publish. Anything else would reach the panel as an
// unrecognised state and silently render as available.
var (
	validProviderStates = map[string]bool{"available": true, "auto_paused": true, "partially_auto_paused": true}
	validMemberStates   = map[string]bool{"paused": true, "available": true}
)

// validateProviders rejects a projection whose members the panel could only
// guess at. It returns the specific reason, or "" when the projection is
// readable end to end.
func validateProviders(providers json.RawMessage) string {
	var entries []json.RawMessage
	if json.Unmarshal(providers, &entries) != nil {
		return "providers_missing"
	}
	for _, entry := range entries {
		var fields map[string]json.RawMessage
		if json.Unmarshal(entry, &fields) != nil {
			return "provider_resource_id_missing"
		}
		if strings.TrimSpace(stringField(fields["resourceId"])) == "" {
			return "provider_resource_id_missing"
		}
		if !validProviderStates[stringField(fields["state"])] {
			return "provider_state_invalid"
		}
		members, present := fields["members"]
		if !present || bytes.Equal(bytes.TrimSpace(members), []byte("null")) {
			continue
		}
		var entries []json.RawMessage
		if json.Unmarshal(members, &entries) != nil {
			return "member_state_invalid"
		}
		for _, member := range entries {
			var memberFields map[string]json.RawMessage
			if json.Unmarshal(member, &memberFields) != nil {
				return "member_state_invalid"
			}
			if !validMemberStates[stringField(memberFields["state"])] {
				return "member_state_invalid"
			}
		}
	}
	return ""
}

// stringField reads a JSON string field, treating any other shape as absent.
func stringField(raw json.RawMessage) string {
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}
