package credentialpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	svc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
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
	switch {
	case r.Method == "GET" && r.URL.Path == "/usage-service/credential-policy":
		if id := r.URL.Query().Get("subjectId"); id != "" {
			out, err = h.App.CredentialPolicyService.StoredStatus(ctx, id)
		} else {
			out, err = h.App.CredentialPolicyService.Status(ctx, r.URL.Query().Get("authIndex"))
		}
	case r.Method == "PUT" && r.URL.Path == "/usage-service/credential-policy":
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		var q svc.Update
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if d.Decode(&q) != nil || d.Decode(new(any)) != io.EOF {
			response.JSON(w, 400, map[string]string{"error": "invalid_policy"})
			return
		}
		out, err = h.App.CredentialPolicyService.Update(ctx, q)
	case r.Method == "POST" && r.URL.Path == "/usage-service/credential-policy/resume":
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		var q svc.ResumeRequest
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if d.Decode(&q) != nil || d.Decode(new(any)) != io.EOF {
			response.JSON(w, 400, map[string]string{"error": "invalid_policy"})
			return
		}
		out, err = h.App.CredentialPolicyService.Resume(ctx, q)
	default:
		response.MethodNotAllowed(w)
		return
	}
	if err == nil {
		response.JSON(w, 200, out)
		return
	}
	code, reason := 503, "credential_policy_unavailable"
	if errors.Is(err, svc.ErrIdentity) {
		code, reason = 409, "identity_changed"
	}
	if errors.Is(err, svc.ErrInput) {
		code, reason = 400, "invalid_policy"
	}
	if errors.Is(err, svc.ErrUnavailable) {
		reason = "gate_contract_unavailable"
		var unavailable *svc.UnavailableError
		if errors.As(err, &unavailable) {
			reason = unavailable.Reason
		}
	}
	if errors.Is(err, store.ErrPolicyConflict) {
		code, reason = 409, "policy_generation_conflict"
	}
	if errors.Is(err, store.ErrPolicyOperationConflict) {
		code, reason = 409, "policy_operation_conflict"
	}
	response.JSON(w, code, map[string]string{"error": reason})
}
