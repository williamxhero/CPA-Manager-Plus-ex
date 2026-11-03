package providergate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	mc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

var errOperationMissing = errors.New("provider operation not found")

// One immutable saved connection is used for the complete read/write/readback
// exchange. Neither a caller URL nor a redirected management request is allowed.
type session struct {
	service *mc.Service
	config  store.ManagerConfig
	base    string
}

func openSession(ctx context.Context, service *mc.Service) (session, error) {
	if service == nil {
		return session{}, ErrUnavailable
	}
	cfg, _, _, err := service.ResolveManagerConfigWithSource(ctx)
	if err != nil {
		return session{}, ErrUnavailable
	}
	u, err := url.Parse(cpa.NormalizeBaseURL(cfg.CPAConnection.CPABaseURL))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "http" && u.Scheme != "https" || cfg.CPAConnection.ManagementKey == "" {
		return session{}, ErrUnavailable
	}
	return session{service, cfg, strings.TrimRight(u.String(), "/") + "/v8/management/inference-gates"}, nil
}

func (s session) current(ctx context.Context) bool {
	cfg, _, _, err := s.service.ResolveManagerConfigWithSource(ctx)
	return err == nil && cfg.CPAConnection == s.config.CPAConnection && cfg.UpdatedAtMS == s.config.UpdatedAtMS
}

func (s session) request(ctx context.Context, method, path string, body, out any) error {
	if !s.current(ctx) {
		return ErrIdentity
	}
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return ErrInput
		}
	}
	endpoint := s.base
	if path != "" {
		endpoint += "/" + path
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(data))
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+s.config.CPAConnection.ManagementKey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	res, err := client.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer res.Body.Close()
	if !s.current(ctx) {
		return ErrIdentity
	}
	if res.StatusCode == 409 {
		return ErrIdentity
	}
	if res.StatusCode == 404 && method == "GET" && strings.HasPrefix(path, "operations/") {
		return errOperationMissing
	}
	if res.StatusCode != 200 {
		return ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 || json.Unmarshal(raw, out) != nil {
		return ErrUnavailable
	}
	if !s.current(ctx) {
		return ErrIdentity
	}
	return nil
}
