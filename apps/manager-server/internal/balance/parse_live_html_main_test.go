package balance_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
)

func TestParseCapturedLiveModelsHTML(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "verification", "issue97-live-20261104", "models-html-live3.json"))
	if err != nil {
		t.Skip(err)
	}
	page, err := balance.ParseLaysoDashboardModelsHTML(raw)
	if err != nil {
		t.Fatal(err)
	}
	if page.AccountID != "h7w8" || page.Amount == nil || *page.Amount != "46.92" {
		t.Fatalf("unexpected page: %+v", page)
	}
}
