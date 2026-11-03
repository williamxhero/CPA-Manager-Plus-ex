package plugin

import (
	"encoding/json"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"testing"
)

func TestExecutorInvocationPreservesHostScope(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "http", true: "sse"}[stream], func(t *testing.T) {
			seen := false
			m, _ := newExecutorManager(t, func(method string, payload []byte) ([]byte, error) {
				if method == pluginabi.MethodHostHTTPDo || method == pluginabi.MethodHostHTTPDoStream {
					var req map[string]any
					json.Unmarshal(payload, &req)
					if req["method"] == "POST" {
						seen = true
						if req["host_callback_id"] != "issued-host-scope" {
							t.Error("inference callback lost selected host operation scope")
						}
					}
				}
				if method == pluginabi.MethodHostHTTPDoStream {
					return hostOK(hostStreamStartResp{StatusCode: 200}), nil
				}
				return hostOK(pluginapi.HTTPResponse{StatusCode: 200, Body: []byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)}), nil
			})
			var wire map[string]any
			json.Unmarshal(executionBody("openai", `{"model":"qwen3.8-max","messages":[{"role":"user","content":"synthetic"}]}`, stream), &wire)
			wire["host_callback_id"] = "issued-host-scope"
			raw, _ := json.Marshal(wire)
			method := pluginabi.MethodExecutorExecute
			if stream {
				method = pluginabi.MethodExecutorExecuteStream
			}
			m.HandleCall(method, raw)
			if !seen {
				t.Fatal("actual execution did not call HTTP bridge")
			}
		})
	}
}
