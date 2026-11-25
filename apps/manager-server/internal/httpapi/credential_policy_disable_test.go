package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// Automatic pause is always on. A disable write is rejected as a stable reason
// before identity, Core or storage work, so it can never create intent, a Core
// gate change or an operation receipt, and repeating it changes nothing.
func TestCredentialPolicyRejectsDisableIntent(t *testing.T) {
	ctx := context.Background()
	var coreRequests atomic.Int64
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		coreRequests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer core.Close()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	if err := db.SaveManagerConfig(ctx, store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: core.URL, ManagementKey: "test-password"}}); err != nil {
		t.Fatal(err)
	}
	h := New(cfg, db, collector.NewManager(cfg, db)).Handler()
	var source cq.Source
	id := cp.SubjectID(source)
	body := policyBody(t, source, 0, false)
	reject := func(stage string) {
		t.Helper()
		rr := testutil.Request(t, h, "PUT", "/usage-service/credential-policy", body, testutil.AdminKey)
		testutil.RequireStatus(t, rr, 400)
		var rejected struct {
			Error string `json:"error"`
		}
		testutil.DecodeJSON(t, rr, &rejected)
		if rejected.Error != "automatic_pause_always_on" {
			t.Fatalf("%s disable rejection = %s", stage, rr.Body.String())
		}
		if again := testutil.Request(t, h, "PUT", "/usage-service/credential-policy", body, testutil.AdminKey); again.Code != rr.Code || again.Body.String() != rr.Body.String() {
			t.Fatalf("%s repeated disable is not idempotent: first=%d %s second=%d %s", stage, rr.Code, rr.Body.String(), again.Code, again.Body.String())
		}
	}
	reject("unknown subject")
	record, err := db.CredentialPolicy(ctx, id)
	if err != nil || record.Document != "" {
		t.Fatalf("rejected disable wrote a policy record: %+v %v", record, err)
	}
	// A participating intent that already exists is never rewritten by a
	// disable attempt, not even its generation.
	seeded := `{"enabled":true,"generation":4,"owner":"owner-seeded","subjectId":"` + id + `"}`
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: id, Generation: 4, Document: seeded}, 0); err != nil {
		t.Fatal(err)
	}
	reject("existing participating intent")
	record, err = db.CredentialPolicy(ctx, id)
	if err != nil || record.Generation != 4 || record.Document != seeded {
		t.Fatalf("rejected disable rewrote stored intent: %+v %v", record, err)
	}
	var wg sync.WaitGroup
	codes := make(chan int, 8)
	barrier := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			codes <- testutil.Request(t, h, "PUT", "/usage-service/credential-policy", policyBody(t, source, 4, false), testutil.AdminKey).Code
		}()
	}
	close(barrier)
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 400 {
			t.Fatalf("concurrent disable returned %d, want 400", code)
		}
	}
	if coreRequests.Load() != 0 {
		t.Fatalf("rejected disable contacted Core %d times", coreRequests.Load())
	}
	ops, err := db.PendingCredentialPolicyOperations(ctx)
	if err != nil || len(ops) != 0 {
		t.Fatalf("rejected disable created an operation receipt: %+v %v", ops, err)
	}
}
