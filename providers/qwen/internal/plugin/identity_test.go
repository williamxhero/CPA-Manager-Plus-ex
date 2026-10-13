package plugin

import (
	"context"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"qwen-cliproxyapi/internal/config"
)

func TestQuotaRejectsLegacyOrReplacedCredentialLocator(t *testing.T) {
	m := commandManager(t, "success")
	key := "dummy-selected-key"
	seed, err := credentialIndexSeed(key, "", m.cfg)
	if err != nil {
		t.Fatal(err)
	}
	index := credentialAuthIndex(seed)
	for _, tc := range []struct{ name, key, baseURL, seed, index string }{
		{"legacy path index", key, "", seed, "legacy-path-index"},
		{"missing seed", key, "", "", index},
		{"key replaced", "dummy-replacement-key", "", seed, index},
		{"URL replaced", key, "https://example.test/v2", seed, index},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.FetchQuota(context.Background(), pluginapi.QuotaFetchRequest{Provider: ProviderID, AuthIndex: tc.index, Attributes: map[string]string{"api_key": tc.key, "base_url": tc.baseURL, "auth_index_seed": tc.seed}})
			if err == nil || !strings.Contains(err.Error(), "credential identity") {
				t.Fatalf("stale quota identity accepted: %v", err)
			}
		})
	}
	result, err := m.FetchQuota(context.Background(), pluginapi.QuotaFetchRequest{Provider: ProviderID, AuthIndex: index, Attributes: map[string]string{"api_key": key, "auth_index_seed": seed}})
	if err != nil || len(result.Groups) == 0 {
		t.Fatalf("matching identity rejected: %v", err)
	}
}

func TestQuotaStorageCredentialKeepsIdentityFence(t *testing.T) {
	m := commandManager(t, "success")
	oldSeed, err := credentialIndexSeed("synthetic-old-key", "https://old.test/v1", m.cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, key, base, locator string
		attrs                    map[string]string
		wantOK                   bool
	}{
		{"current identity without key attributes", "synthetic-current-key", "https://current.test/v1", "current", nil, true},
		{"current identity with stale attributes", "synthetic-current-key", "https://current.test/v1", "current", map[string]string{"api_key": "synthetic-old-key", "base_url": "https://old.test/v1"}, true},
		{"old locator after key edit", "synthetic-current-key", "https://old.test/v1", "old", map[string]string{"api_key": "synthetic-old-key", "base_url": "https://old.test/v1"}, false},
		{"old locator after URL edit", "synthetic-old-key", "https://current.test/v1", "old", map[string]string{"api_key": "synthetic-old-key", "base_url": "https://old.test/v1"}, false},
		{"normalized URL preserves identity", "synthetic-old-key", "https://OLD.test:443/v1/", "old", map[string]string{"api_key": "synthetic-old-key", "base_url": "https://old.test/v1"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seed, err := credentialIndexSeed(tc.key, tc.base, m.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if tc.locator == "old" {
				seed = oldSeed
			}
			attrs := map[string]string{"auth_index_seed": seed}
			for k, v := range tc.attrs {
				attrs[k] = v
			}
			req := pluginapi.QuotaFetchRequest{Provider: ProviderID, AuthIndex: credentialAuthIndex(seed), Attributes: attrs, StorageJSON: mustJSON(map[string]string{"api_key": tc.key, "base_url": tc.base})}
			result, err := m.FetchQuota(context.Background(), req)
			if tc.wantOK {
				if err != nil || len(result.Groups) == 0 {
					t.Fatalf("current storage identity rejected: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "credential identity") {
				t.Fatalf("old locator accepted after storage changed: %v", err)
			}
		})
	}
}

func TestQwenCredentialIdentityBindsKeyAndURLAcrossParseAndRefresh(t *testing.T) {
	p := authProvider{cfg: config.Config{BaseURL: "https://example.test/v1"}}
	parse := func(key, baseURL, label string, disabled bool) pluginapi.AuthData {
		t.Helper()
		raw := mustJSON(map[string]any{"type": "qwen", "api_key": key, "base_url": baseURL, "label": label, "disabled": disabled})
		resp, err := p.ParseAuth(context.Background(), pluginapi.AuthParseRequest{Provider: ProviderID, FileName: "same.json", RawJSON: raw})
		if err != nil || !resp.Handled {
			t.Fatalf("parse: %v", err)
		}
		return resp.Auth
	}
	original := parse("synthetic-key-a", "", "First", false)
	seed := original.Attributes["auth_index_seed"]
	if seed == "" || strings.Contains(seed, "synthetic-key") || strings.Contains(seed, "example.test") {
		t.Fatalf("missing/nonopaque credential identity: %q", seed)
	}
	for _, same := range []pluginapi.AuthData{
		parse("synthetic-key-a", "https://EXAMPLE.test:443/v1/", "Renamed", true),
		parse("synthetic-key-a", "https://example.test/v1", "Other", false),
	} {
		if same.Attributes["auth_index_seed"] != seed {
			t.Fatal("presentation/disabled/canonical URL changed identity")
		}
	}
	for _, replaced := range []pluginapi.AuthData{
		parse("synthetic-key-b", "", "First", false),
		parse("synthetic-key-a", "https://example.test/v2", "First", false),
	} {
		if replaced.Attributes["auth_index_seed"] == seed {
			t.Fatal("key/URL replacement retained credential identity")
		}
	}
	refreshed, err := p.RefreshAuth(context.Background(), pluginapi.AuthRefreshRequest{AuthProvider: ProviderID, AuthID: original.ID, StorageJSON: original.StorageJSON, Attributes: original.Attributes})
	if err != nil || refreshed.Auth.Attributes["auth_index_seed"] != seed {
		t.Fatalf("refresh identity drift: %v", err)
	}
	attrs := map[string]string{"api_key": "synthetic-key-b", "auth_index_seed": seed}
	replaced, err := p.RefreshAuth(context.Background(), pluginapi.AuthRefreshRequest{AuthProvider: ProviderID, AuthID: original.ID, Attributes: attrs})
	if err != nil || replaced.Auth.Attributes["auth_index_seed"] == seed {
		t.Fatalf("refresh trusted stale identity: %v", err)
	}
	if attrs["auth_index_seed"] != seed {
		t.Fatal("refresh mutated caller attributes")
	}
	current := parse("synthetic-key-b", "https://example.test/v2", "Edited", false)
	refreshed, err = p.RefreshAuth(context.Background(), pluginapi.AuthRefreshRequest{AuthProvider: ProviderID, AuthID: original.ID, StorageJSON: current.StorageJSON, Attributes: original.Attributes})
	if err != nil || refreshed.Auth.Attributes["auth_index_seed"] != current.Attributes["auth_index_seed"] || refreshed.Auth.Attributes["api_key"] != "synthetic-key-b" || refreshed.Auth.Attributes["base_url"] != "https://example.test/v2" {
		t.Fatalf("refresh retained stale routing attributes or identity: %v", err)
	}
	if original.Attributes["auth_index_seed"] != seed || original.Attributes["api_key"] != "synthetic-key-a" {
		t.Fatal("storage-aware refresh mutated caller attributes")
	}
	m := commandManager(t, "success")
	_, err = m.FetchQuota(context.Background(), pluginapi.QuotaFetchRequest{Provider: ProviderID, AuthIndex: credentialAuthIndex(seed), StorageJSON: refreshed.Auth.StorageJSON, Attributes: refreshed.Auth.Attributes})
	if err == nil || !strings.Contains(err.Error(), "credential identity") {
		t.Fatalf("refresh allowed quota to release the original locator: %v", err)
	}
}
