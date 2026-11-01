package fullrefresh_test

import (
	"encoding/json"
	"strings"
	"testing"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
)

func TestSourceScheduleKeyNativeAttemptFieldsDoNotChangeSource(t *testing.T) {
	q := cq.Query{SourceVersion: "source-v1", AuthIndex: "issued-auth-reference", Version: 7, TimeoutMS: 15000}
	request := fr.SourceRequest{Kind: fr.Credential, Credential: &q}
	before, _ := json.Marshal(request)
	key := fr.SourceScheduleKey(request)
	other := q
	other.Version = 8
	other.TimeoutMS = 60000
	if got := fr.SourceScheduleKey(fr.SourceRequest{Kind: fr.Credential, Credential: &other}); got != key {
		t.Fatal("attempt version/timeout changed exact-source schedule identity")
	}
	other.SourceVersion = "source-v2"
	if fr.SourceScheduleKey(fr.SourceRequest{Kind: fr.Credential, Credential: &other}) == key {
		t.Fatal("different source version shared schedule identity")
	}
	after, _ := json.Marshal(request)
	if string(after) != string(before) {
		t.Fatal("key derivation mutated caller request")
	}
	if !strings.HasPrefix(key, "source:") || len(key) != len("source:")+64 || strings.Contains(key, q.AuthIndex) {
		t.Fatal("key must be an opaque schedule identifier, not credential material")
	}
}

func TestSourceScheduleKeyOfficialConfigurationVersionsRemainDistinct(t *testing.T) {
	q := ob.Query{Version: 7}
	first := fr.SourceScheduleKey(fr.SourceRequest{Kind: fr.Official, Official: &q})
	q.Version = 8
	second := fr.SourceScheduleKey(fr.SourceRequest{Kind: fr.Official, Official: &q})
	if first == second {
		t.Fatal("official configuration versions shared schedule identity")
	}
	if second != fr.SourceScheduleKey(fr.SourceRequest{Kind: fr.Official, Official: &q}) {
		t.Fatal("same official source did not retain deterministic schedule identity")
	}
}
