// 文件名: load.go
// 用途: 按「环境变量 > .env > config.toml > 默认值」合并配置，并在加载期拒绝未知键、密钥与阶段一禁用项。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

// Load 按「环境变量 > Config/.env > config.toml > 内置默认值」优先级合并。
// 任一项不合法则返回 error，调用方 MUST 直接退出。
func Load(configPath, envPath string) (Config, error) {
	// DEEPSEEKSEARCH_CONFIG 是路径覆盖而非 Config 字段，故不写 sources。
	if override := os.Getenv("DEEPSEEKSEARCH_CONFIG"); override != "" {
		configPath = override
	}

	cfg := Default()
	if err := applyToml(&cfg, configPath); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	if err := applyDotEnv(&cfg, envPath); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	applyEnv(&cfg)

	if _, err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}

// LoadFromEnvOnly 只用环境变量 + 内置默认值构造配置，不读任何文件。
// 供 stdio 单机场景与测试使用。
func LoadFromEnvOnly() (Config, error) {
	cfg := Default()
	applyEnv(&cfg)
	if _, err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}

// applyToml 解析 config.toml。密钥检查先于未知键检查：
// [upstream] api_key 这类键同时命中两条规则，用户更需要看到「禁止放密钥」而不是「拼错了」。
func applyToml(cfg *Config, path string) error {
	data, err := os.ReadFile(path) //nolint:gosec // 配置文件路径由用户显式指定，属预期输入
	if err != nil {
		return configErr(domain.CodeLocConfigInvalid, "读取 config.toml 失败: %v", err)
	}
	if err := checkTomlSecrets(data); err != nil {
		return err
	}
	if err := rejectHedgeTable(data); err != nil {
		return err
	}

	md, err := toml.Decode(string(data), cfg)
	if err != nil {
		return configErr(domain.CodeLocConfigInvalid, "解析 config.toml 失败: %v", err)
	}
	if err := unknownKeysError(&md, data); err != nil {
		return err
	}
	markTomlSources(cfg, &md)
	return nil
}

// markTomlSources 只标记模板里真正写出来的叶子键；默认值来源保持 default。
func markTomlSources(cfg *Config, md *toml.MetaData) {
	for _, key := range md.Keys() {
		path := key.String()
		if _, known := cfg.sources[path]; known {
			cfg.sources[path] = SourceFile
		}
	}
}

// checkPhaseOne 是启动检查的前半段：凭据、阶段一禁用项与 stdio 的 stdout 冲突。
// Validate 会复用它，再补齐枚举、数值区间与其余跨字段约束。
func checkPhaseOne(cfg *Config) error {
	var errs []error
	if strings.TrimSpace(cfg.ApiKey) == "" {
		errs = append(errs, configErr(domain.CodeLocNoAPIKey,
			"DEEPSEEK_API_KEY 未配置：在 Config/.env 或环境变量中设置"))
	}
	if cfg.Server.Transport == "stdio" && isStdoutDest(cfg.Log.Dir) {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"transport=stdio 时日志目的地不能是 stdout（log.dir=%q）：stdout 是 MCP 协议通道", cfg.Log.Dir))
	}
	return errors.Join(errs...)
}

// isStdoutDest 识别把日志写到 stdout 的写法。
// 配置里没有独立的 dest 键，dir 是唯一目的地；"-" 与 /dev/stdout 是常见的 Unix 别名，
// /dev/fd/1 与 /proc/self/fd/1 指向同一个 fd（漏了它们，stdio 的协议通道就会被日志污染）。
func isStdoutDest(dir string) bool {
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case "stdout", "-", "/dev/stdout", "/dev/fd/1", "/proc/self/fd/1":
		return true
	default:
		return false
	}
}

func configErr(code int, format string, args ...any) error {
	//nolint:wrapcheck // 这是错误构造器，不是把外部包错误原样透传
	return domain.NewLocalError(code, fmt.Sprintf(format, args...), nil)
}
