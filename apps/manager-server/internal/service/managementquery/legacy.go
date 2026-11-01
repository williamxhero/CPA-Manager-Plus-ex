package managementquery

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
)

var ErrSharedEntryRequired = errors.New("use shared scoped management refresh")
var ErrBusy = errors.New("management query busy")
var ErrLegacyModeUnverified = errors.New("legacy management mode unverified")

// LegacyGuard is startup wiring for old quota-only callers. Current v8 mode
// must use issued source jobs; older servers remain explicitly legacy/unprotected
// and still consume the SAME global admission budget. No inference path uses it.
type LegacyGuard struct {
	Budget   *Budget
	Identity *rc.Service
}

// CheckTarget authorizes legacy-only work, never a scoped fallback. Inventory
// errors are not legacy evidence. Require an actual404 on the exact mutation
// target too: unsupported versions and stale runtime targets must fail closed.
func (g *LegacyGuard) CheckTarget(ctx context.Context, baseURL, key string) error {
	operation, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := g.checkMode(operation); err != nil {
		return err
	}
	u, err := url.Parse(cpa.NormalizeBaseURL(baseURL))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "http" && u.Scheme != "https" || strings.TrimSpace(key) == "" {
		return ErrLegacyModeUnverified
	}
	req, err := http.NewRequestWithContext(operation, "GET", strings.TrimRight(u.String(), "/")+"/v8/management/resource-capabilities", nil)
	if err != nil {
		return ErrLegacyModeUnverified
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrLegacyModeUnverified }}
	response, err := client.Do(req)
	if err != nil {
		return ErrLegacyModeUnverified
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		return ErrLegacyModeUnverified
	}
	// A late negative response from an old target cannot authorize work after
	// the current saved connection/mode changes. Inventory also fences changes
	// during this final read, including its own negative responses.
	return g.checkMode(operation)
}

func (g *LegacyGuard) checkMode(ctx context.Context) error {
	if g == nil || g.Identity == nil {
		return ErrLegacyModeUnverified
	}
	n := g.Identity.Inventory(ctx)
	if n.ContractVersion == 1 {
		return ErrSharedEntryRequired
	}
	if n.Identity.Reason != "unsupported_contract" {
		return ErrLegacyModeUnverified
	}
	return nil
}

// Begin requires the caller's resolved management target, so unsupported
// positive capability responses can never become untargeted legacy admission.
func (g *LegacyGuard) Begin(ctx context.Context, baseURL, key string) (context.Context, func(), error) {
	operation, cancel := context.WithTimeout(ctx, 15*time.Second)
	err := g.CheckTarget(operation, baseURL, key)
	if err != nil || g.Budget == nil {
		cancel()
		if err == nil {
			err = ErrLegacyModeUnverified
		}
		return nil, nil, err
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
