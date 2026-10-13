package plugin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestExecutorNormalQuotaErrorPreservesSafeABIMessage(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonstream", true: "stream"}[stream], func(t *testing.T) {
			body := []byte(`{"error":{"code":"insufficient_quota","type":"insufficient_quota","message":"Quota exhausted dummy-selected-key dummy-config-key token-secret cookie-secret","token":"token-secret"},"reset_at":"2030-01-02T03:04:05Z","cookie":"cookie-secret"}`)
			m, _ := newExecutorManager(t, func(method string, _ []byte) ([]byte, error) {
				switch method {
				case pluginabi.MethodHostHTTPDo:
					return hostOK(pluginapi.HTTPResponse{StatusCode: 429, Body: body}), nil
				case pluginabi.MethodHostHTTPDoStream:
					return hostOK(hostStreamStartResp{StatusCode: 429, StreamID: "upstream"}), nil
				case pluginabi.MethodHostHTTPStreamRead:
					return hostOK(hostStreamReadResp{Payload: body, Done: true}), nil
				default:
					return hostOK(map[string]any{}), nil
				}
			})
			method := pluginabi.MethodExecutorExecute
			if stream {
				method = pluginabi.MethodExecutorExecuteStream
			}
			raw, err := m.HandleCall(method, executionBody("openai", `{"messages":[]}`, stream))
			if err != nil {
				t.Fatal(err)
			}
			env := decodeEnv(t, raw)
			if env.OK || env.Error == nil || env.Error.HTTPStatus != 429 || env.Error.Code != "quota_exhaustion" {
				t.Fatalf("wrong ABI error: %s", raw)
			}
			var metadata struct {
				Error struct {
					Code string `json:"code"`
					Type string `json:"type"`
				} `json:"error"`
				ResetAt string `json:"reset_at"`
			}
			if err := json.Unmarshal([]byte(env.Error.Message), &metadata); err != nil || metadata.Error.Code != "insufficient_quota" || metadata.Error.Type != "insufficient_quota" || metadata.ResetAt != "2030-01-02T03:04:05Z" {
				t.Fatalf("normal quota metadata lost: %s (%v)", raw, err)
			}
			for _, secret := range []string{"dummy-selected-key", "dummy-config-key", "token-secret", "cookie-secret"} {
				if strings.Contains(string(raw), secret) {
					t.Fatalf("ABI exposed upstream secret %q", secret)
				}
			}
		})
	}
}
