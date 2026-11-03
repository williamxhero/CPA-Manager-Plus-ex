package providerpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	svc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providerpolicy"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type Handler struct{ App *app.Context }

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	middleware.WriteCORS(h.App.Config, w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(204)
		return
	}
	if !middleware.AuthorizePanel(w, r, h.App.AdminAuthService) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var out svc.Status
	var err error
	switch r.Method {
	case http.MethodGet:
		out, err = h.App.ProviderPolicyService.Status(ctx, r.URL.Query().Get("providerId"))
	case http.MethodPut:
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		var q svc.Update
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if d.Decode(&q) != nil || d.Decode(new(any)) != io.EOF {
			err = svc.ErrInput
		} else {
			out, err = h.App.ProviderPolicyService.Update(ctx, q)
		}
	default:
		response.MethodNotAllowed(w)
		return
	}
	if err == nil {
		response.JSON(w, 200, out)
		return
	}
	code, reason := 503, "provider_policy_unavailable"
	if errors.Is(err, svc.ErrInput) {
		code, reason = 400, "invalid_provider_policy"
	}
	if errors.Is(err, svc.ErrIdentity) {
		code, reason = 409, "provider_identity_changed"
	}
	if errors.Is(err, store.ErrProviderPolicyConflict) {
		code, reason = 409, "provider_policy_generation_conflict"
	}
	response.JSON(w, code, map[string]string{"error": reason})
}
