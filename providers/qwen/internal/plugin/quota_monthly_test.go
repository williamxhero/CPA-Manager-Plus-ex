package plugin

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"cpa-credential-qwen/internal/config"
)

func TestMonthlyBLQuotaReadsTheSingleStoredWindow(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "qwen-quota.json")
	observed := time.Now().Add(-time.Minute).Format(time.RFC3339Nano)
	reset := time.Now().Add(24 * time.Hour).Format(time.RFC3339Nano)
	body := `{"source":"bl","observedAt":"` + observed + `","windows":[{"window":"1month","usedPercent":89.15,"resetTime":"` + reset + `"}]}`
	if err := os.WriteFile(cache, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{CommandArgs: []string{"--cache-only", "--cache-file", cache, "--max-age", "45m"}}
	got, ok := monthlyBLQuota(cfg)
	if !ok || len(got.Groups) != 1 || len(got.Groups[0].Buckets) != 1 || got.Groups[0].Buckets[0].Window != "billing_month" {
		t.Fatalf("monthly reading was not returned: %+v ok=%v", got, ok)
	}
	cfg.CommandArgs = []string{"--cache-file", cache, "--max-age", "45m"}
	if _, ok := monthlyBLQuota(cfg); ok {
		t.Fatal("a cache without cache-only was accepted")
	}
}
