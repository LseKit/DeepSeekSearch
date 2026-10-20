# 变更日志

本文件遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 结构。

## [未发布]

<!-- 下一个版本尚未发布的改动记录在这里。 -->

## [v0.1.0] - 2026-10-20

### 新增

- 项目骨架：`cmd/deepseeksearch` 入口与 `internal/` 六个包占位。
- 构建系统：`Makefile` 与编译期版本注入（`-ldflags -X` 注入 version/commit/buildTime）。
- `--version` 输出（格式以 `Contract/17` §3.1 为准，go 版本用 `runtime.Version()` 运行时取）。
- 配置样例 `config.example.toml`，与 `Contract/13` §二逐字一致。

### 已知限制

本版本为骨架，尚无业务逻辑。面向使用者的能力边界以 [README.md](README.md#已知限制) 的「已知限制」9 条为准，此处不重复。
