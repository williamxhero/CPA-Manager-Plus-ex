package officialbalance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// RuntimeKeyDigest keeps the encrypted query credential within its owning
// service. It signs a nonce-bound, domain-separated Core challenge, never a
// stable key fingerprint or a caller-supplied HTTP request.
func (s *Service) RuntimeKeyDigest(ctx context.Context, b Binding, version int64, nonce, sourceVersion, authIndex string) (string, error) {
	if !s.verify(ctx, b) || len(b.Targets) != 1 || version <= 0 {
		return "", ErrIdentity
	}
	r, err := s.Store.OfficialBalance(ctx, key(b))
	var config Config
	if err != nil || r.Version != version || !r.Configured || json.Unmarshal([]byte(r.Config), &config) != nil ||
		!sameAuthority(config.Binding, b) || config.Adapter != "deepseek" || config.Endpoint != "https://api.deepseek.com/user/balance" {
		return "", errors.New("official runtime key unavailable")
	}
	secret, err := s.Store.ResolveOfficialBalanceSecret(ctx, r.ID, version, r.SecretRef)
	if err != nil || secret == "" {
		return "", errors.New("official runtime key unavailable")
	}
	m := hmac.New(sha256.New, []byte(secret))
	_, _ = m.Write([]byte("cpa-provider-key-match-v1\n" + nonce + "\n" + sourceVersion + "\n" + authIndex))
	return hex.EncodeToString(m.Sum(nil)), nil
}
