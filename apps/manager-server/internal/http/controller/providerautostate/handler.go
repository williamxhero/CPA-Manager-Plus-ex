package providerautostate

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	svc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providerautostate"
)

type Handler struct{ App *app.Context }

// Handle exposes Core's automatic-pause projection as a read-only panel view.
func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	middleware.WriteCORS(h.App.Config, w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, OPTIONS")
		response.MethodNotAllowed(w)
		return
	}
	if !middleware.AuthorizePanel(w, r, h.App.AdminAuthService) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	projection, err := h.App.ProviderAutoStateService.Read(ctx)
	if err != nil {
		status, body := failureResponse(err)
		response.JSON(w, status, body)
		return
	}
	response.JSON(w, http.StatusOK, projection)
}

// failureResponse maps one classified read failure to one stable panel status.
// A Core authorization failure stays an upstream gateway error so the panel never
// mistakes it for its own 401.
func failureResponse(err error) (int, map[string]string) {
	var failure *svc.Failure
	if !errors.As(err, &failure) {
		return http.StatusServiceUnavailable, map[string]string{"error": svc.KindUnavailable, "reason": svc.KindUnavailable}
	}
	status := http.StatusServiceUnavailable
	if failure.Kind == svc.KindInvalid || failure.Kind == svc.KindAuth {
		status = http.StatusBadGateway
	}
	return status, map[string]string{"error": failure.Kind, "reason": failure.Reason}
}
