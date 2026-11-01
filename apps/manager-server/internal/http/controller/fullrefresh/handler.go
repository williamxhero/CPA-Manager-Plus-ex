package fullrefresh

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	svc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	"io"
	"net/http"
	"time"
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
	var out any
	var err error
	code := 200
	switch r.Method {
	case "GET":
		if id := r.URL.Query().Get("id"); id != "" {
			out, err = h.App.FullRefreshService.GetProgress(ctx, id)
		} else {
			out, err = h.App.FullRefreshService.Inventory(ctx)
		}
	case "POST":
		var req struct {
			Purpose svc.Purpose        `json:"purpose"`
			Source  *svc.SourceRequest `json:"source,omitempty"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if d.Decode(&req) != nil || d.Decode(new(any)) != io.EOF {
			response.JSON(w, 400, map[string]string{"error": "invalid_refresh"})
			return
		}
		if req.Source == nil {
			out, err = h.App.FullRefreshService.StartRound(ctx, req.Purpose)
		} else {
			out, err = h.App.FullRefreshService.StartSingleSource(ctx, *req.Source, req.Purpose)
		}
		code = 202
	case "DELETE":
		out, err = h.App.FullRefreshService.Cancel(ctx, r.URL.Query().Get("id"))
	default:
		response.MethodNotAllowed(w)
		return
	}
	if err != nil {
		code = 503
		reason := "inventory_unavailable"
		if errors.Is(err, svc.ErrInput) {
			code = 400
			reason = "invalid_refresh"
		}
		response.JSON(w, code, map[string]string{"error": reason})
		return
	}
	response.JSON(w, code, out)
}
