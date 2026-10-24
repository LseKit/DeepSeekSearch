// 文件名: env.go
// 用途: 读取 Config/.env 与真实环境变量并记录来源，绝不把 .env 写回进程环境。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package config

import (
	"errors"
	"io/fs"
	"os"
	"runtime"

	"github.com/joho/godotenv"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

// applyDotEnv 读取 .env 并只应用两个凭据键。
// 用 godotenv.Read 而不是 Load/Overload：后两者内部 os.Setenv，会污染子进程与同进程测试。
func applyDotEnv(cfg *Config, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// 纯环境变量部署是合法配置；凭据缺失由 checkPhaseOne 统一报 3040。
			return nil
		}
		return configErr(domain.CodeLocNoAPIKey, "读取 .env 失败: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return configErr(domain.CodeLocConfigInvalid,
			".env 权限过宽：0%o（要求 group/other 无任何权限，如 0600 或 0400）", info.Mode().Perm())
	}

	values, err := godotenv.Read(path)
	if err != nil {
		return configErr(domain.CodeLocConfigInvalid, "解析 .env 失败: %v", err)
	}
	applyDotEnvValue(cfg, values, "DEEPSEEK_API_KEY", "api_key", func(v string) { cfg.ApiKey = v })
	tokenEnv := cfg.Server.Http.AuthTokenEnv
	if tokenEnv == "" {
		tokenEnv = "DEEPSEEKSEARCH_HTTP_TOKEN"
	}
	applyDotEnvValue(cfg, values, tokenEnv, "http_token", func(v string) { cfg.HttpToken = v })
	return nil
}

func applyDotEnvValue(cfg *Config, values map[string]string, envName, sourceKey string, set func(string)) {
	value, ok := values[envName]
	if !ok {
		return
	}
	// 真实环境变量优先级更高；.env 不得覆盖它。
	if _, exists := os.LookupEnv(envName); exists {
		return
	}
	set(value)
	cfg.sources[sourceKey] = SourceDotEnv
}

// applyEnv 应用环境变量覆盖。DEEPSEEKSEARCH_CONFIG 已在 Load 中作为路径处理。
func applyEnv(cfg *Config) {
	if value, ok := os.LookupEnv("DEEPSEEK_API_KEY"); ok {
		cfg.ApiKey = value
		cfg.sources["api_key"] = SourceEnv
	}

	// auth_token_env 让 token 的变量名可配；为空时回落到内置默认名。
	tokenEnv := cfg.Server.Http.AuthTokenEnv
	if tokenEnv == "" {
		tokenEnv = "DEEPSEEKSEARCH_HTTP_TOKEN"
	}
	if value, ok := os.LookupEnv(tokenEnv); ok {
		cfg.HttpToken = value
		cfg.sources["http_token"] = SourceEnv
	}

	if value, ok := os.LookupEnv("DEEPSEEK_BASE_URL"); ok {
		cfg.Upstream.BaseURL = value
		cfg.sources["upstream.base_url"] = SourceEnv
	}
	if value, ok := os.LookupEnv("DEEPSEEKSEARCH_TRANSPORT"); ok {
		cfg.Server.Transport = value
		cfg.sources["server.transport"] = SourceEnv
	}
	if value, ok := os.LookupEnv("DEEPSEEKSEARCH_LOG_LEVEL"); ok {
		cfg.Log.Level = value
		cfg.sources["log.level"] = SourceEnv
	}
}
