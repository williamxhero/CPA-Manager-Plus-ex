package credentialquota

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
	svc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
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
	var out any
	var err error
	switch r.Method {
	case "GET":
		if strings.HasPrefix(r.URL.Path, "/usage-service/credential-quota/jobs/") {
			out, err = h.App.CredentialQuotaService.Job(ctx, strings.TrimPrefix(r.URL.Path, "/usage-service/credential-quota/jobs/"))
		} else if index := r.URL.Query().Get("authIndex"); index != "" {
			out, err = h.App.CredentialQuotaService.Status(ctx, index)
		} else {
			var sources []svc.Source
			sources, err = h.App.CredentialQuotaService.Sources(ctx)
			out = map[string]any{"sources": sources}
		}
	case "POST":
		if r.URL.Path != "/usage-service/credential-quota" {
			response.MethodNotAllowed(w)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		var q svc.Query
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if d.Decode(&q) != nil || d.Decode(new(any)) != io.EOF {
			response.JSON(w, 400, map[string]string{"error": "invalid_query"})
			return
		}
		out, err = h.App.CredentialQuotaService.Start(ctx, q)
		if err == nil {
			response.JSON(w, 202, out)
			return
		}
	default:
		response.MethodNotAllowed(w)
		return
	}
	if err != nil {
		code := 500
		reason := "credential_quota_unavailable"
		if errors.Is(err, svc.ErrIdentity) {
			code = 409
			reason = "identity_changed"
		}
		if errors.Is(err, svc.ErrInput) {
			code = 400
			reason = "invalid_query"
		}
		if errors.Is(err, svc.ErrBusy) {
			code = 429
			reason = "query_busy"
		}
		if errors.Is(err, store.ErrQuotaConflict) {
			code = 409
			reason = "source_version_conflict"
		}
		response.JSON(w, code, map[string]string{"error": reason})
		return
	}
	response.JSON(w, 200, out)
}
