package managementquery

import (
	"context"
	"errors"
	"github.com/google/uuid"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"time"
)

var ErrSharedEntryRequired = errors.New("use shared scoped management refresh")
var ErrBusy = errors.New("management query busy")

// LegacyGuard is startup wiring for old quota-only callers. Current v8 mode
// must use issued source jobs; older servers remain explicitly legacy/unprotected
// and still consume the SAME global admission budget. No inference path uses it.
type LegacyGuard struct {
	Budget   *Budget
	Identity *rc.Service
}

func (g *LegacyGuard) Begin(ctx context.Context) (context.Context, func(), error) {
	operation, cancel := context.WithTimeout(ctx, 15*time.Second)
	n := g.Identity.Inventory(operation)
	if n.ContractVersion == 1 {
		cancel()
		return nil, nil, ErrSharedEntryRequired
	}
	id := uuid.NewString()
	parent, ok := g.Budget.TryAcquire(id)
	if !ok {
		cancel()
		return nil, nil, ErrBusy
	}
	stop := context.AfterFunc(parent, cancel)
	return operation, func() { stop(); cancel(); g.Budget.Release(id) }, nil
}
