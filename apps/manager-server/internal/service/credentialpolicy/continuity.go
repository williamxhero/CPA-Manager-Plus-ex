package credentialpolicy

import (
	"context"
	"reflect"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
)

type ContinuityReceipt struct {
	Version int    `json:"version"`
	Proven  bool   `json:"proven"`
	Reason  string `json:"reason"`
	Kind    string `json:"kind"`
	cq.Binding
	PreviousTargets []rc.Target `json:"previousTargets"`
	AuthIndex       string      `json:"auth_index"`
	SourceVersion   string      `json:"sourceVersion"`
}

func (r ContinuityReceipt) Confirms(previous cq.Binding, current cq.Source) bool {
	return r.Version == 1 && r.Proven && r.Kind == "native_oauth_refresh" && r.Reason == "certified_native_refresh_chain" && len(previous.Targets) > 0 && len(current.Targets) > 0 && previous.Scope.InstanceID == current.Scope.InstanceID && previous.Scope.InstanceIncarnation == current.Scope.InstanceIncarnation && reflect.DeepEqual(r.Binding, current.Binding) && reflect.DeepEqual(r.PreviousTargets, previous.Targets) && r.AuthIndex == current.AuthIndex && r.SourceVersion == current.SourceVersion
}
func (c coreClient) continuity(ctx context.Context, previous cq.Binding, current cq.Source) (bool, string) {
	request := struct {
		Version       int        `json:"version"`
		Previous      cq.Binding `json:"previous"`
		Current       cq.Binding `json:"current"`
		AuthIndex     string     `json:"auth_index"`
		SourceVersion string     `json:"sourceVersion"`
	}{1, previous, current.Binding, current.AuthIndex, current.SourceVersion}
	var receipt ContinuityReceipt
	if reason := c.request(ctx, "POST", "continuity", request, &receipt); reason != "" {
		return false, reason
	}
	if !receipt.Confirms(previous, current) {
		return false, "continuity_unproven"
	}
	return true, ""
}
