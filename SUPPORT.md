# 获取帮助

先看这里，能解决 80% 的问题。

## 我该去哪里问

| 你想做什么 | 去哪里 |
|---|---|
| 报告一个 bug | [新建 issue](https://github.com/LseKit/DeepSeekSearch/issues/new?template=bug_report.yml) |
| 提一个功能需求 | [新建 issue](https://github.com/LseKit/DeepSeekSearch/issues/new?template=feature_request.yml) |
| 问「怎么配置」 | 先读 [`docs/Contract/13-配置契约.md`](docs/Contract/13-配置契约.md)，仍不懂再开 issue |
| 问「部署出错怎么办」 | 先读 [`docs/Ops/30-部署运维手册.md`](docs/Ops/30-部署运维手册.md) §六「故障排查」 |
| 上报安全漏洞 | **不要开 issue**，见 [SECURITY.md](SECURITY.md) |
| 想知道某个功能什么时候做 | 查 [`docs/Decisions/16-技术债登记表.md`](docs/Decisions/16-技术债登记表.md) |

## 开 issue 前请自检

1. 跑一次 `DeepSeekSearch --version`，把完整输出贴进 issue
2. 跑一次 `./Bin/DeepSeekSearch -check -config Config/config.toml`，确认配置本身没问题
3. 如果是「搜不到 / 抓不到」，先看 [`docs/Contract/14-错误契约.md`](docs/Contract/14-错误契约.md) §2.3 —— 大部分情况是上游统一返回 404
4. 贴日志时**把 API Key 替换成 `sk-***`**

## 本项目不提供

- DeepSeek 账号、余额、计费相关的支持（找 DeepSeek 官方）
- 上游搜索质量 / 抓取成功率的保证
- 商业支持或 SLA
