package officialbalance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
)

// RuntimeKeyDigest stays inside trusted service wiring. It is never a public
// balance response, a persistent key fingerprint, or an account ownership claim.
// Configured group labels alone cannot authorize a provider-wide decision.
func (s *Service) RuntimeKeyDigest(ctx context.Context, q Query, nonce, sourceVersion, index string) (string, error) {
	if uuid.Validate(nonce) != nil || len(sourceVersion) != 64 || index == "" {
		return "", ErrInput
	}
	status, err := s.Status(ctx, q.Binding)
	if err != nil {
		return "", err
	}
	if status.Version != q.Version || !status.Configured || status.Adapter != "deepseek" || status.Endpoint != "https://api.deepseek.com/user/balance" {
		return "", ErrInput
	}
	digest, err := s.runtimeKeyDigest(ctx, q, nonce, sourceVersion, index)
	if err != nil {
		return "", err
	}
	// Revalidate both identity and complete secret/source authority after use.
	current, err := s.Status(ctx, q.Binding)
	if err != nil || current.Version != q.Version || !sameSourceAuthority(current.Config, status.Config) {
		return "", ErrIdentity
	}
	return digest, nil
}

// RuntimeKeyDigestForInventory is the batched read-only equivalent. The caller
// must close this authenticated inventory and re-read source versions before
// using any proof, exactly as for InventoryStatus. It cannot authorize a gate.
func (s *Service) RuntimeKeyDigestForInventory(ctx context.Context, q Query, n *rc.Negotiated, nonce, sourceVersion, index string) (string, error) {
	if uuid.Validate(nonce) != nil || len(sourceVersion) != 64 || index == "" {
		return "", ErrInput
	}
	status, err := s.InventoryStatus(ctx, q.Binding, n)
	if err != nil {
		return "", err
	}
	if status.Version != q.Version || !status.Configured || status.Adapter != "deepseek" || status.Endpoint != "https://api.deepseek.com/user/balance" {
		return "", ErrIdentity
	}
	return s.runtimeKeyDigest(ctx, q, nonce, sourceVersion, index)
}
func (s *Service) runtimeKeyDigest(ctx context.Context, q Query, nonce, sourceVersion, index string) (string, error) {
	id := key(q.Binding)
	record, err := s.Store.OfficialBalance(ctx, id)
	if err != nil || record.Version != q.Version {
		return "", ErrIdentity
	}
	secret, err := s.Store.ResolveOfficialBalanceSecret(ctx, id, q.Version, record.SecretRef)
	if err != nil || secret == "" {
		return "", ErrIdentity
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("cpa-provider-key-match-v1\n" + nonce + "\n" + sourceVersion + "\n" + index))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// WithFundingReading holds the current local query authority through the Core
// publication. A configuration or completion transition on this host cannot
// replace the authenticated reading between final validation and transmission.
func (s *Service) WithFundingReading(ctx context.Context, q Query, n *rc.Negotiated, attemptID string, send func(balance.Observation, func(nonce, sourceVersion, index string) (string, error)) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.InventoryStatus(ctx, q.Binding, n)
	if err != nil {
		return err
	}
	adapter := FundingAdapter(current)
	if !current.Configured || current.Version != q.Version || adapter == "" || current.Stale || current.LastSuccess == nil || current.Attempt == nil ||
		current.Attempt.QueryID != attemptID || current.LastSuccess.QueryID != attemptID || current.Attempt.QueryStatus != "success" || current.LastSuccess.QueryStatus != "success" || current.LastSuccess.CapabilityStatus != "supported" || (adapter == "deepseek" && current.LastSuccess.Semantic != "account_wallet") || (adapter == "deepinfra" && current.LastSuccess.DeepInfraFunding == nil) {
		return nil
	}
	id := key(q.Binding)
	record, err := s.Store.OfficialBalance(ctx, id)
	if err != nil || record.Version != q.Version {
		return ErrIdentity
	}
	secret, err := s.Store.ResolveOfficialBalanceSecret(ctx, id, q.Version, record.SecretRef)
	if err != nil || secret == "" {
		return ErrIdentity
	}
	reading := *current.LastSuccess
	raw, err := json.Marshal(balance.FundingWire(reading))
	if err != nil {
		return err
	}
	stamp := sha256.Sum256(raw)
	return send(reading, func(nonce, sourceVersion, index string) (string, error) {
		if uuid.Validate(nonce) != nil || len(sourceVersion) != 64 || index == "" {
			return "", ErrInput
		}
		mac := hmac.New(sha256.New, []byte(secret))
		domain := "cpa-provider-funding-v1\n"
		if adapter == "deepinfra" {
			domain = "cpa-provider-funding/deepinfra-v1\n"
		}
		_, _ = mac.Write([]byte(domain + nonce + "\n" + sourceVersion + "\n" + index + "\n" + hex.EncodeToString(stamp[:])))
		return hex.EncodeToString(mac.Sum(nil)), nil
	})
}
