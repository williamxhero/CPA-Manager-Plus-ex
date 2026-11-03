package aliyunconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil || cfg.QuotaSource != SourceCommand || cfg.Command != "bailian-quota" || len(cfg.CommandArgs) != 1 || cfg.CommandArgs[0] != "--json" || cfg.CommandTimeout != 90*time.Second || cfg.CommandWorkdir != "" || cfg.DisplayName != "阿里云百炼额度（CLI）" || cfg.RequestTimeout != 20*time.Second || !cfg.IncludeBill || !cfg.DebugLogRequests || cfg.AllowHTTP || cfg.IncludeResourcePackages || cfg.Region != "cn-beijing" || len(cfg.SupportedProviders) != 1 {
		t.Fatalf("unexpected defaults: %v", err)
	}
}

func keyFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keys.json")
	// Synthetic precedence markers, not usable Alibaba credentials.
	if err := os.WriteFile(path, []byte(`{"access_key_id":"file-id","access_key_secret":"file-value","region":"file-region"}`), 0600); err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(path)
}

func TestLiteralWinsFileAndEnvironment(t *testing.T) {
	t.Setenv("ALIYUN_AK_ID", "env-id")
	t.Setenv("ALIYUN_AK_SECRET", "env-value")
	cfg, err := Load([]byte("quota-source: bss\nak-file: " + keyFile(t) + "\naccess-key-id: literal-id\naccess-key-secret: literal-value\nregion: literal-region\n"))
	if err != nil || cfg.AccessKeyID != "literal-id" || cfg.AccessKeySecret != "literal-value" || cfg.Region != "literal-region" {
		t.Fatalf("precedence failed: %v", err)
	}
}

func TestFileWinsEnvironmentReferences(t *testing.T) {
	t.Setenv("ALIYUN_AK_ID", "env-id")
	t.Setenv("ALIYUN_AK_SECRET", "env-value")
	cfg, err := Load([]byte("quota-source: bss\nak-file: " + keyFile(t) + "\naccess-key-id: '${ALIYUN_AK_ID}'\naccess-key-secret: '${ALIYUN_AK_SECRET}'\n"))
	if err != nil || cfg.AccessKeyID != "file-id" || cfg.AccessKeySecret != "file-value" || cfg.Region != "file-region" {
		t.Fatalf("file precedence failed: %v", err)
	}
}

func TestEnvironmentExpansion(t *testing.T) {
	t.Setenv("TEST_CONFIG_ID", "expanded-id")
	t.Setenv("TEST_CONFIG_VALUE", "expanded-value")
	cfg, err := Load([]byte("quota-source: bss\naccess-key-id: '${TEST_CONFIG_ID}'\naccess-key-secret: '${TEST_CONFIG_VALUE}'\n"))
	if err != nil || cfg.AccessKeyID != "expanded-id" || cfg.AccessKeySecret != "expanded-value" {
		t.Fatalf("expansion failed: %v", err)
	}
}

func TestEnvironmentFallbackOrder(t *testing.T) {
	for _, key := range []string{"ALIYUN_AK_ID", "ALIYUN_AK_SECRET", "ALIBABA_CLOUD_ACCESS_KEY_ID", "ALIBABA_CLOUD_ACCESS_KEY_SECRET"} {
		t.Setenv(key, "")
	}
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_ID", "secondary-id")
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET", "secondary-value")
	cfg, err := Load([]byte("quota-source: bss"))
	if err != nil || cfg.AccessKeyID != "secondary-id" || cfg.AccessKeySecret != "secondary-value" {
		t.Fatal("secondary environment failed")
	}
	t.Setenv("ALIYUN_AK_ID", "primary-id")
	t.Setenv("ALIYUN_AK_SECRET", "primary-value")
	cfg, err = Load([]byte("quota-source: bss"))
	if err != nil || cfg.AccessKeyID != "primary-id" || cfg.AccessKeySecret != "primary-value" {
		t.Fatal("primary environment failed")
	}
}

func TestPartialLiteralCredentials(t *testing.T) {
	cfg, err := Load([]byte("quota-source: bss\nak-file: " + keyFile(t) + "\naccess-key-id: literal-id\n"))
	if err != nil || cfg.AccessKeyID != "literal-id" || cfg.AccessKeySecret != "file-value" {
		t.Fatal("per-field precedence failed")
	}
}

func TestTimeoutClamping(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  time.Duration
	}{{"0s", time.Second}, {"-1s", time.Second}, {"500ms", time.Second}, {"4m", 120 * time.Second}, {"37s", 37 * time.Second}} {
		t.Run(tc.input, func(t *testing.T) {
			cfg, err := Load([]byte("quota-source: bss\nrequest-timeout: " + tc.input))
			if err != nil || cfg.RequestTimeout != tc.want {
				t.Fatalf("timeout %v %v", cfg.RequestTimeout, err)
			}
		})
	}
}

func TestInvalidTimeout(t *testing.T) {
	if _, err := Load([]byte("quota-source: bss\nrequest-timeout: nope")); err == nil {
		t.Fatal("expected error")
	}
}
func TestExplicitFalseAndProviderList(t *testing.T) {
	cfg, err := Load([]byte("quota-source: bss\ninclude-bill: false\ndebug-log-requests: false\ninclude-resource-packages: true\nsupported-providers: ['custom', ' ']"))
	if err != nil || cfg.IncludeBill || cfg.DebugLogRequests || !cfg.IncludeResourcePackages || len(cfg.SupportedProviders) != 1 || cfg.SupportedProviders[0] != "custom" {
		t.Fatalf("explicit flags: %v", err)
	}
}
func TestExplicitEmptyProviderList(t *testing.T) {
	cfg, err := Load([]byte("quota-source: bss\nsupported-providers: []"))
	if err != nil || len(cfg.SupportedProviders) != 0 {
		t.Fatal("empty list ignored")
	}
}
func TestInvalidYAMLRedacted(t *testing.T) {
	_, err := Load([]byte("quota-source: bss\naccess-key-secret: [private-value"))
	if err == nil || strings.Contains(err.Error(), "private-value") {
		t.Fatal("YAML error leaked value")
	}
}
func TestUnreadableAKFile(t *testing.T) {
	_, err := Load([]byte("quota-source: bss\nak-file: " + filepath.ToSlash(filepath.Join(t.TempDir(), "missing"))))
	if err == nil || err.Error() != "cannot read ak-file" {
		t.Fatal("file error")
	}
}
func TestCommandConfigurationExpansion(t *testing.T) {
	t.Setenv("QUOTA_COMMAND", "C:/path with spaces/bailian-quota.exe")
	t.Setenv("QUOTA_ARG", "  argument with spaces & ; |  ")
	t.Setenv("QUOTA_WORKDIR", "C:/working directory")
	t.Setenv("QUOTA_TIMEOUT", "125ms")
	t.Setenv("QUOTA_DISPLAY", "自定义额度")
	cfg, err := Load([]byte("command: '${QUOTA_COMMAND}'\ncommand-args: ['--json', '${QUOTA_ARG}', '']\ncommand-timeout: '${QUOTA_TIMEOUT}'\ncommand-workdir: '${QUOTA_WORKDIR}'\ndisplay-name: '${QUOTA_DISPLAY}'"))
	if err != nil || cfg.Command != os.Getenv("QUOTA_COMMAND") || cfg.CommandTimeout != 125*time.Millisecond || cfg.CommandWorkdir != os.Getenv("QUOTA_WORKDIR") || cfg.DisplayName != "自定义额度" || len(cfg.CommandArgs) != 3 || cfg.CommandArgs[1] != os.Getenv("QUOTA_ARG") || cfg.CommandArgs[2] != "" {
		t.Fatalf("command expansion: command=%q args=%q timeout=%s workdir=%q display=%q, %v", cfg.Command, cfg.CommandArgs, cfg.CommandTimeout, cfg.CommandWorkdir, cfg.DisplayName, err)
	}
}

func TestCommandExplicitEmptyArgs(t *testing.T) {
	cfg, err := Load([]byte("command-args: []"))
	if err != nil || len(cfg.CommandArgs) != 0 {
		t.Fatalf("explicit empty args ignored: %#v, %v", cfg.CommandArgs, err)
	}
}

func TestCommandTimeoutValidation(t *testing.T) {
	for _, timeout := range []string{"nope", "0s", "-1s", "999999999999999999h"} {
		t.Run(timeout, func(t *testing.T) {
			if _, err := Load([]byte("command-timeout: " + timeout)); err == nil {
				t.Fatal("invalid timeout accepted")
			}
		})
	}
	cfg, err := Load([]byte("command-timeout: 5m"))
	if err != nil || cfg.CommandTimeout != 5*time.Minute {
		t.Fatal("command timeout should not use BSS clamping", err)
	}
}

func TestQuotaSourceSelectionAndDisplayName(t *testing.T) {
	t.Setenv("QUOTA_SOURCE", "bss")
	for _, tc := range []struct{ yaml, source, display string }{
		{"", SourceCommand, "阿里云百炼额度（CLI）"},
		{"quota-source: command", SourceCommand, "阿里云百炼额度（CLI）"},
		{"quota-source: bss", SourceBSS, "阿里云余额（AK/SK）"},
		{"quota-source: '${QUOTA_SOURCE}'", SourceBSS, "阿里云余额（AK/SK）"},
		{"quota-source: bss\ndisplay-name: custom", SourceBSS, "custom"},
		{"display-name: custom", SourceCommand, "custom"},
	} {
		cfg, err := Load([]byte(tc.yaml))
		if err != nil || cfg.QuotaSource != tc.source || cfg.DisplayName != tc.display {
			t.Fatalf("source/display %q: source=%q display=%q, %v", tc.yaml, cfg.QuotaSource, cfg.DisplayName, err)
		}
	}
	for _, source := range []string{"other", "BSS", "COMMAND"} {
		if _, err := Load([]byte("quota-source: " + source)); err == nil {
			t.Fatalf("invalid source %q accepted", source)
		}
	}
}

func TestCommandDoesNotRequireBSSKeyFile(t *testing.T) {
	cfg, err := Load([]byte("ak-file: missing-keys.json"))
	if err != nil || cfg.QuotaSource != SourceCommand {
		t.Fatal("command mode depended on BSS credentials", err)
	}
}

func TestCommandArgsRequireArray(t *testing.T) {
	if _, err := Load([]byte("command-args: '--json --pretty'")); err == nil {
		t.Fatal("shell command string accepted as args")
	}
}

func TestMalformedAKFileRedacted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte(`{"access_key_secret":"private-value"`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Load([]byte("quota-source: bss\nak-file: " + filepath.ToSlash(path)))
	if err == nil || strings.Contains(err.Error(), "private-value") {
		t.Fatal("file parse leaked value")
	}
}
