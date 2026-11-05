package officialbalance

import (
	"encoding/json"

	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// predecessorRecord keeps a saved observation visible after one saved target
// disappears, when exactly one stored source still shares the request.
// An exact current binding is handled by the normal read and never comes here.
func predecessorRecord(request Binding, records []store.OfficialBalanceRecord) (store.OfficialBalanceRecord, bool) {
	current := map[string]string{}
	for _, target := range request.Targets {
		current[target.ResourceID] = target.Incarnation
	}
	var found store.OfficialBalanceRecord
	count := 0
	for _, record := range records {
		if record.Config == "" || record.LastSuccess == "" || !predecessorMatch(request, current, record.Config) {
			continue
		}
		count++
		found = record
	}
	if count != 1 {
		return store.OfficialBalanceRecord{}, false
	}
	return found, true
}

func predecessorMatch(request Binding, current map[string]string, raw string) bool {
	var cfg Config
	if json.Unmarshal([]byte(raw), &cfg) != nil {
		return false
	}
	if cfg.Scope.InstanceID != request.Scope.InstanceID || cfg.Scope.InstanceIncarnation != request.Scope.InstanceIncarnation {
		return false
	}
	if len(cfg.Targets) == 0 {
		return false
	}
	overlap := 0
	for _, item := range cfg.Targets {
		incarnation, ok := current[item.ResourceID]
		if !ok {
			continue
		}
		if incarnation != item.Incarnation {
			return false
		}
		overlap++
	}
	return overlap > 0 && overlap < len(cfg.Targets)
}

func target(id, incarnation string) rc.Target {
	return rc.Target{ResourceID: id, Incarnation: incarnation}
}
