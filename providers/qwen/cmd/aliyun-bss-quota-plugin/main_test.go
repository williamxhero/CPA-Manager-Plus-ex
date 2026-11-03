package main

import (
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
)

func TestNativeMissingMethodReturnsFailureWithoutResponseBuffer(t *testing.T) {
	if rc := cliproxyPluginCall(nil, nil, 0, nil); rc != 1 {
		t.Fatalf("missing method returned %d", rc)
	}
	cliproxyPluginFree(nil, 0)
	writeResponse(nil, []byte(`{"ok":true}`))
}

func TestNativeShutdownDelegatesToQuotaOnlyManager(t *testing.T) {
	raw, err := dispatcher.HandleCall(pluginabi.MethodPluginRegister, nil)
	var env pluginabi.Envelope
	if err != nil || json.Unmarshal(raw, &env) != nil || !env.OK {
		t.Fatal("registration failed")
	}
	cliproxyPluginShutdown()
	raw, err = dispatcher.HandleCall(pluginabi.MethodQuotaFetch, []byte(`{}`))
	if err != nil || json.Unmarshal(raw, &env) != nil || env.OK || env.Error.Code != "quota_error" {
		t.Fatal("native shutdown did not stop quota provider")
	}
	cliproxyPluginShutdown()
}
