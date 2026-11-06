package plugin

import (
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"cpa-credential-opencode/internal/config"
)

func TestQuotaInvocationCallbackContextIsForwarded(t *testing.T) {
	calls := 0
	bridge := NewHostBridge(func(method string, payload []byte) ([]byte, error) {
		calls++
		var request struct {
			HostCallbackID string `json:"host_callback_id"`
			URL            string `json:"url"`
		}
		if err := json.Unmarshal(payload, &request); err != nil {
			t.Fatal(err)
		}
		if method != pluginabi.MethodHostHTTPDo || request.HostCallbackID != "issued-quota-operation" || request.URL != "https://opencode.ai/inference/go/v1/usage" {
			t.Errorf("quota context/destination lost")
		}
		return hostOK(pluginapi.HTTPResponse{StatusCode: 200, Body: []byte(`{"usage":{"rolling":{"percent":42,"status":"rate-limited","resetsAt":"2027-01-01T00:00:00Z"}}}`)}), nil
	})
	m := NewManager(bridge)
	m.cfg = config.Config{BaseURL: "https://opencode.ai/inference/go/v1"}
	request := []byte(`{"provider":"opencode-go","attributes":{"api_key":"selected-synthetic"},"host_callback_id":"issued-quota-operation"}`)
	var response pluginapi.QuotaFetchResponse
	decodeResult(t, mustHandle(t, m, pluginabi.MethodQuotaFetch, request), &response)
	if calls != 1 {
		t.Fatal("quota invoked a fallback or duplicate request")
	}
	var description struct {
		ScopedHTTP          bool   `json:"scoped_http"`
		DefaultQuotaBaseURL string `json:"default_quota_base_url"`
	}
	decodeResult(t, mustHandle(t, m, pluginabi.MethodQuotaDescribe, []byte(`{}`)), &description)
	if !description.ScopedHTTP || description.DefaultQuotaBaseURL != config.DefaultBaseURL {
		t.Fatal("scoped quota capability not advertised")
	}
}
