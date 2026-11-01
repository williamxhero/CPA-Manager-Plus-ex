package association_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	a "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy/association"
)

func goldenRequest(t *testing.T) a.Request {
	t.Helper()
	raw, err := os.ReadFile("testdata/frozen-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Request a.Request `json:"baseRequest"`
	}
	if json.Unmarshal(raw, &v) != nil {
		t.Fatal("fixture")
	}
	return v.Request
}
func TestAssociationPrepareAndStrictFrozenRequest(t *testing.T) {
	original := goldenRequest(t)
	r, _, err := a.Prepare(original, []byte("SYNTHETIC-ASSOCIATION-VECTOR-KEY"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r)
	if _, err := a.DecodeRequest(raw); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"duplicate":         bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1),
		"escaped_duplicate": bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"\u0076ersion":1`), 1),
		"case_shadow":       bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"Version":1`), 1),
		"unicode_shadow":    bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"verſion":1`), 1),
		"exponent":          bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1e0`), 1),
		"negative_zero":     bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":-0`), 1),
		"surrogate":         bytes.Replace(raw, []byte(`synthetic-selected-index`), []byte(`\ud800`), 1),
		"nested_duplicate":  bytes.Replace(raw, []byte(`"credential_version":2`), []byte(`"credential_version":2,"credential_version":2`), 1),
		"trailing":          append(append([]byte(nil), raw...), []byte(`{}`)...),
		"too_big":           bytes.Repeat([]byte(" "), 32769),
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := a.DecodeRequest(b); err == nil {
				t.Fatal("malformed wire accepted")
			}
		})
	}
	for _, name := range []string{"provider_credential_zero", "counter_overflow", "config_max", "auth", "path", "null_newapi"} {
		t.Run(name, func(t *testing.T) {
			r := goldenRequest(t)
			switch name {
			case "provider_credential_zero":
				r.Official.Targets[0].CredentialVersion = 0
			case "counter_overflow":
				r.Selected.Scope.Revision = 9007199254740992
			case "config_max":
				r.Official.Version = 9007199254740991
			case "auth":
				r.Official.NewAPI.Auth = "user-pat-v1"
			case "path":
				r.Official.Endpoint = "https://synthetic.invalid/tenant/B/api/usage/token/"
			case "null_newapi":
				r.Official.NewAPI = nil
			}
			if _, _, err := a.Prepare(r, []byte("synthetic")); err == nil {
				t.Fatal("invalid association contract accepted")
			}
		})
	}
}
