package providergate

import (
	"context"
	"reflect"

	"github.com/google/uuid"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
)

type RuntimeMember struct {
	Scope     rc.Scope    `json:"scope"`
	Targets   []rc.Target `json:"targets"`
	AuthIndex string      `json:"auth_index"`
}

func (s *Service) RuntimeMembers(ctx context.Context, b Binding) (Status, []RuntimeMember, error) {
	out, err := s.prepare(ctx, b)
	if err != nil {
		return out.status, nil, err
	}
	members := make([]RuntimeMember, 0, len(out.proof.Members))
	for _, m := range out.proof.Members {
		members = append(members, RuntimeMember{Scope: m.Scope, Targets: append([]rc.Target(nil), m.Targets...), AuthIndex: m.AuthIndex})
	}
	return out.status, members, nil
}

// RuntimeKeyVerification is an ephemeral read-only envelope. It is never cached
// or accepted as an operation permit. Call Close after every source has been
// checked and before consuming any result.
type RuntimeKeyVerification struct {
	service  *Service
	prepared prepared
}

func (s *Service) OpenRuntimeKeyVerification(ctx context.Context, b Binding) (*RuntimeKeyVerification, error) {
	out, err := s.prepare(ctx, b)
	if err != nil {
		return nil, err
	}
	return &RuntimeKeyVerification{service: s, prepared: out}, nil
}
func (v *RuntimeKeyVerification) Status() Status            { return v.prepared.status }
func (v *RuntimeKeyVerification) Inventory() *rc.Negotiated { return v.prepared.inventory }
func (v *RuntimeKeyVerification) Members() []RuntimeMember {
	out := make([]RuntimeMember, 0, len(v.prepared.proof.Members))
	for _, m := range v.prepared.proof.Members {
		out = append(out, RuntimeMember{Scope: m.Scope, Targets: append([]rc.Target(nil), m.Targets...), AuthIndex: m.AuthIndex})
	}
	return out
}
func (v *RuntimeKeyVerification) Close(ctx context.Context) error {
	closing := v.service.identity.Inventory(ctx, true)
	if _, err := members(v.prepared.status.Binding, closing); err != nil || !v.service.identity.SameInventoryConnection(v.prepared.inventory, closing) || !v.prepared.core.current(ctx) {
		return ErrIdentity
	}
	return nil
}
func (v *RuntimeKeyVerification) Match(ctx context.Context, index, nonce, digest string) (bool, error) {
	if uuid.Validate(nonce) != nil || !validDigest(digest) {
		return false, ErrInput
	}
	out := v.prepared
	b := out.status.Binding
	sourceVersion := out.status.SourceVersion
	found := false
	for _, m := range out.proof.Members {
		found = found || m.AuthIndex == index
	}
	if !found {
		return false, ErrIdentity
	}
	request := struct {
		groupBinding
		Version       int    `json:"version"`
		SourceVersion string `json:"sourceVersion"`
		AuthIndex     string `json:"auth_index"`
		Adapter       string `json:"adapter"`
		Nonce         string `json:"nonce"`
		Digest        string `json:"digest"`
	}{coreGroup(canonical(b)), 1, sourceVersion, index, "deepseek", nonce, digest}
	var reply struct {
		groupBinding
		Version       int    `json:"version"`
		SourceVersion string `json:"sourceVersion"`
		AuthIndex     string `json:"auth_index"`
		Adapter       string `json:"adapter"`
		Nonce         string `json:"nonce"`
		Authority     string `json:"authority"`
		Matched       bool   `json:"matched"`
	}
	if err := out.core.request(ctx, "POST", "providers/key-match", request, &reply); err != nil {
		return false, err
	}
	if reply.Version != 1 || reply.SourceVersion != sourceVersion || reply.AuthIndex != index || reply.Nonce != nonce || reply.Adapter != "deepseek" || reply.Authority != "runtime_key_match_only" || !reflect.DeepEqual(reply.groupBinding, request.groupBinding) {
		return false, ErrUnavailable
	}
	return reply.Matched, nil
}

// MatchQueryKey proves only current runtime-key equality, not monetary scope.
func (s *Service) MatchQueryKey(ctx context.Context, b Binding, sourceVersion, index, nonce, digest string) (bool, error) {
	v, err := s.OpenRuntimeKeyVerification(ctx, b)
	if err != nil {
		return false, err
	}
	if v.Status().SourceVersion != sourceVersion {
		return false, ErrIdentity
	}
	matched, err := v.Match(ctx, index, nonce, digest)
	if err != nil {
		return false, err
	}
	if err = v.Close(ctx); err != nil {
		return false, err
	}
	return matched, nil
}
