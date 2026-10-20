// 文件名: load_test.go
// 用途: 验证配置加载优先级、未知键/密钥检测、.env 权限与启动前检查。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

func TestLoad(t *testing.T) {
	envPath := writeEnvFile(t, 0o600, "DEEPSEEK_API_KEY=sk-test\n")

	tests := []struct {
		name       string
		configPath string
		envPath    string
		wantErr    []string
		wantCode   int
		check      func(t *testing.T, cfg Config)
	}{
		{
			name:       "只有 toml 时生效",
			configPath: "testdata/valid.toml",
			check: func(t *testing.T, cfg Config) {
				t.Helper()
				if cfg.RateLimit.Search.Rps != 99.0 {
					t.Fatalf("RateLimit.Search.Rps = %v, want 99", cfg.RateLimit.Search.Rps)
				}
				if got := cfg.Source("ratelimit.search.rps"); got != SourceFile {
					t.Fatalf("Source(ratelimit.search.rps) = %q, want %q", got, SourceFile)
				}
			},
		},
		{
			name:       "默认示例配置逐字加载",
			configPath: "../../config.example.toml",
			check: func(t *testing.T, cfg Config) {
				t.Helper()
				if cfg.Server.Transport != "streamable-http" {
					t.Fatalf("示例配置 transport = %q", cfg.Server.Transport)
				}
				if cfg.Cache.Backend != "sqlite" {
					t.Fatalf("示例配置 cache.backend = %q", cfg.Cache.Backend)
				}
				if got := cfg.Source("server.http.port"); got != SourceFile {
					t.Fatalf("Source(server.http.port) = %q, want %q", got, SourceFile)
				}
			},
		},
		{
			name:       "未知键报错含行号",
			configPath: "testdata/unknown_key.toml",
			wantErr:    []string{"未知键", "hosst", "第 2 行"},
			wantCode:   domain.CodeLocConfigInvalid,
		},
		{
			name:       "toml 密钥键名被拒",
			configPath: "testdata/secret_key.toml",
			wantErr:    []string{"禁止在 config.toml 放密钥", "api_key", "第 2 行"},
			wantCode:   domain.CodeLocConfigInvalid,
		},
		{
			name:       "toml 密钥值形态被拒",
			configPath: "testdata/secret_value.toml",
			wantErr:    []string{"值形态疑似密钥", "note", "第 2 行"},
			wantCode:   domain.CodeLocConfigInvalid,
		},
		{
			name:       "synth 开启被拒",
			configPath: "testdata/synth.toml",
			wantErr:    []string{"synth.enabled", "阶段二"},
			wantCode:   domain.CodeValSynthDisabled,
		},
		{
			name:       "stdio 日志 stdout 被拒",
			configPath: "testdata/stdio_stdout.toml",
			wantErr:    []string{"stdout", "transport=stdio"},
			wantCode:   domain.CodeLocConfigInvalid,
		},
		{
			name:       "hedge 段被拒",
			configPath: "testdata/hedge.toml",
			wantErr:    []string{"未知键", "hedge"},
			wantCode:   domain.CodeLocConfigInvalid,
		},
		{
			name:       "config.toml 不存在报错",
			configPath: "testdata/does_not_exist.toml",
			wantErr:    []string{"读取 config.toml 失败"},
			wantCode:   domain.CodeLocConfigInvalid,
		},
		{
			name:       ".env 不存在报错",
			configPath: "testdata/valid.toml",
			envPath:    "testdata/does_not_exist.env",
			wantErr:    []string{"读取 .env 失败"},
			wantCode:   domain.CodeLocNoAPIKey,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := tt.envPath
			if env == "" {
				env = envPath
			}
			cfg, err := Load(tt.configPath, env)

			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("Load() 返回错误: %v", err)
				}
				tt.check(t, cfg)
				return
			}
			if err == nil {
				t.Fatalf("Load() 期望失败，实际成功")
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %q, 缺少 %q", err, want)
				}
			}
			if got := domain.CodeOf(err); got != tt.wantCode {
				t.Fatalf("错误码 = %d, want %d（error: %v）", got, tt.wantCode, err)
			}
		})
	}
}

func TestLoadEnvOverridesToml(t *testing.T) {
	t.Setenv("DEEPSEEKSEARCH_LOG_LEVEL", "debug")
	envPath := writeEnvFile(t, 0o600, "DEEPSEEK_API_KEY=sk-test\n")

	cfg, err := Load("testdata/env_priority.toml", envPath)
	if err != nil {
		t.Fatalf("Load() 返回错误: %v", err)
	}
	if cfg.Log.Level != "debug" {
		t.Fatalf("Log.Level = %q, want debug", cfg.Log.Level)
	}
	if got := cfg.Source("log.level"); got != SourceEnv {
		t.Fatalf("Source(log.level) = %q, want %q", got, SourceEnv)
	}
}

func TestLoadDotEnvPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不校验 POSIX 权限")
	}

	wide := writeEnvFile(t, 0o644, "DEEPSEEK_API_KEY=sk-test\n")
	if _, err := Load("testdata/valid.toml", wide); err == nil {
		t.Fatal("0644 的 .env 应被拒绝")
	} else if !strings.Contains(err.Error(), "权限过宽") || !strings.Contains(err.Error(), "0644") {
		t.Fatalf("error = %q, 应包含权限过宽与 0644", err)
	}

	secure := writeEnvFile(t, 0o400, "DEEPSEEK_API_KEY=sk-test\n")
	if _, err := Load("testdata/valid.toml", secure); err != nil {
		t.Fatalf("0400 是更严格的权限，应放行，实际: %v", err)
	}
}

func TestLoadDotEnvDoesNotOverrideEnv(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "from-env")
	envPath := writeEnvFile(t, 0o600, "DEEPSEEK_API_KEY=from-dotenv\n")

	cfg, err := Load("testdata/valid.toml", envPath)
	if err != nil {
		t.Fatalf("Load() 返回错误: %v", err)
	}
	if cfg.ApiKey != "from-env" {
		t.Fatalf("ApiKey = %q, want from-env（.env 不得覆盖真实环境变量）", cfg.ApiKey)
	}
	if got := cfg.Source("api_key"); got != SourceEnv {
		t.Fatalf("Source(api_key) = %q, want %q", got, SourceEnv)
	}
}

func TestLoadConfigPathEnv(t *testing.T) {
	override := filepath.Join(t.TempDir(), "override.toml")
	if err := os.WriteFile(override, []byte("[ratelimit.search]\nrps = 77.0\n"), 0o600); err != nil {
		t.Fatalf("写入临时配置失败: %v", err)
	}
	t.Setenv("DEEPSEEKSEARCH_CONFIG", override)
	envPath := writeEnvFile(t, 0o600, "DEEPSEEK_API_KEY=sk-test\n")

	cfg, err := Load("testdata/valid.toml", envPath)
	if err != nil {
		t.Fatalf("Load() 返回错误: %v", err)
	}
	if cfg.RateLimit.Search.Rps != 77.0 {
		t.Fatalf("RateLimit.Search.Rps = %v, want 77（DEEPSEEKSEARCH_CONFIG 应覆盖路径）", cfg.RateLimit.Search.Rps)
	}
}

func TestLoadFromEnvOnly(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-env")
	t.Setenv("DEEPSEEK_BASE_URL", "https://example.test")
	t.Setenv("DEEPSEEKSEARCH_TRANSPORT", "stdio")
	t.Setenv("DEEPSEEKSEARCH_LOG_LEVEL", "warn")

	cfg, err := LoadFromEnvOnly()
	if err != nil {
		t.Fatalf("LoadFromEnvOnly() 返回错误: %v", err)
	}
	if cfg.ApiKey != "sk-env" || cfg.Upstream.BaseURL != "https://example.test" {
		t.Fatalf("环境变量未生效: ApiKey=%q BaseURL=%q", cfg.ApiKey, cfg.Upstream.BaseURL)
	}
	if cfg.Server.Transport != "stdio" || cfg.Log.Level != "warn" {
		t.Fatalf("环境变量未生效: Transport=%q LogLevel=%q", cfg.Server.Transport, cfg.Log.Level)
	}
	if got := cfg.Source("upstream.base_url"); got != SourceEnv {
		t.Fatalf("Source(upstream.base_url) = %q, want %q", got, SourceEnv)
	}
}

func TestLoadValueSemantics(t *testing.T) {
	envPath := writeEnvFile(t, 0o600, "DEEPSEEK_API_KEY=sk-test\n")

	first, err := Load("testdata/valid.toml", envPath)
	if err != nil {
		t.Fatalf("Load() 返回错误: %v", err)
	}
	first.RateLimit.Search.Rps = 1.0
	first.Dedupe.TrackingParams[0] = "mutated"

	second, err := Load("testdata/valid.toml", envPath)
	if err != nil {
		t.Fatalf("第二次 Load() 返回错误: %v", err)
	}
	if second.RateLimit.Search.Rps != 99.0 {
		t.Fatalf("返回值被修改后影响了后续 Load: Rps = %v", second.RateLimit.Search.Rps)
	}
	if second.Dedupe.TrackingParams[0] != "utm_source" {
		t.Fatalf("返回值修改切片后影响了后续 Load: %q", second.Dedupe.TrackingParams[0])
	}
}

func writeEnvFile(t *testing.T, mode os.FileMode, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("写入 .env 失败: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("设置 .env 权限失败: %v", err)
	}
	return path
}
