package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// Counts the authenticated wire boundary, not an internal mock. Inventory and
// the returned source projection both come from the independently verified Core.
func TestCredentialQuotaActualSourcesSingleAuthenticatedProjection(t *testing.T) {
	f := newNativeAdmissionFixture(t)
	u, err := url.Parse(f.base)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	var mu sync.Mutex
	calls := map[string]int{}
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls[r.URL.Path]++
		mu.Unlock()
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(edge.Close)
	if err := f.db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: edge.URL, ManagementKey: "test-password"}}); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	f.api("GET", "/usage-service/credential-quota", nil, 200, nil)
	mu.Lock()
	defer mu.Unlock()
	t.Logf("one public source read elapsed=%s authenticated management calls=%v", time.Since(started), calls)
	for path, want := range map[string]int{
		"/v8/management/resource-capabilities":    2,
		"/v8/management/resources":                1,
		"/v8/management/resource-bindings":        1,
		"/v8/management/credential-quota/sources": 1,
	} {
		if calls[path] != want {
			t.Errorf("%s calls=%d want=%d", path, calls[path], want)
		}
	}
	if len(calls) != 4 || f.quotaCalls[0].Load() != 0 || f.quotaCalls[1].Load() != 0 {
		t.Fatal("source read performed unexpected management or upstream query I/O")
	}
}

func TestCredentialQuotaActualProjectionRejectsSourceAndConnectionChanges(t *testing.T) {
	for _, kind := range []string{"source-boot", "binding-mismatch", "source-version", "protocol", "connection-CAS"} {
		t.Run(kind, func(t *testing.T) {
			f := newNativeAdmissionFixture(t)
			u, err := url.Parse(f.base)
			if err != nil {
				t.Fatal(err)
			}
			proxy := httputil.NewSingleHostReverseProxy(u)
			var sourceCalls atomic.Int64
			proxy.ModifyResponse = func(response *http.Response) error {
				if response.Request.URL.Path != "/v8/management/credential-quota/sources" {
					return nil
				}
				sourceCalls.Add(1)
				if kind == "connection-CAS" {
					// Change the real saved connection after the authenticated reply,
					// before Inventory's deferred connection CAS. No new RPC.
					return f.db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: f.base, ManagementKey: "test-password"}})
				}
				data, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil {
					return err
				}
				var envelope struct {
					Version int              `json:"version"`
					Sources []map[string]any `json:"sources"`
				}
				if err := json.Unmarshal(data, &envelope); err != nil {
					return err
				}
				source := envelope.Sources[0]
				switch kind {
				case "source-boot":
					source["scope"].(map[string]any)["boot_epoch"] = "00000000-0000-4000-8000-000000000000"
				case "binding-mismatch":
					source["auth_index"], envelope.Sources[1]["auth_index"] = envelope.Sources[1]["auth_index"], source["auth_index"]
				case "source-version":
					delete(source, "sourceVersion")
				case "protocol":
					source["protocol"] = "unsupported-future-protocol"
				}
				data, err = json.Marshal(envelope)
				if err != nil {
					return err
				}
				response.Body = io.NopCloser(bytes.NewReader(data))
				response.ContentLength = int64(len(data))
				response.Header.Del("Content-Length")
				return nil
			}
			edge := httptest.NewServer(proxy)
			t.Cleanup(edge.Close)
			if err := f.db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: edge.URL, ManagementKey: "test-password"}}); err != nil {
				t.Fatal(err)
			}
			rr := testutil.Request(t, f.manager.Handler(), "GET", "/usage-service/credential-quota", "", testutil.AdminKey)
			var rejected struct {
				Error string `json:"error"`
			}
			if rr.Code != http.StatusConflict || json.Unmarshal(rr.Body.Bytes(), &rejected) != nil || rejected.Error != "identity_changed" {
				t.Fatalf("changed authority returned HTTP%d: %s", rr.Code, rr.Body.String())
			}
			if sourceCalls.Load() != 1 || f.quotaCalls[0].Load() != 0 || f.quotaCalls[1].Load() != 0 {
				t.Fatal("rejected projection retried/acquired quota")
			}
		})
	}
}

func TestCredentialQuotaActualProjectionIsPrivateCopyAndRequestLocal(t *testing.T) {
	f := newNativeAdmissionFixture(t)
	u, err := url.Parse(f.base)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	var sourceCalls atomic.Int64
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v8/management/credential-quota/sources" && sourceCalls.Add(1) > 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(edge.Close)
	if err := f.db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: edge.URL, ManagementKey: "test-password"}}); err != nil {
		t.Fatal(err)
	}
	identity := f.manager.AppContext().ResourceCapabilityService
	first := identity.Inventory(context.Background(), true)
	original := first.CredentialQuotaSourceProjection()
	if first.CredentialQuery.State != "available" || len(original) == 0 {
		t.Fatal("actual verified projection missing")
	}
	copy := first.CredentialQuotaSourceProjection()
	copy[0] = '!'
	if !bytes.Equal(original, first.CredentialQuotaSourceProjection()) {
		t.Fatal("caller mutated retained evidence")
	}
	serialized, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(serialized, []byte(`"sourceVersion"`)) {
		t.Fatal("private source projection leaked in JSON")
	}
	second := identity.Inventory(context.Background(), true)
	if second.CredentialQuery.State == "available" || len(second.CredentialQuotaSourceProjection()) != 0 || sourceCalls.Load() != 2 {
		t.Fatal("new inventory reused old authenticated source evidence")
	}
	if !bytes.Equal(original, first.CredentialQuotaSourceProjection()) {
		t.Fatal("later inventory mutated prior result")
	}
}
