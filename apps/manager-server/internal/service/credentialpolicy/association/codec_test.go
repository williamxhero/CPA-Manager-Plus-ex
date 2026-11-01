package association_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	a "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy/association"
)

// Shared synthetic-only golden bytes independently checked by parent. Encoding
// altered semantic fields is NOT acceptance of those fields as an authority.
func TestFrozenAssociationGoldenVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/frozen-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	if digest := sha256.Sum256(raw); hex.EncodeToString(digest[:]) != "d8a9a872e317580e3b61d2c80733fa32c54bfba9aab145d016258259c8f3cc16" {
		t.Fatal("not the identical frozen LF canonical fixture")
	}
	type expected struct {
		EncodedBytes  int    `json:"encodedBytes"`
		ChallengeHash string `json:"challengeHash"`
		ContractHash  string `json:"contractHash"`
		CandidateMAC  string `json:"candidateMac"`
		Hex           string `json:"encodedEnvelopeHex"`
	}
	var vectors struct {
		Key     string          `json:"syntheticKeyUTF8"`
		Request json.RawMessage `json:"baseRequest"`
		Base    expected        `json:"base"`
		Vectors []struct {
			Name    string `json:"name"`
			Replace []struct {
				Path  []any `json:"path"`
				Value any   `json:"value"`
			} `json:"replace"`
			Remove [][]any `json:"remove"`
			expected
		}
	}
	if json.Unmarshal(raw, &vectors) != nil {
		t.Fatal("bad golden fixture")
	}
	check := func(t *testing.T, raw []byte, w expected) {
		var r a.Request
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatal(err)
		}
		got, err := a.Encode(r, []byte(vectors.Key))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Envelope) != w.EncodedBytes || got.ChallengeHash != w.ChallengeHash || got.ContractHash != w.ContractHash || got.CandidateMAC != w.CandidateMAC {
			t.Fatal("frozen codec mismatch")
		}
		if w.Hex != "" && hex.EncodeToString(got.Envelope) != w.Hex {
			t.Fatal("full envelope mismatch")
		}
	}
	t.Run("base", func(t *testing.T) { check(t, vectors.Request, vectors.Base) })
	for _, v := range vectors.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			var r any
			_ = json.Unmarshal(vectors.Request, &r)
			locate := func(path []any) (any, any) {
				node := r
				for _, key := range path[:len(path)-1] {
					switch x := node.(type) {
					case map[string]any:
						node = x[key.(string)]
					case []any:
						node = x[int(key.(float64))]
					}
				}
				return node, path[len(path)-1]
			}
			for _, change := range v.Replace {
				node, key := locate(change.Path)
				switch x := node.(type) {
				case map[string]any:
					x[key.(string)] = change.Value
				case []any:
					x[int(key.(float64))] = change.Value
				}
			}
			for _, path := range v.Remove {
				node, key := locate(path)
				delete(node.(map[string]any), key.(string))
			}
			raw, _ := json.Marshal(r)
			check(t, raw, v.expected)
		})
	}
}
