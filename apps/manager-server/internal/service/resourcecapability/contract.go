package resourcecapability

import (
	"encoding/json"
	"errors"
	"regexp"
)

// Scope and Target mirror Core catalog v1 preconditions, never routing ACKs.
// Keep counters as integers; consumers must carry the entire tuple unchanged.
type Scope struct {
	InstanceID          string `json:"instance_id"`
	InstanceIncarnation string `json:"instance_incarnation"`
	BootEpoch           string `json:"boot_epoch"`
	Revision            uint64 `json:"snapshot_revision"`
}
type Target struct {
	ResourceID        string `json:"resource_id"`
	Incarnation       string `json:"incarnation"`
	IdentityVersion   uint64 `json:"identity_version"`
	CredentialVersion uint64 `json:"credential_version"`
	ConfigVersion     uint64 `json:"config_version"`
	MembershipVersion uint64 `json:"membership_version"`
}
type Row struct {
	Target
	SourceBindings []Target `json:"source_bindings,omitempty"`
	BindingState   string   `json:"binding_state,omitempty"`
	Kind           string   `json:"kind"`
	Family         string   `json:"family"`
	ParentID       string   `json:"parent_id,omitempty"`
	Members        []string `json:"members,omitempty"`
	Manual         struct {
		Disabled   bool `json:"disabled"`
		Excluded   bool `json:"excluded"`
		ZeroWeight bool `json:"zero_weight"`
	} `json:"manual"`
	Identity     string `json:"identity"`
	Readiness    string `json:"catalog_readiness"`
	Routing      string `json:"runtime_readiness"`
	Capabilities struct {
		Inventory bool `json:"inventory"`
		Query     bool `json:"query"`
		Gate      bool `json:"gate"`
	} `json:"capabilities"`
}
type Snapshot struct {
	ContractVersion int `json:"version"`
	Scope
	Complete  bool  `json:"complete"`
	Resources []Row `json:"resources"`
}

var opaqueID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validCounter(n uint64, allowZero bool) bool {
	return (allowZero || n > 0) && n <= 9007199254740991
}
func (s Scope) valid() bool {
	return opaqueID.MatchString(s.InstanceID) && opaqueID.MatchString(s.InstanceIncarnation) && opaqueID.MatchString(s.BootEpoch) && validCounter(s.Revision, false)
}
func oneOf(s string, allowed ...string) bool {
	for _, a := range allowed {
		if s == a {
			return true
		}
	}
	return false
}
func (s Snapshot) valid() bool {
	if !s.Scope.valid() || s.Resources == nil || len(s.Resources) > 10000 {
		return false
	}
	seen := map[string]bool{}
	rows := map[string]Row{}
	for _, row := range s.Resources {
		if !oneOf(row.Kind, "provider", "credential") || !oneOf(row.Family, "gemini", "interactions", "vertex", "codex", "claude", "xai", "meta", "openai-compatibility", "antigravity", "gemini-cli", "qwen", "opencode-go", "iflow", "kimi", "kiro", "devin", "unsupported") || !oneOf(row.Identity, "valid", "ambiguous") || !oneOf(row.Readiness, "ready", "not_ready", "unsupported") || row.Routing != "unknown" {
			return false
		}
		if row.ParentID != "" && !opaqueID.MatchString(row.ParentID) {
			return false
		}
		for _, member := range row.Members {
			if !opaqueID.MatchString(member) {
				return false
			}
		}
		if !oneOf(row.BindingState, "", "missing_credential", "standalone", "unmaterialized", "verified_projection", "ambiguous_projection") {
			return false
		}
		// Core can retain unproven rows without a target; do not invent a binding.
		if row.Identity == "ambiguous" && row.Readiness == "not_ready" && row.Target == (Target{}) {
			if len(row.SourceBindings) != 0 {
				return false
			}
			continue
		}
		if !opaqueID.MatchString(row.ResourceID) || !opaqueID.MatchString(row.Incarnation) || seen[row.ResourceID] || !validCounter(row.IdentityVersion, false) || !validCounter(row.CredentialVersion, false) || !validCounter(row.ConfigVersion, false) || !validCounter(row.MembershipVersion, true) {
			return false
		}
		seen[row.ResourceID] = true
		rows[row.ResourceID] = row
		if row.ParentID != "" && !opaqueID.MatchString(row.ParentID) {
			return false
		}
		for _, member := range row.Members {
			if !opaqueID.MatchString(member) {
				return false
			}
		}
	}
	for _, row := range rows {
		for _, binding := range row.SourceBindings {
			peer, ok := rows[binding.ResourceID]
			if !ok || peer.Target != binding || peer.Family != row.Family || peer.ResourceID == row.ResourceID {
				return false
			}
		}

		if row.ParentID != "" {
			parent, ok := rows[row.ParentID]
			if !ok || row.Kind != "credential" || parent.Kind != "provider" || parent.Family != row.Family {
				return false
			}
			count := 0
			for _, member := range parent.Members {
				if member == row.ResourceID {
					count++
				}
			}
			if count != 1 {
				return false
			}
		}
		members := map[string]bool{}
		for _, id := range row.Members {
			child, ok := rows[id]
			if !ok || row.Kind != "provider" || child.ParentID != row.ResourceID || members[id] {
				return false
			}
			members[id] = true
		}
	}

	return true
}

// Manual-state omission/null must not silently become an enabled credential.
func (r *Row) UnmarshalJSON(data []byte) error {
	type plain Row
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var manual map[string]json.RawMessage
	if err := json.Unmarshal(raw["manual"], &manual); err != nil {
		return errors.New("invalid catalog row")
	}
	for _, key := range []string{"disabled", "excluded", "zero_weight"} {
		value := string(manual[key])
		if value != "true" && value != "false" {
			return errors.New("invalid catalog manual state")
		}
	}
	*r = Row(decoded)
	return nil
}

// RuntimeBinding joins a current-runtime selector to host-issued targets only.
// AuthIndex is not durable identity and is never used without the full Scope.
type RuntimeBinding struct {
	AuthIndex    string   `json:"auth_index"`
	RuntimeAlias string   `json:"runtime_alias"`
	Scope        Scope    `json:"scope"`
	Targets      []Target `json:"targets"`
}

var runtimeIndex = regexp.MustCompile(`^[0-9a-f]{16}$`)

func validBindings(snapshot Snapshot, bindings []RuntimeBinding) bool {
	if bindings == nil || len(bindings) > 10000 {
		return false
	}
	rows := map[string]Row{}
	for _, row := range snapshot.Resources {
		rows[row.ResourceID] = row
	}
	seen := map[string]bool{}
	for _, binding := range bindings {
		if binding.Scope != snapshot.Scope || !runtimeIndex.MatchString(binding.AuthIndex) || seen[binding.AuthIndex] || len(binding.Targets) == 0 || binding.RuntimeAlias != binding.Targets[0].ResourceID {
			return false
		}
		seen[binding.AuthIndex] = true
		targets := map[string]bool{}
		for _, target := range binding.Targets {
			row, ok := rows[target.ResourceID]
			if !ok || row.Target != target || row.Kind != "credential" || row.Identity != "valid" || row.Readiness != "ready" || targets[target.ResourceID] {
				return false
			}
			targets[target.ResourceID] = true
		}
		// Only the producer's proven file/config projection may add counterpart targets.
		primary := rows[binding.RuntimeAlias]
		if len(binding.Targets) != 1+len(primary.SourceBindings) {
			return false
		}
		for _, target := range primary.SourceBindings {
			if !targets[target.ResourceID] {
				return false
			}
		}
	}
	return true
}
