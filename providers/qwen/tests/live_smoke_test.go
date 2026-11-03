//go:build live

package tests_test

// Controlled smoke protocol mirrors the canonical OpenCode gate fixture.
import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

// TestLiveControlledInferenceGate never defaults to a user runtime or provider.
// Its parent supplies an isolated Core + actual DLL + two synthetic accounts.
func TestLiveControlledInferenceGate(t *testing.T) {
	base := os.Getenv("CPA_GATE_CONTROLLED_BASE")
	if base == "" {
		t.Skip("explicit isolated Core/plugin gate fixture required")
	}
	countsURL := os.Getenv("CPA_GATE_CONTROLLED_COUNTS")
	for _, raw := range []string{base, countsURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "http" || u.User != nil || !net.ParseIP(u.Hostname()).IsLoopback() {
			t.Fatal("gate fixture must use literal loopback")
		}
	}
	key, index, model, op := os.Getenv("CPA_GATE_CONTROLLED_KEY"), os.Getenv("CPA_GATE_CONTROLLED_INDEX"), os.Getenv("CPA_GATE_CONTROLLED_MODEL"), os.Getenv("CPA_GATE_CONTROLLED_OP")
	if key == "" || index == "" || model == "" || op == "" {
		t.Fatal("explicit fixture authority required")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	request := func(method, path string, body any) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, base+path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		r, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		out, _ := io.ReadAll(r.Body)
		if r.StatusCode != 200 {
			t.Fatalf("controlled gate status=%d", r.StatusCode)
		}
		return out
	}
	var ledger struct {
		Ready bool `json:"ready"`
	}
	json.Unmarshal(request("GET", "/v8/management/inference-gates", nil), &ledger)
	if !ledger.Ready {
		t.Fatal("ledger not restored/published")
	}
	var receipt struct {
		Applied, Durable bool
		Status           string
	}
	json.Unmarshal(request("GET", "/v8/management/inference-gates/operations/"+op, nil), &receipt)
	if !receipt.Applied || !receipt.Durable || receipt.Status != "applied" {
		t.Fatal("no durable applied operation receipt")
	}
	var view struct {
		Sources []map[string]any `json:"sources"`
	}
	json.Unmarshal(request("GET", "/v8/management/credential-quota/sources", nil), &view)
	var source map[string]any
	for _, s := range view.Sources {
		if s["auth_index"] == index {
			source = s
		}
	}
	if source == nil {
		t.Fatal("selected source missing")
	}
	q := map[string]any{"version": 1, "scope": source["scope"], "targets": source["targets"], "auth_index": index, "sourceVersion": source["sourceVersion"], "route": "all-current-source"}
	var eligibility struct{ Ready, Supported bool }
	json.Unmarshal(request("POST", "/v8/management/inference-gates/eligibility", q), &eligibility)
	if !eligibility.Ready || !eligibility.Supported {
		t.Fatal("selected route eligibility unavailable")
	}
	counts := func() struct{ A, B int } {
		t.Helper()
		r, err := client.Get(countsURL)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var c struct{ A, B int }
		if json.NewDecoder(r.Body).Decode(&c) != nil {
			t.Fatal("bad controlled send counts")
		}
		return c
	}
	before := counts()
	for _, stream := range []bool{false, true} {
		body := request("POST", "/v1/chat/completions", map[string]any{"model": model, "messages": []any{map[string]any{"role": "user", "content": "synthetic"}}, "stream": stream})
		if !bytes.Contains(body, []byte("ok")) {
			t.Fatal("healthy B did not complete")
		}
	}
	after := counts()
	if before.A != after.A || after.B != before.B+2 {
		t.Fatal("new paused-A sends or healthy-B failure after ACK")
	}
}
