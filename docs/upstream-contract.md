# P0：Darkbloom 收益接口复验

复验日期：2026-10-16（Asia/Shanghai）。目标设备使用既有 SSH 别名 `bloom-01`，Darkbloom 版本 **0.9.22**，凭证位于该设备运行用户的 `~/.darkbloom/auth_token`。连接方式与用户提供的“tailscale ssh配置”聊天记录一致。脚本在目标设备上读取凭证、请求官方 HTTPS 接口，仅带回字段类型、计数、格式和脱敏示例。

## 已观察的请求

接口为 `GET https://api.darkbloom.dev/v1/provider/account-earnings?limit=N`，认证使用设备文件中的 Bearer 令牌。原始观测摘要见 [p0-upstream-observation.json](p0-upstream-observation.json)。

| 请求 | HTTP 状态 | 实际返回 | `history_limit` |
| --- | --- | --- | --- |
| 不带认证，limit=50 | 401 | 结构化错误 | — |
| 带认证，limit=50 | 200 | 50 条 | 50 |
| 带认证，limit=1000 | 200 | 1000 条 | 1000 |
| 带认证，limit=1001 | 200 | 1000 条 | 1000 |

此设备与接口的本次样本中，`recent_count` 等于响应数组长度，各行账户一致，没有重复 ID，按发生时间降序。观察到的最大返回量为 1000；这不是分页能力或完整历史的证明。1001 只用于一次边界复验方案，采集器最大请求值仍为 1000。

## 字段与解析口径

| 官方字段 | 实测 JSON 类型 | 本地处理 |
| --- | --- | --- |
| `account_id` | string | 保留文本，明细必须与汇总一致；配置账户不匹配时整轮失败 |
| `count` / `recent_count` / `history_limit` | integer | 与当前数组和请求上限校验；`count` 对应官方账户累计记录数 |
| `total_micro_usd` | integer | int64，官方累计；不受本地 365 天保留范围截断 |
| `available_balance_micro_usd` / `withdrawable_balance_micro_usd` | integer | int64 |
| `total_usd` / 两种 `*_balance_usd` | string | 仅官方显示值；不参与金额换算或入账 |
| `email` | string | 丢弃，不入库、不写日志 |
| `earnings[].id` | integer | 直接从 JSON 原文转文本，不经过浮点；也接受非空字符串 ID |
| `provider_id` / `provider_key` / `job_id` / `model` | string | 保留源值；Provider 标识不能直接视为物理设备 ID |
| `amount_micro_usd` | integer | int64，允许负调整与合法零值，溢出或小数失败 |
| `prompt_tokens` / `completion_tokens` | integer | 非负 int64，映射输入和输出 Token |
| `created_at` | string | 本次为 UTC RFC3339、`Z` 结尾，小数秒有 3/4/5/6 位；存为 UTC 微秒 |

缺失、null、错误类型、账户冲突、整数溢出、冲突重复 ID 和畸形 JSON 均不作为有效快照。相同 ID、相同内容折叠；同一响应中同 ID 内容冲突则失败。跨响应修正已由 P2 采集器实现，以后取得的有效字段为准。`history_limit` 必须与本次请求窗口一致，防止服务端意外缩小窗口后被误判为完整读取。

当前解码器还接受带明确偏移量的 RFC3339 时间并归一化为 UTC；这是解析兼容范围，偏移形式及文本 ID 未在本次真实响应中出现，使用合成样本验证。超过微秒的非零精度明确拒绝，避免静默丢失。

## 错误与未知能力

实测未认证错误结构为 `{"error":{"code":"string","message":"string","type":"string"}}`，保留结构，不持久化原始消息。程序错误分类使用固定安全代码：

| 情况 | 内部分类 | 处理约定 |
| --- | --- | --- |
| 401 / 403 | `authentication_failed` | 保留旧数据，下个正常周期再检查凭证；不在一轮内反复认证 |
| 429 | `rate_limited` | 已实现 Retry-After 秒数/HTTP 日期解析与持久化截止时间，缺失或无效时等待一个正常周期 |
| 5xx | `upstream_unavailable` | 后续周期重试 |
| 3xx | `redirect_rejected` | 不转发官方认证到其他来源；P2 客户端拒绝自动重定向，包括同源重定向 |
| 其他非成功状态 | `upstream_request_rejected` | 显式失败，不伪造空快照 |
| 超时、网络错误、结构校验失败 | 对应安全错误 | 后续周期重试，成功检查点不推进；P2 已实现，超时覆盖响应头与响应体读取阶段 |

本次只触发了 401。403、429、5xx、重定向、超时和异常响应使用合成契约样本/测试，**没有通过压测触发真实限流**。`Retry-After` 的真实服务端形式和限流配额仍待自然出现时核验。

未验证：游标、since、按时间分页、按更新时间读取、账户汇总与明细的原子快照、修正的历史范围、窗口外补回能力、Provider 与设备的可靠映射。不添加这些请求参数，不承诺窗口外历史完整。

## 样本与复验

[样本目录](../apps/agent/testdata/upstream/)包含正常、重复、冲突、缺失金额、溢出、畸形 JSON、认证和限流结构。`observed-redacted.json` 来自已观察结构，所有身份、金额、Token 数和时间均被替换，数组缩为两条、计数相应调整。其余样本是明确的合成测试数据。

复验时在仓库根目录执行：

```sh
ssh -o BatchMode=yes -o StrictHostKeyChecking=yes bloom-01 python3 - < scripts/probe-upstream.py
```

脚本不打印令牌、真实金额、邮箱、Provider 标识或原始响应，不修改远端文件或 Provider 状态。输出的 `timestamp_formats` 使用 `#` 替换所有数字。脚本兼容目标设备的 Python 3.9；小数秒用 `strptime` 解析，避免旧版 `fromisoformat` 对 4/5 位小数秒的限制。
