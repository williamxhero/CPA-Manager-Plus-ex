package credentialpolicy

import (
	"context"
	"encoding/json"

	a "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy/association"
)

func (c coreClient) associate(ctx context.Context, request a.Request) (a.Result, string) {
	var raw json.RawMessage
	if reason := c.requestAt(ctx, "credential-quota/credential-association", "POST", "", request, &raw); reason != "" {
		return a.Result{}, reason
	}
	result, err := a.DecodeResult(raw)
	if err != nil {
		return a.Result{}, "association_contract_invalid"
	}
	return result, ""
}
