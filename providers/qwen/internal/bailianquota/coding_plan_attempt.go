package bailianquota

import (
	"os"
	"path/filepath"
)

// A receipt binds cache production/freshness only, never selected-key/account
// authority. Same-cache producers must be externally serialized.
type codingPlanCacheAttempt struct {
	Version     int    `json:"version"`
	State       string `json:"state"`
	StartedAt   string `json:"startedAt"`
	CompletedAt string `json:"completedAt,omitempty"`
	ObservedAt  string `json:"observedAt,omitempty"`
	CacheSHA256 string `json:"cacheSHA256,omitempty"`
}

func writeCodingPlanAttempt(cachePath string, attempt codingPlanCacheAttempt) error {
	path := cachePath + ".attempt.json"
	file, err := os.CreateTemp(filepath.Dir(path), ".coding-plan-attempt-*")
	if err != nil {
		return failure("cache_attempt_error", "cannot_publish_coding_plan_cache_attempt")
	}
	defer os.Remove(file.Name())
	if err := writeJSON(file, false, attempt); err != nil {
		file.Close()
		return failure("cache_attempt_error", "cannot_publish_coding_plan_cache_attempt")
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return failure("cache_attempt_error", "cannot_publish_coding_plan_cache_attempt")
	}
	if err := file.Close(); err != nil {
		return failure("cache_attempt_error", "cannot_publish_coding_plan_cache_attempt")
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return failure("cache_attempt_error", "cannot_publish_coding_plan_cache_attempt")
	}
	return nil
}
