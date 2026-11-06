package aliyunplugin

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	config "cpa-credential-qwen/internal/aliyunconfig"
	"cpa-credential-qwen/internal/quotawire"
)

func fetchCommand(cfg config.Config, req pluginapi.QuotaFetchRequest) (*pluginapi.QuotaFetchResponse, error) {
	secrets := []string{cfg.AccessKeyID, cfg.AccessKeySecret}
	// These values are redaction input only. The selected credential must never
	// be added to command arguments, stdin, a working directory or environment.
	for key, value := range req.Attributes {
		secrets = append(secrets, key, value)
	}
	if !collectSecrets(req.Metadata, &secrets, 0) {
		return nil, errors.New("quota request metadata exceeds safe traversal depth")
	}
	var storage any
	if json.Unmarshal(req.StorageJSON, &storage) == nil {
		if !collectSecrets(storage, &secrets, 0) {
			return nil, errors.New("quota request storage exceeds safe traversal depth")
		}
	}
	out, err := quotawire.Run(context.Background(), cfg.Command, cfg.CommandArgs, cfg.CommandWorkdir, cfg.CommandTimeout, secrets)
	if err != nil {
		return nil, err
	}
	result, err := quotawire.Decode(out, cfg.DisplayName)
	if err != nil {
		return nil, errors.New(quotawire.Sanitize(err.Error(), cfg.CommandArgs, secrets))
	}
	return &result, nil
}

// A bounded walk rejects over-depth/cyclic metadata before running the helper.
// Silently stopping would omit secrets while still exposing its diagnostics.
// ABI JSON itself cannot contain cycles. Nothing here is logged.
func collectSecrets(value any, into *[]string, depth int) bool {
	if depth > 32 {
		return false
	}
	switch value := value.(type) {
	case string:
		*into = append(*into, value)
	case map[string]any:
		for key, item := range value {
			*into = append(*into, key)
			if !collectSecrets(item, into, depth+1) {
				return false
			}
		}
	case []any:
		for _, item := range value {
			if !collectSecrets(item, into, depth+1) {
				return false
			}
		}
	}
	return true
}
