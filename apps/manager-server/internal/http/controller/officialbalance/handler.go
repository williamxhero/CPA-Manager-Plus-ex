package officialbalance

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
	svc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type Handler struct{ App *app.Context }

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	middleware.WriteCORS(h.App.Config, w, r)
	if r.Method == "OPTIONS" {
		w.WriteHeader(204)
		return
	}
	if !middleware.AuthorizePanel(w, r, h.App.AdminAuthService) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	var out any
	var err error
	switch r.Method {
	case "PUT":
		var u svc.Update
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if d.Decode(&u) != nil || d.Decode(new(any)) != io.EOF {
			response.JSON(w, 400, map[string]string{"error": "invalid_configuration"})
			return
		}
		out, err = h.App.OfficialBalanceService.Configure(ctx, u)
	case "POST":
		var q svc.Query
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if d.Decode(&q) != nil || d.Decode(new(any)) != io.EOF {
			response.JSON(w, 400, map[string]string{"error": "invalid_query"})
			return
		}
		out, err = h.App.FullRefreshService.StartOfficial(ctx, q)
		if err == nil {
			response.JSON(w, 202, out)
			return
		}
	case "GET":
		if id := r.URL.Query().Get("job"); id != "" {
			out, err = h.App.OfficialBalanceService.Job(ctx, id)
			break
		}
		var b svc.Binding
		if json.Unmarshal([]byte(r.URL.Query().Get("binding")), &b) != nil {
			response.JSON(w, 400, map[string]string{"error": "invalid_binding"})
			return
		}
		out, err = h.App.OfficialBalanceService.Status(ctx, b)
	default:
		response.MethodNotAllowed(w)
		return
	}
	if err != nil {
		code := 500
		reason := "balance_unavailable"
		if errors.Is(err, svc.ErrBusy) {
			code = 429
			reason = "query_busy"
		}
		if errors.Is(err, store.ErrBalanceConflict) {
			code = 409
			reason = "configuration_conflict"
		}
		if errors.Is(err, svc.ErrIdentity) {
			code = 409
			reason = "identity_changed"
		}
		if errors.Is(err, svc.ErrInput) {
			code = 400
			reason = "invalid_configuration"
		}
		response.JSON(w, code, map[string]string{"error": reason})
		return
	}
	response.JSON(w, 200, out)
}
