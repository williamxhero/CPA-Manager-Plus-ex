package runtimecontrols

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/setting"
)

type Handler struct{ App *app.Context }

func (h *Handler) load(ctx context.Context) (setting.RuntimeControls, error) {
	controls, err := h.App.Store.LoadRuntimeControls(ctx)
	if err != nil || h.App.OfficialBalanceService == nil {
		return controls, err
	}
	h.App.OfficialBalanceService.RestoreCachedBalances(ctx, controls.BalanceTexts)
	return controls, nil
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	middleware.WriteCORS(h.App.Config, w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !middleware.AuthorizePanel(w, r, h.App.AdminAuthService) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	switch r.Method {
	case http.MethodGet:
		controls, err := h.load(ctx)
		if err != nil {
			response.JSON(w, http.StatusServiceUnavailable, map[string]string{"error": "runtime_controls_unavailable"})
			return
		}
		response.JSON(w, http.StatusOK, controls)
	case http.MethodPut:
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		var controls setting.RuntimeControls
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&controls) != nil || decoder.Decode(new(any)) != io.EOF {
			response.JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_runtime_controls"})
			return
		}
		if err := h.App.Store.SaveRuntimeControls(ctx, controls); err != nil {
			response.JSON(w, http.StatusServiceUnavailable, map[string]string{"error": "runtime_controls_unavailable"})
			return
		}
		saved, err := h.load(ctx)
		if err != nil {
			response.JSON(w, http.StatusServiceUnavailable, map[string]string{"error": "runtime_controls_unavailable"})
			return
		}
		response.JSON(w, http.StatusOK, saved)
	default:
		response.MethodNotAllowed(w)
	}
}
