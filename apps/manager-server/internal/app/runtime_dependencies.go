//go:build !cpa_entry_fixture

package app

import (
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
)

// Ordinary builds retain the production defaults: no unverified web parser or
// enrollment, and the real scheduler clock. Runtime input cannot enable fixtures.
func managementRuntimeDependencies() (balance.WebReader, fullrefresh.Clock) {
	return nil, nil
}
