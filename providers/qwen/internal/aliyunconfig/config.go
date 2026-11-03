package aliyunconfig

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	SourceCommand = "command"
	SourceBSS     = "bss"
)

type Config struct {
	QuotaSource             string
	Command                 string
	CommandArgs             []string
	CommandTimeout          time.Duration
	CommandWorkdir          string
	DisplayName             string
	AccessKeyID             string
	AccessKeySecret         string
	Region                  string
	Endpoint                string
	AllowHTTP               bool
	RequestTimeout          time.Duration
	SupportedProviders      []string
	IncludeBill             bool
	IncludeResourcePackages bool
	DebugLogRequests        bool
}

type rawConfig struct {
	QuotaSource             string    `yaml:"quota-source"`
	Command                 string    `yaml:"command"`
	CommandArgs             *[]string `yaml:"command-args"`
	CommandTimeout          string    `yaml:"command-timeout"`
	CommandWorkdir          string    `yaml:"command-workdir"`
	DisplayName             string    `yaml:"display-name"`
	AccessKeyID             string    `yaml:"access-key-id"`
	AccessKeySecret         string    `yaml:"access-key-secret"`
	AKFile                  string    `yaml:"ak-file"`
	Region                  string    `yaml:"region"`
	Endpoint                string    `yaml:"endpoint"`
	AllowHTTP               bool      `yaml:"allow-http"`
	RequestTimeout          string    `yaml:"request-timeout"`
	SupportedProviders      *[]string `yaml:"supported-providers"`
	IncludeBill             *bool     `yaml:"include-bill"`
	IncludeResourcePackages bool      `yaml:"include-resource-packages"`
	DebugLogRequests        *bool     `yaml:"debug-log-requests"`
}

func Load(data []byte) (Config, error) {
	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Config{}, errors.New("invalid plugin configuration YAML")
	}
	source := first(os.ExpandEnv(raw.QuotaSource), SourceCommand)
	if source != SourceCommand && source != SourceBSS {
		return Config{}, errors.New("quota-source must be command or bss")
	}
	var file struct {
		AccessKeyID     string `json:"access_key_id"`
		AccessKeySecret string `json:"access_key_secret"`
		Region          string `json:"region"`
	}
	if path := strings.TrimSpace(os.ExpandEnv(raw.AKFile)); source == SourceBSS && path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, errors.New("cannot read ak-file")
		}
		if err := json.Unmarshal(data, &file); err != nil {
			return Config{}, errors.New("invalid ak-file JSON")
		}
	}
	displayName := "阿里云百炼额度（CLI）"
	if source == SourceBSS {
		displayName = "阿里云余额（AK/SK）"
	}
	cfg := Config{
		QuotaSource:             source,
		Command:                 first(os.ExpandEnv(raw.Command), "bailian-quota"),
		CommandArgs:             []string{"--json"},
		CommandTimeout:          90 * time.Second,
		CommandWorkdir:          strings.TrimSpace(os.ExpandEnv(raw.CommandWorkdir)),
		DisplayName:             first(os.ExpandEnv(raw.DisplayName), displayName),
		AccessKeyID:             credential(raw.AccessKeyID, file.AccessKeyID, "ALIYUN_AK_ID", "ALIBABA_CLOUD_ACCESS_KEY_ID"),
		AccessKeySecret:         credential(raw.AccessKeySecret, file.AccessKeySecret, "ALIYUN_AK_SECRET", "ALIBABA_CLOUD_ACCESS_KEY_SECRET"),
		Region:                  first(os.ExpandEnv(raw.Region), file.Region, "cn-beijing"),
		Endpoint:                first(os.ExpandEnv(raw.Endpoint), "https://business.aliyuncs.com/"),
		AllowHTTP:               raw.AllowHTTP,
		RequestTimeout:          20 * time.Second,
		SupportedProviders:      []string{"openai-compatibility"},
		IncludeBill:             true,
		IncludeResourcePackages: raw.IncludeResourcePackages,
		DebugLogRequests:        true,
	}
	if raw.CommandArgs != nil {
		cfg.CommandArgs = make([]string, len(*raw.CommandArgs))
		for i, arg := range *raw.CommandArgs {
			cfg.CommandArgs[i] = os.ExpandEnv(arg)
		}
	}
	if raw.CommandTimeout != "" {
		d, err := time.ParseDuration(os.ExpandEnv(raw.CommandTimeout))
		if err != nil || d <= 0 {
			return Config{}, errors.New("command-timeout must be a positive duration")
		}
		cfg.CommandTimeout = d
	}
	if raw.RequestTimeout != "" {
		d, err := time.ParseDuration(os.ExpandEnv(raw.RequestTimeout))
		if err != nil {
			return Config{}, errors.New("invalid request-timeout duration")
		}
		if d < time.Second {
			d = time.Second
		}
		if d > 120*time.Second {
			d = 120 * time.Second
		}
		cfg.RequestTimeout = d
	}
	if raw.SupportedProviders != nil {
		cfg.SupportedProviders = nil
		for _, provider := range *raw.SupportedProviders {
			if p := strings.TrimSpace(os.ExpandEnv(provider)); p != "" {
				cfg.SupportedProviders = append(cfg.SupportedProviders, p)
			}
		}
	}
	if raw.IncludeBill != nil {
		cfg.IncludeBill = *raw.IncludeBill
	}
	if raw.DebugLogRequests != nil {
		cfg.DebugLogRequests = *raw.DebugLogRequests
	}
	return cfg, nil
}

// Environment references are fallback values, not literal overrides of the AK file.
func credential(raw, file, primary, secondary string) string {
	if raw != "" && !strings.Contains(raw, "${") && !strings.Contains(raw, "$") {
		return first(raw, file, os.Getenv(primary), os.Getenv(secondary))
	}
	return first(file, os.ExpandEnv(raw), os.Getenv(primary), os.Getenv(secondary))
}

func first(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
