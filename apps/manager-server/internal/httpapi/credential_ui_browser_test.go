package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Opt-in browser bridge: actual candidate API/SQLite/Core and controlled TLS.
// Core's original 120s process deadline is unchanged; this bridge expires sooner.
func TestCredentialUI93RenderedBrowserBridge(t *testing.T) {
	dir := os.Getenv("UI93_BROWSER_DIR")
	if dir == "" {
		t.Skip("explicit isolated rendered-browser session only")
	}
	panel := os.Getenv("UI93_PANEL")
	if _, err := os.Stat(panel); err != nil {
		t.Fatal(err)
	}
	f := newNativeAdmissionFixture(t)
	stop := make(chan struct{})
	var once sync.Once
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/management.html" || r.URL.Path == "/" {
			http.ServeFile(w, r, panel)
			return
		}
		if r.URL.Path == "/__ui93/stop" && r.Method == "POST" {
			once.Do(func() { close(stop) })
			w.WriteHeader(204)
			return
		}
		f.manager.Handler().ServeHTTP(w, r)
	})
	server := httptest.NewServer(h)
	defer server.Close()
	raw, _ := json.Marshal(map[string]any{"url": server.URL + "/management.html#/accounts", "base": server.URL, "core": f.base, "sources": f.sources, "deadlineSeconds": 90, "realAccounts": false, "realProviderTraffic": false})
	if err := os.WriteFile(filepath.Join(dir, "browser-bridge.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("browser bridge ready", server.URL)
	select {
	case <-stop:
	case <-time.After(90 * time.Second):
		t.Error("rendered bridge expired before explicit stop")
	}
	states := map[string]any{"A": f.status("A"), "B": f.status("B"), "quotaCallsA": f.quotaCalls[0].Load(), "quotaCallsB": f.quotaCalls[1].Load(), "businessCallsA": f.sends[0].Load(), "businessCallsB": f.sends[1].Load()}
	raw, _ = json.MarshalIndent(states, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "browser-api-final.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
