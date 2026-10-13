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
}
