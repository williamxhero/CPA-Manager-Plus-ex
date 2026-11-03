package providergate

import (
	"context"
	"reflect"

	"github.com/google/uuid"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
)

// RuntimeKeyChallenge is request-local: neither it nor its response may be used
// as a durable gate permit. The Core establishes key equality, not money scope.
type RuntimeKeyChallenge struct {
	Target        rc.Target
	SourceVersion string
	AuthIndex     string
	Nonce         string
}

type keyMatchRequest struct {
	groupBinding
	Version       int    `json:"version"`
	SourceVersion string `json:"sourceVersion"`
	AuthIndex     string `json:"auth_index"`
	Adapter       string `json:"adapter"`
	Nonce         string `json:"nonce"`
	Digest        string `json:"digest"`
}

type keyMatchResponse struct {
	groupBinding
	Version       int    `json:"version"`
	SourceVersion string `json:"sourceVersion"`
	AuthIndex     string `json:"auth_index"`
	Adapter       string `json:"adapter"`
	Nonce         string `json:"nonce"`
	Authority     string `json:"authority"`
	Matched       bool   `json:"matched"`
}

// VerifyRuntimeKeys validates complete current route coverage before and after
// independently challenging every member. It never calls the gate write API.
func (s *Service) VerifyRuntimeKeys(ctx context.Context, b Binding, sign func(RuntimeKeyChallenge) (string, error)) (string, error) {
	opening, err := s.prepare(ctx, b)
	if err != nil {
		return "", err
	}
	for _, member := range opening.proof.Members {
		if err := ctx.Err(); err != nil {
			return "", ErrUnavailable
		}
		challenge := RuntimeKeyChallenge{member.Targets[0], opening.proof.SourceVersion, member.AuthIndex, uuid.NewString()}
		digest, err := sign(challenge)
		if err != nil || !validDigest(digest) {
			return "", ErrUnavailable
		}
		q := keyMatchRequest{coreGroup(opening.status.Binding), 1, challenge.SourceVersion, challenge.AuthIndex, "deepseek", challenge.Nonce, digest}
		var response keyMatchResponse
		if err := opening.core.request(ctx, "POST", "providers/key-match", q, &response); err != nil {
			return "", err
		}
		if response.Version != 1 || response.Authority != "runtime_key_match_only" || !response.Matched ||
			!reflect.DeepEqual(response.groupBinding, q.groupBinding) || response.SourceVersion != q.SourceVersion ||
			response.AuthIndex != q.AuthIndex || response.Nonce != q.Nonce || response.Adapter != q.Adapter {
			return "", ErrUnavailable
		}
	}
	closing, err := s.prepare(ctx, b)
	if err != nil || !reflect.DeepEqual(opening.proof, closing.proof) || !opening.core.current(ctx) || ctx.Err() != nil {
		return "", ErrIdentity
	}
	return opening.proof.SourceVersion, nil
}
