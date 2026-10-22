# 设备状态采集

设备端功能，默认启用；每次启动立即采集，之后每 60 秒采集一次，覆盖写入本机 `agent.db` 中的最新一条 telemetry。采集与收益请求独立，收益接口失败、限流或 `collection_enabled=false` 时仍采集。休眠/进程停止期间没有样本，恢复后不补造历史。

当前实现面向已发布的 macOS / Apple Silicon Agent，不增加运行依赖，不需要管理员权限。其他系统指标标记为 `unsupported`。

## 指标与口径

| 字段 | 来源与含义 |
| --- | --- |
| `cpu_percent` | `iostat` 第二个样本的一秒区间整机 CPU 使用率，0～100；不使用开机以来平均值，也不是整分钟平均值 |
| `provider_cpu_percent` | 已核对进程身份的 Darkbloom 进程 `ps pcpu` 近期加权 CPU 使用率；单核为 100%，多核可超过 100%，不与整机百分比直接相减 |
| `memory_total_bytes` | `sysctl hw.memsize`，字节 |
| `memory_available_bytes` | `vm_stat` 的 free + inactive + speculative 页数乘页大小，属于可回收内存近似值，不是严格的模型可加载预算 |
| `memory_pressure` | 系统内存压力：normal / warning / critical；不根据内存占用比例推算 |
| `swap_used_bytes` | 系统 Swap 已用字节，命令显示值按二进制单位换算，有源显示精度限制 |
| `gpu_percent` | `IOAccelerator` 中已知的 Device Utilization % / GPU Activity(%)；多个匹配值取最大值。没有该属性则 unsupported |
| `thermal_state` | Foundation `NSProcessInfo.thermalState`：nominal / fair / serious / critical；通过系统内置 JXA 只读访问 |
| `cpu_temperature_c` / `gpu_temperature_c` | 当前没有实现可靠的摄氏温度来源，固定 null / unsupported；不安装特权传感器助手，不把热压力当摄氏度 |
| `disk_available_bytes` | Agent 数据目录所在文件系统对当前用户可用的字节数 |
| `provider_running` | 官方状态文件记录的 daemon 实例是否仍运行；核对进程名，存在 process_identity 时核对 PID 与内核启动时间，避免 PID 重用 |
| `current_model` | 新鲜官方状态文件的 current_model；没有当前模型可为 null / ok |
| `model_ready` | 当前模型在 warm_models 中，且未报告加载转换或对应 slot 加载失败；仅表示本地加载状态，不证明平台接单资格。源未报告必要字段时为未知 |
| `trust_received_at` | 新鲜状态文件的 trust.received_at，浮点 Unix 秒，授权心跳时间，通常约 30 秒刷新；不读取 trust.status 推断连接 |
| `session_id` | 新鲜状态文件的 trust.authorization.session_id，重连后变化的 Provider 会话 ID |
| `provider_connected` / `dispatch_tier` | 旧版占位字段，停用并保持空；不推断平台连接、接单资格或优先级 |
| `inference_active` | 新鲜本地状态文件报告的任务处理标志；确认 daemon 进程身份后读取 |
| `request_work_pending` | 新鲜本地状态文件报告的已接受或排队工作标志；缺失保持未知 |
| `is_idle` | 两个任务标志均为 false，且该设备最新收益距采集时严格超过 5 分钟时为 true；正在处理/排队为 false |
| `last_device_earning_at` | 仅精确匹配本机 Provider 的最新收益明细 created_at，不使用账户全体设备的最新记录 |
| `last_earnings_sync_at` | 本地收益数据库最后一次成功同步时间 |
| `earnings_provider_id` | 最新匹配收益所属 Provider ID；可能是本机之前已确认的注册会话 |
| `activity_error` | 空闲状态不可用的安全原因代码；缺失、过期和读取失败不当作空闲 |

每条还保存本安装 `device_id`、UTC `collected_at`、采集耗时，以及有来源时的 Provider PID、版本和状态文件写入时间。固定设备型号、芯片、CPU 数和内存首次启动读取并保存，后续启动有变化才更新；部分读取失败不覆盖之前的固定硬件信息。

每个指标在 `quality` 中都有状态：`ok`、`unsupported`、`unavailable`、`permission_denied`、`invalid`、`stale` 或 `not_running`。非 ok 的指标为空；合法的零或 false 保留。状态文件超过 90 秒，或比采集时间超前超过 5 秒时，不作为当前模型证据；进程是否存活独立核对。

## 配置

旧配置不需要手动增加字段，新版本默认开启：

```json
{
  "device_state_enabled": true,
  "provider_state_file": "~/.darkbloom/daemon-state.json"
}
```

上面是合并到既有配置的字段示例，不是完整配置。`device_state_enabled=false` 关闭后续状态采集，已保存的最新样本仍可查询。自定义 Darkbloom 数据位置时设置 `provider_state_file`；不会因查询访问凭证或执行 Darkbloom 控制命令。

## 本机任务与设备收益匹配

不再调用需要网页登录令牌的 `/v1/me/providers`，不再采集或展示接单状态和优先级。旧配置中的 `provider_account_token_file`、`provider_id` 仅为升级兼容而接受并忽略；不读取旧账户令牌文件。

任务状态来自 `~/.darkbloom/daemon-state.json` 的 `inference_active` 和 `request_work_pending`，与模型状态一样校验 PID/启动时间及快照新鲜度（90 秒）。不能从 CPU 低负载、模型是否加载或无收益推断没有任务。

本机状态文件的 `attestation_public_key` 与无需登录的官方公开接口 `/v1/providers/attestation` 的 `se_public_key` 精确匹配，取得本机 `provider_id`。实机 bloom-01（0.9.22）已核验此映射可匹配本地收益记录。认证公钥与收益 `provider_key` 是不同标识，不能直接比较。公开接口只用于身份映射，不读取或推断派单状态；请求不携带认证头。按同一 HTTPS upstream 来源派生固定路径，10 秒超时、2 MiB 响应上限、禁止重定向，429 遵守 Retry-After。

同一认证身份可能有多个注册会话；保存已精确确认的 Provider ID 与公钥 SHA-256 摘要的关联，在重连后仍可查询该设备已确认会话的收益。公钥原文、完整公开设备列表和认证信息不入库。查询限定本地绑定账户、匹配到的本机 Provider ID 和 365 天保留范围，取 created_at 最大的一条，不受看板日期、模型或分页筛选影响，不回退到账户最新收益，也不按芯片、机型或列表顺序猜测。尚未确认过的历史会话不自动归属本机。

判定以采集时刻为准：有正在处理或排队的任务时为非空闲；两个标志均为 false 且该设备最新收益距采集时超过 300 秒时为空闲，恰好 300 秒仍为非空闲。没有该设备收益、身份匹配失败、任务字段缺失/过期、收益同步超过 120 秒或时间在未来时，空闲状态为空并保存原因。进程停止显示未运行。未运行期间、休眠与采集缺口不补造状态。

热压力仍通过系统自带 JXA 调用 `NSProcessInfo.thermalState`；内存压力仍读取 `kern.memorystatus_vm_pressure_level`，都不依赖网络或令牌。热压力不是摄氏温度；CPU/GPU 摄氏温度继续为未支持。原生 Swift/Objective-C 可调用相同 Foundation API；`pmset -g therm` 只提供补充热警告/性能信息，不能替代四档热压力。

## 数据库和留存

schema 3 新增 `device_identity`、`device_state_samples`，schema 4 的路由列保留为历史兼容，不再读写或返回。schema 5 新增本地任务、空闲判定、设备最近收益证据及 `device_provider_ids` 身份关联表和按设备查询收益的索引。schema 6 增加授权心跳、会话与 [Bark 告警](alert.md) 状态。schema 7 升级时删除更早的状态历史，此后每个 `device_id` 仅保留最新一行。升级前自动备份；旧样本的新字段保持未知。本安装 `device_id` 独立于官方 Provider ID。

指标用独立的 SQLite 列保存，时间为 UTC 整数微秒；质量状态为白名单 JSON，官方错误/原因仅使用安全代码。表不参与收益 365 天 weekly 清理；数据库升级前沿用一致性备份。不会记录完整状态文件、模型请求/输出、官方凭证、认证公钥或原始错误消息。

## 只读接口

与既有 `/v1` 接口使用相同的 Bloomer 独立 Bearer 认证；允许任意网页来源（合法预检免令牌，实际查询必须认证）。请求只读取数据库，不触发采集。看板首页与「我的设备」使用相同的本机任务和该设备收益判定口径。

- `GET /v1/device/state`：返回 `device`、最新 `sample`、`meta.sample_interval_seconds` 与 `meta.data_stale`。未采集时 sample 为 null；超过 120 秒或时间在未来时为 stale。即使收益尚无首次成功快照也可查询。不接受查询参数。

### 收益分析（本机口径）

`GET /v1/earnings/analysis` 供看板「收益分析」页一次查询返回趋势、模型占比、时段与星期×小时等聚合。只统计 `device_provider_ids` 中已确认属于本机的 Provider ID 对应的收益行，不包含账户内其他设备的记录；尚未写入任何已确认 Provider ID 时返回 **409**、`device_identity_unavailable`，不回退为账户全体收益。

| 参数 | 说明 |
| --- | --- |
| `from` / `to` | RFC3339 UTC 半开区间，**必填**；跨度 ≤ 366 天，并受 365 天保留期截取（`meta.range_truncated`） |
| `timezone` | IANA 时区，**必填** |
| `granularity` | `hour` / `day` / `month`，默认 `day`；`hour` 时跨度 ≤ 31 天 |
| `model` | 可重复 0～8 次、不可重复；省略表示全部模型 |

响应含 `timezone`、`granularity`、有效 `from`/`to`（UTC RFC3339Nano）、`totals`、`models`、`series`、`hour_of_day`、`weekday_hour` 与与其他查询接口相同的 `meta`。`models` 列出范围内本机全部模型（**不受** `model` 筛选影响），按金额降序，含 `active_buckets`（该模型在所选粒度下有收益的分桶数）。`totals`、`series`、`hour_of_day`、`weekday_hour` 受 `model` 筛选影响；`weekday_hour` 为所选模型合计，`weekday` 1=周一 … 7=周日（按请求时区）。`series` 仅含有数据的分桶，空数组为 `[]`。金额均为微美元整数字符串；分组过多返回 422，其余错误映射与 `/v1/earnings/stats` 一致。

## 已核验来源与验证

官方 [DaemonStateFile.swift](https://github.com/Layr-Labs/d-inference/blob/master/provider-swift/Sources/ProviderCore/Service/DaemonStateFile.swift)定义 snake_case 状态文件字段与 PID 身份；本机已只读核验 schema 1 字段结构。实际设备版本缺少字段时保留未知。

测试覆盖：本地采集与部分失败、未知/零/false、任务字段缺失/过期和进程停止、严格 5 分钟边界、收益同步过期、未来时间、公开身份精确匹配与冲突、跨设备/账户收益隔离、已确认会话重连、身份变化、schema 4→7 升级备份与旧样本未知、最新样本覆盖与 schema 7 历史裁剪、设备状态独立调度。
