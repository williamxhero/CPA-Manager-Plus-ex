//go:build !cpa_entry_fixture

package app

import (
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
)

// Ordinary builds keep the service-installed LAYSO parser registry and host
// attestation Enrollment from officialbalance.New. This hook only supplies the
// tagged entry-fixture WebReader/clock; HTTP still cannot invent enrollment.
func managementRuntimeDependencies() (balance.WebReader, fullrefresh.Clock) {
	return nil, nil
}
