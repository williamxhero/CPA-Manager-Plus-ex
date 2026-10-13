//go:build debug

package plugin

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestExecutorDoesNotLogCredentialsOnFailures(t *testing.T) {
	for _, mode := range []string{"nonstream-network", "stream-open-network", "stream-read-transport", "stream-read-reported"} {
		t.Run(mode, func(t *testing.T) {
			key := "synthetic-debug-secret-" + mode
			m, _ := newExecutorManager(t, func(method string, _ []byte) ([]byte, error) {
				switch method {
				case pluginabi.MethodHostHTTPDo:
					return nil, errors.New("network failed " + key)
				case pluginabi.MethodHostHTTPDoStream:
					if mode == "stream-open-network" {
						return nil, errors.New("network failed " + key)
					}
					return hostOK(hostStreamStartResp{StatusCode: 200, StreamID: "upstream-" + key}), nil
				case pluginabi.MethodHostHTTPStreamRead:
					if mode == "stream-read-transport" {
						return nil, errors.New("upstream failed " + key)
					}
					return hostOK(hostStreamReadResp{Error: "upstream failed " + key, Done: true}), nil
				}
				return hostOK(map[string]any{}), nil
			})
			var req executorRequest
			_ = json.Unmarshal(executionBody("openai", `{"messages":[]}`, mode != "nonstream-network"), &req)
			req.StorageJSON = mustJSON(map[string]string{"api_key": key})
			raw, err := m.HandleCall(pluginabi.MethodExecutorExecute, mustJSON(req))
			if err != nil || decodeEnv(t, raw).OK != strings.HasPrefix(mode, "stream-read-") {
				t.Fatalf("failure did not reach the public execution seam: %s %v", raw, err)
			}
			if !m.bridge.WaitForInFlight(time.Second) {
				t.Fatal("stream failure did not finish")
			}
			log, err := os.ReadFile(filepath.Join(filepath.Dir(os.Args[0]), "qwen-debug.log"))
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{key, "dummy-selected-key", "dummy-config-key"} {
				if strings.Contains(string(log), secret) {
					t.Fatal("whole debug log leaked a credential during execution failure")
				}
			}
		})
	}
}

func TestExecutorRecordsCredentialConflictWithoutValues(t *testing.T) {
	m, _ := newExecutorManager(t, func(string, []byte) ([]byte, error) {
		return hostOK(pluginapi.HTTPResponse{StatusCode: 200, Body: []byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)}), nil
	})
	var req executorRequest
	_ = json.Unmarshal(executionBody("openai", `{"messages":[]}`, false), &req)
	req.AuthAttributes = map[string]string{"api_key": "synthetic-old-key", "base_url": "https://old.test/v1"}
	req.StorageJSON = []byte(`{"api_key":"synthetic-current-key","base_url":"https://current.test/v1"}`)
	raw, err := m.HandleCall(pluginabi.MethodExecutorExecute, mustJSON(req))
	if err != nil || !decodeEnv(t, raw).OK {
		t.Fatalf("execution failed: %s %v", raw, err)
	}
	log, err := os.ReadFile(filepath.Join(filepath.Dir(os.Args[0]), "qwen-debug.log"))
	if err != nil {
		t.Fatalf("debug conflict was not recorded: %v", err)
	}
	found := false
	for _, line := range strings.Split(string(log), "\n") {
		if !strings.Contains(line, "credential storage_attributes_conflict") {
			continue
		}
		if strings.Contains(line, "api_key=true base_url=true") {
			found = true
		}
		for _, value := range []string{"synthetic-old-key", "synthetic-current-key", "https://old.test/v1", "https://current.test/v1"} {
			if strings.Contains(line, value) {
				t.Fatal("conflict trace leaked a credential value")
			}
		}
	}
	if !found {
		t.Fatal("debug log did not record key and URL conflict flags")
	}
}
