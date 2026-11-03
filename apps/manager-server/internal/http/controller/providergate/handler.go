package providergate

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	svc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providergate"
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
		raw := r.URL.Query().Get("binding")
		var b svc.Binding
		d := json.NewDecoder(strings.NewReader(raw))
		d.DisallowUnknownFields()
		if len(raw) > 32<<10 || d.Decode(&b) != nil || d.Decode(new(any)) != io.EOF {
			err = svc.ErrInput
		} else {
			out, err = h.App.ProviderGateService.Status(ctx, b)
		}
	case http.MethodPut:
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		var q svc.Update
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if d.Decode(&q) != nil || d.Decode(new(any)) != io.EOF {
			err = svc.ErrInput
		} else {
			out, err = h.App.ProviderGateService.Update(ctx, q)
		}
	default:
		response.MethodNotAllowed(w)
		return
	}
	if err == nil {
		response.JSON(w, 200, out)
		return
	}
	code, reason := 503, "provider_gate_unavailable"
	if errors.Is(err, svc.ErrInput) {
		code, reason = 400, "invalid_provider_gate"
	}
	if errors.Is(err, svc.ErrIdentity) {
		code, reason = 409, "provider_identity_changed"
	}
	if errors.Is(err, store.ErrProviderGateConflict) {
		code, reason = 409, "provider_generation_conflict"
	}
	response.JSON(w, code, map[string]string{"error": reason})
}
