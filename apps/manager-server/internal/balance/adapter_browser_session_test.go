package balance_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
)

func TestBrowserSessionContractRejectsUnenrolledOrigin(t *testing.T) {
	cfg := &balance.BrowserSessionConfig{
		Origin:            "https://code28.ccwu.cc",
		DashboardPath:     "/dashboard",
		BrowserInstanceID: "edge-1",
	}
	newAPI := &balance.NewAPIConfig{
		ProtocolConfirmed: true,
		InstanceVersion:   "fixture",
		Auth:              balance.NewAPIBrowserSessionAuthV1,
		UserID:            "42",
		AccountScope:      "top-account",
		NativeUnit:        "newapi_quota",
	}
	if balance.ValidateBrowserSessionContract(cfg, newAPI) == nil {
		t.Fatal("unenrolled kele/code28 origin must fail closed")
	}
	cfg.Origin = "https://api-top.com"
	if err := balance.ValidateBrowserSessionContract(cfg, newAPI); err != nil {
		t.Fatalf("api-top.com rejected: %v", err)
	}
}

func TestBrowserSessionQueryNativeQuota(t *testing.T) {
	var stops atomic.Int32
	run := func(ctx context.Context, args ...string) ([]byte, error) {
		switch {
		case len(args) >= 1 && args[0] == "browsers":
			return []byte(`[{"instance_id":"edge-1","unresponsive":false}]`), nil
		case len(args) >= 2 && args[0] == "session" && args[1] == "start":
			return []byte(`{"session_id":"sess-1","browser_instance_id":"edge-1"}`), nil
		case len(args) >= 2 && args[0] == "session" && args[1] == "stop":
			stops.Add(1)
			return []byte(`{"stopped":["sess-1"]}`), nil
		case len(args) >= 1 && args[0] == "navigate":
			return []byte(`{"final_url":"https://api-top.com/dashboard"}`), nil
		case len(args) >= 2 && args[0] == "cooperate" && args[1] == "wait":
			return []byte(`{"ready":true}`), nil
		case len(args) >= 2 && args[0] == "cooperate" && args[1] == "sync":
			return []byte(`{"context_id":"ctx-1","observation_id":"obs-1"}`), nil
		case len(args) >= 1 && args[0] == "evaluate":
			joined := strings.Join(args, " ")
			if !strings.Contains(joined, "--context ctx-1") || !strings.Contains(joined, "--observation obs-1") {
				t.Fatalf("evaluate missing cooperate ids: %#v", args)
			}
			return []byte(`{"ok":true,"value":{"status":"ok","origin":"https://api-top.com","http_status":200,"success":true,"user_id":"42","balance":"123456"}}`), nil
		default:
			t.Fatalf("unexpected args: %#v", args)
			return nil, nil
		}
	}
	got, err := balance.NewWithBrowser(balance.NetworkOptions{}, run).Query(context.Background(), balance.QueryTarget{
		Adapter:     balance.NewAPIBrowserSessionV1,
		EndpointURL: "https://api-top.com/api/user/self",
		NewAPI: &balance.NewAPIConfig{
			ProtocolConfirmed: true,
			InstanceVersion:   "fixture",
			Auth:              balance.NewAPIBrowserSessionAuthV1,
			UserID:            "42",
			AccountScope:      "top-account",
			NativeUnit:        "newapi_quota",
		},
		BrowserSession: &balance.BrowserSessionConfig{
			Origin:            "https://api-top.com",
			DashboardPath:     "/dashboard",
			BrowserInstanceID: "edge-1",
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	if got.QueryStatus != "success" || got.NativeQuota == nil || got.NativeQuota.Remaining == nil || *got.NativeQuota.Remaining != "123456" || got.Semantic != "account_wallet" || stops.Load() != 1 {
		t.Fatalf("unexpected observation: %+v stops=%d raw=%s", got, stops.Load(), raw)
	}
	if strings.Contains(string(raw), "cookie") || got.AmountDecimal != "" {
		t.Fatalf("leaked session material or fabricated money: %s", raw)
	}
}

func TestBrowserSessionLoginRequired(t *testing.T) {
	run := func(ctx context.Context, args ...string) ([]byte, error) {
		switch {
		case args[0] == "browsers":
			return []byte(`[{"instance_id":"edge-1","unresponsive":false}]`), nil
		case args[0] == "session" && args[1] == "start":
			return []byte(`{"session_id":"sess-1","browser_instance_id":"edge-1"}`), nil
		case args[0] == "session" && args[1] == "stop":
			return []byte(`{"stopped":["sess-1"]}`), nil
		case args[0] == "navigate":
			return []byte(`{"final_url":"https://api-top.com/dashboard"}`), nil
		case args[0] == "cooperate" && args[1] == "wait":
			return []byte(`{"ready":true}`), nil
		case args[0] == "cooperate" && args[1] == "sync":
			return []byte(`{"context_id":"ctx-1","observation_id":"obs-1"}`), nil
		case args[0] == "evaluate":
			return []byte(`{"ok":true,"value":{"status":"login","origin":"https://api-top.com"}}`), nil
		default:
			t.Fatalf("unexpected args: %#v", args)
			return nil, nil
		}
	}
	got, err := balance.NewWithBrowser(balance.NetworkOptions{}, run).Query(context.Background(), balance.QueryTarget{
		Adapter: balance.NewAPIBrowserSessionV1,
		NewAPI: &balance.NewAPIConfig{
			ProtocolConfirmed: true, InstanceVersion: "fixture", Auth: balance.NewAPIBrowserSessionAuthV1,
			UserID: "42", AccountScope: "top-account", NativeUnit: "newapi_quota",
		},
		BrowserSession: &balance.BrowserSessionConfig{
			Origin: "https://api-top.com", DashboardPath: "/dashboard", BrowserInstanceID: "edge-1",
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.QueryStatus != "auth_error" || got.ErrorCode != "WEB_LOGIN_REQUIRED" || got.NativeQuota != nil {
		t.Fatalf("login required mishandled: %+v", got)
	}
}
