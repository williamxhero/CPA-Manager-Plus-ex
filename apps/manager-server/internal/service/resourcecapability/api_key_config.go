package resourcecapability

import (
	"context"
	"errors"
	"strings"
)

var ErrAPIKeyConfig = errors.New("api key config unavailable for binding")

// ReadBoundAPIKeyConfig reads the same v8 API-key group view that Manager
// proxies for admins. The join is by Core-issued resource ID, never display
// name, array position, hostname or runtime auth index. Only the selected
// credential leaves this function; nothing is cached or logged.
func (s *Service) ReadBoundAPIKeyConfig(ctx context.Context, before *Negotiated, rows []Row) (baseURL, credential string, err error) {
	if before == nil || before.connection == nil || before.connection.owner != s || len(rows) == 0 {
		return "", "", ErrAPIKeyConfig
	}
	family := rows[0].Family
	// Only config API-key families, not file/OAuth/plugin credentials.
	if !oneOf(family, "gemini", "interactions", "vertex", "codex", "claude", "xai", "meta", "openai-compatibility") {
		return "", "", ErrAPIKeyConfig
	}
	ids := make(map[string]bool, len(rows))
	for _, row := range rows {
		if row.Kind != "credential" || row.Family != family {
			return "", "", ErrAPIKeyConfig
		}
		ids[row.ResourceID] = true
	}
	var groups []struct {
		BaseURL string `json:"base-url"`
		Keys    []struct {
			ResourceID string `json:"resource-id"`
			Key        string `json:"api-key"`
		} `json:"keys"`
	}
	conn := before.connection.config
	if readCore(ctx, conn.CPABaseURL, conn.ManagementKey, "config/api-keys/"+family, &groups) != "" {
		return "", "", ErrAPIKeyConfig
	}
	seen := make(map[string]bool, len(ids))
	selectedGroup := -1
	ambiguousKey := false
	for i, group := range groups {
		// Core's provider group owns base-url. Do not accept an arbitrary
		// per-key extension as authority for where to send the credential.
		base := strings.TrimSpace(group.BaseURL)
		for _, key := range group.Keys {
			if !ids[key.ResourceID] {
				continue
			}
			secret := strings.TrimSpace(key.Key)
			if seen[key.ResourceID] || base == "" || selectedGroup >= 0 && i != selectedGroup {
				return "", "", ErrAPIKeyConfig
			}
			if selectedGroup < 0 {
				selectedGroup, baseURL, credential = i, base, secret
			} else if credential != secret {
				ambiguousKey = true
			}
			seen[key.ResourceID] = true
		}
	}
	if len(seen) != len(ids) {
		return "", "", ErrAPIKeyConfig
	}
	// A binding covering distinct keys does not select a billing account.
	// Hostname evidence is still useful, but do not probe or register a key.
	if ambiguousKey || len(credential) > 8192 || strings.ContainsAny(credential, "\r\n") || strings.HasPrefix(credential, "dca:") {
		credential = ""
	}
	// Never expose a misconfigured URL with a key embedded in it, even
	// when the binding contained several keys and none was selected.
	foldedBase := strings.ToLower(baseURL)
	embedded := func(key string) bool {
		return key != "" && strings.Contains(foldedBase, strings.ToLower(key))
	}
	if embedded(conn.ManagementKey) {
		return "", "", ErrAPIKeyConfig
	}
	for _, group := range groups {
		for _, key := range group.Keys {
			if embedded(strings.TrimSpace(key.Key)) {
				return "", "", ErrAPIKeyConfig
			}
		}
	}
	return baseURL, credential, nil
}
