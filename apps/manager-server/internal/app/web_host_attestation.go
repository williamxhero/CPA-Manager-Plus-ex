package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	officialbalancesvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// Optional host-only attestation file (never management HTTP). Shape matches
// docs/verification/issue97-live-20261104/host-attestation.example.json.
func loadHostWebBrowserAttestation(ctx context.Context, svc *officialbalancesvc.Service) error {
	path := strings.TrimSpace(os.Getenv("CPAMP_WEB_BROWSER_ATTESTATION"))
	if path == "" || svc == nil {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read CPAMP_WEB_BROWSER_ATTESTATION: %w", err)
	}
	var body struct {
		ProofReference string `json:"proofReference"`
		Family         string `json:"family"`
		Profile        string `json:"profile"`
		InstanceID     string `json:"instanceId"`
		ExecutionUser  string `json:"executionUser"`
		EvidenceKind   string `json:"evidenceKind"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return fmt.Errorf("parse CPAMP_WEB_BROWSER_ATTESTATION: %w", err)
	}
	return svc.AttestWebBrowserHost(ctx, store.WebBrowserHostAttestation{
		ProofReference: body.ProofReference,
		Family:         body.Family,
		Profile:        body.Profile,
		InstanceID:     body.InstanceID,
		ExecutionUser:  body.ExecutionUser,
		EvidenceKind:   body.EvidenceKind,
	})
}
