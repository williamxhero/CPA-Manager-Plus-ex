package fullrefresh

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"sort"
)

// Internal source-group locator hashes ONLY host-issued resource/incarnation
// tuples. It is never a credential hash, label, row index or a policy identity.
func sourceID(kind Kind, scope rc.Scope, targets []rc.Target) string {
	type tuple struct{ ID, Incarnation string }
	group := []tuple{}
	for _, t := range targets {
		group = append(group, tuple{t.ResourceID, t.Incarnation})
	}
	sort.Slice(group, func(i, j int) bool { return group[i].ID < group[j].ID })
	raw, _ := json.Marshal(struct {
		Instance, Incarnation string
		Targets               []tuple
	}{scope.InstanceID, scope.InstanceIncarnation, group})
	hash := sha256.Sum256(raw)
	return string(kind) + ":" + hex.EncodeToString(hash[:])
}
