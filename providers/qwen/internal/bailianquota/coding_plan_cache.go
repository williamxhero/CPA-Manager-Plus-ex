package bailianquota

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Coding Plan caches use the same strict subprocess contract, not the legacy
// Token Plan cache schema. A cached query never becomes account authority.
func readCodingPlanCache(path string, now time.Time, maxAge time.Duration) (CodingPlanResult, error) {
	empty := CodingPlanResult{}
	file, err := os.Open(path)
	if err != nil {
		return empty, failure("cache_file_error", "cannot_open_coding_plan_cache")
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return empty, failure("cache_file_error", "coding_plan_cache_unreadable_or_exceeds_response_limit")
	}
	result, err := DecodeCodingPlanResult(body)
	if err != nil {
		return empty, failure("invalid_cache", "coding_plan_cache_requires_complete_strict_protocol")
	}
	observed, err := time.Parse(time.RFC3339Nano, result.ObservedAt)
	if err != nil || observed.After(now) {
		return empty, failure("invalid_cache", "coding_plan_cache_has_invalid_or_future_observation")
	}
	if now.Sub(observed) > maxAge {
		return empty, failure("stale_cache", "coding_plan_cache_exceeds_max_age; refresh_required")
	}
	// Keep the original observation and window times, including their precision.
	return result, nil
}

func writeCodingPlanCache(path string, result CodingPlanResult) (string, error) {
	var body bytes.Buffer
	if writeJSON(&body, false, result) != nil || body.Len() > maxResponseBytes {
		return "", failure("cache_file_error", "coding_plan_cache_exceeds_response_limit")
	}
	// Same-directory temporary file, complete write and close before replace.
	// A query/write failure never truncates the previous service reading.
	file, err := os.CreateTemp(filepath.Dir(path), ".coding-plan-*")
	if err != nil {
		return "", failure("cache_file_error", "cannot_create_coding_plan_cache")
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(body.Bytes()); err != nil {
		file.Close()
		return "", failure("cache_file_error", "cannot_write_coding_plan_cache")
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return "", failure("cache_file_error", "cannot_sync_coding_plan_cache")
	}
	if err := file.Close(); err != nil {
		return "", failure("cache_file_error", "cannot_close_coding_plan_cache")
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return "", failure("cache_file_error", "cannot_replace_coding_plan_cache")
	}
	sum := sha256.Sum256(body.Bytes())
	return hex.EncodeToString(sum[:]), nil
}
