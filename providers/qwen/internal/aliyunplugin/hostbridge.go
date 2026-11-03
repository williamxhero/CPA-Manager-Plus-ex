package aliyunplugin

import (
	"encoding/json"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
)

type RawCaller func(method string, payload []byte) ([]byte, error)

type HostBridge struct{ call RawCaller }

func NewHostBridge(call RawCaller) *HostBridge { return &HostBridge{call: call} }

// Log is synchronous: no callback goroutine can outlive plugin shutdown.
// Callers supply only secret-free diagnostic text, never signed URLs.
func (b *HostBridge) Log(level, message string) {
	if b == nil || b.call == nil {
		return
	}
	payload, err := json.Marshal(struct {
		Level   string `json:"level"`
		Message string `json:"message"`
	}{level, message})
	if err == nil {
		_, _ = b.call(pluginabi.MethodHostLog, payload)
	}
}
