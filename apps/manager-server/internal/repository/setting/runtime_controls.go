package setting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

var safeBalanceKey = regexp.MustCompile(`^(gemini|interactions|codex|xai|meta|claude|vertex|openai):[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var safeAutoID = regexp.MustCompile(`^(gemini|interactions|codex|xai|meta|claude|vertex|openai):([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|[0-9]{1,6})$`)

const runtimeControlsKey = "runtime_controls_v1"

type RuntimeControls struct {
	QuotaRefreshIntervalMs int64             `json:"quotaRefreshIntervalMs"`
	QuotaFetchedAtMs       int64             `json:"quotaFetchedAtMs"`
	AutoStartStopEnabled   bool              `json:"autoStartStopEnabled"`
	AutoStartStopIDs       []string          `json:"autoStartStopIds"`
	BalanceTexts           map[string]string `json:"balanceTexts"`
}

func DefaultRuntimeControls() RuntimeControls {
	return RuntimeControls{
		QuotaRefreshIntervalMs: 3_600_000,
		AutoStartStopIDs:       []string{},
		BalanceTexts:           map[string]string{},
	}
}

func LoadRuntimeControls(ctx context.Context, db *sql.DB) (RuntimeControls, error) {
	var raw string
	err := db.QueryRowContext(ctx, "select value from settings where key = ?", runtimeControlsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultRuntimeControls(), nil
	}
	if err != nil {
		return RuntimeControls{}, err
	}
	controls := DefaultRuntimeControls()
	if err := json.Unmarshal([]byte(raw), &controls); err != nil {
		return RuntimeControls{}, err
	}
	return normalizeRuntimeControls(controls), nil
}

func SaveRuntimeControls(ctx context.Context, db *sql.DB, controls RuntimeControls) error {
	controls = normalizeRuntimeControls(controls)
	raw, err := json.Marshal(controls)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "insert into settings(key,value,updated_at_ms) values(?,?,?) on conflict(key) do update set value=excluded.value,updated_at_ms=excluded.updated_at_ms", runtimeControlsKey, string(raw), time.Now().UnixMilli())
	return err
}

func normalizeRuntimeControls(controls RuntimeControls) RuntimeControls {
	if controls.QuotaRefreshIntervalMs < 0 || controls.QuotaRefreshIntervalMs > 86_400_000 {
		controls.QuotaRefreshIntervalMs = 3_600_000
	}
	if controls.QuotaFetchedAtMs < 0 {
		controls.QuotaFetchedAtMs = 0
	}
	ids := make([]string, 0, len(controls.AutoStartStopIDs))
	seen := map[string]struct{}{}
	for _, id := range controls.AutoStartStopIDs {
		if !safeAutoID.MatchString(id) {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
		if len(ids) == 500 {
			break
		}
	}
	controls.AutoStartStopIDs = ids
	if controls.BalanceTexts == nil {
		controls.BalanceTexts = map[string]string{}
	}
	for key, value := range controls.BalanceTexts {
		if !safeBalanceKey.MatchString(key) || value == "" || len(value) > 80 {
			delete(controls.BalanceTexts, key)
		}
	}
	if len(controls.BalanceTexts) > 200 {
		trimmed := make(map[string]string, 200)
		for key, value := range controls.BalanceTexts {
			if len(trimmed) == 200 {
				break
			}
			trimmed[key] = value
		}
		controls.BalanceTexts = trimmed
	}
	return controls
}
