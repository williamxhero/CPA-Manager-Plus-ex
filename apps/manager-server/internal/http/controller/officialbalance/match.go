package officialbalance

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/balancematch"
	svc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func (h *Handler) Match(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	middleware.WriteCORS(h.App.Config, w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !middleware.AuthorizePanel(w, r, h.App.AdminAuthService) {
		return
	}
	if r.Method != http.MethodPost {
		response.MethodNotAllowed(w)
		return
	}
	var req balancematch.Request
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil || d.Decode(new(any)) != io.EOF || len(req.Binding.Targets) == 0 {
		response.JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_binding"})
		return
	}
	out, err := h.App.BalanceMatcher.Match(r.Context(), req)
	if err == nil {
		response.JSON(w, http.StatusOK, out)
		return
	}
	code, reason := http.StatusInternalServerError, "balance_unavailable"
	switch {
	case errors.Is(err, svc.ErrIdentity):
		code, reason = http.StatusConflict, "identity_changed"
	case errors.Is(err, store.ErrBalanceConflict):
		code, reason = http.StatusConflict, "configuration_conflict"
	case errors.Is(err, svc.ErrInput):
		code, reason = http.StatusBadRequest, "invalid_configuration"
	}
	response.JSON(w, code, map[string]string{"error": reason})
}
