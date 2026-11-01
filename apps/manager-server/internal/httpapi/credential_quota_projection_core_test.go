package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
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
	for _, kind := range []string{"source-boot", "binding-mismatch", "source-version", "protocol-A-first", "protocol-A-last", "honest-unsupported-protocol", "connection-CAS"} {
		t.Run(kind, func(t *testing.T) {
			f := newNativeAdmissionFixture(t)
			u, err := url.Parse(f.base)
			if err != nil {
				t.Fatal(err)
			}
			proxy := httputil.NewSingleHostReverseProxy(u)
			var sourceCalls atomic.Int64
			var exactSourceSelected atomic.Bool
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
					Version int         `json:"version"`
					Sources []cq.Source `json:"sources"`
				}
				if err := json.Unmarshal(data, &envelope); err != nil {
					return err
				}
				at := -1
				for i, candidate := range envelope.Sources {
					if kind == "honest-unsupported-protocol" {
						if candidate.State == "unsupported" {
							at = i
							break
						}
					} else if candidate.AuthIndex == f.sources["A"].AuthIndex && candidate.SourceVersion == f.sources["A"].SourceVersion && reflect.DeepEqual(candidate.Binding, f.sources["A"].Binding) && candidate.State == "ready" {
						at = i
						break
					}
				}
				if at < 0 {
					return errors.New("required actual published source not found")
				}
				exactSourceSelected.Store(true)
				source := &envelope.Sources[at]
				switch kind {
				case "source-boot":
					source.Scope.BootEpoch = "00000000-0000-4000-8000-000000000000"
				case "binding-mismatch":
					foundB := false
					for i, other := range envelope.Sources {
						if other.AuthIndex == f.sources["B"].AuthIndex && reflect.DeepEqual(other.Binding, f.sources["B"].Binding) {
							source.AuthIndex, envelope.Sources[i].AuthIndex = other.AuthIndex, source.AuthIndex
							foundB = true
							break
						}
					}
					if !foundB {
						return errors.New("actual published B binding missing")
					}
				case "source-version":
					source.SourceVersion = ""
				case "protocol-A-first", "protocol-A-last", "honest-unsupported-protocol":
					source.Protocol = "unsupported-future-protocol"
					// Deliberately move the SAME issued target, not an assumed first
					// descriptor. Other honest unsupported rows remain unchanged.
					chosen := *source
					rest := append([]cq.Source(nil), envelope.Sources[:at]...)
					rest = append(rest, envelope.Sources[at+1:]...)
					if kind == "protocol-A-last" {
						envelope.Sources = append(rest, chosen)
					} else {
						envelope.Sources = append([]cq.Source{chosen}, rest...)
					}
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
			if kind != "connection-CAS" && !exactSourceSelected.Load() {
				t.Fatal("negative did not exercise the required actual source")
			}
			if kind == "honest-unsupported-protocol" {
				var observed struct {
					Sources []cq.Source `json:"sources"`
				}
				if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &observed) != nil {
					t.Fatalf("honest unsupported projection rejected: HTTP%d %s", rr.Code, rr.Body.String())
				}
				preserved := false
				for _, source := range observed.Sources {
					if source.Protocol == "unsupported-future-protocol" && source.State == "unsupported" {
						preserved = true
					}
				}
				if !preserved {
					t.Fatal("unsupported descriptor was promoted or dropped")
				}
			} else {
				var rejected struct {
					Error string `json:"error"`
				}
				if rr.Code != http.StatusConflict || json.Unmarshal(rr.Body.Bytes(), &rejected) != nil || rejected.Error != "identity_changed" {
					t.Fatalf("changed authority returned HTTP%d: %s", rr.Code, rr.Body.String())
				}
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
