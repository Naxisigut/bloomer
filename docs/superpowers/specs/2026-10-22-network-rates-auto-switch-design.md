# 全网模型收益估算与自动切换设计

日期：2026-10-22

## 目标

Agent 每 5 分钟拉取公开 `GET https://api.darkbloom.dev/v1/stats`，只把「对照组相邻快照差值」按模型汇总入库；用最近 6 个有效间隔估算各已下载模型「同配置机器每台每小时」的接单量、输出 token 与估算 $/h，给出推荐。开关打开时由 Agent 进程内按规则自动 `darkbloom switch`，不依赖看板。关闭时仍采集、展示推荐。

## 已确认的决策

| 项目 | 决定 |
| --- | --- |
| 开关持久化 | 配置里的 `auto_switch` 只是初始默认值；看板开关写入数据目录私有文件 `network-rates.json`（0600，原子写），立即生效，重启沿用 |
| 加载模型集合 | 只加载单个目标模型；兜底时只加载 Gemma。「当前模型」= 本机最新设备状态的 `current_model` |
| 收益口径 | 只按输出 token × 官方输出单价估算，所有模型口径一致，界面标「估算」 |
| 接近 0 | 所有候选估算都 < $0.01 / 台·小时（`near_zero_micro_usd_per_hour`，默认 10000） |
| 保留 | 差值汇总保留 7 天；切换记录保留最近 200 条 |

## 数据源

- **统计**：`/v1/stats`，主机取自 `upstream_url` 的主机名。使用独立 `http.Client`，`Transport` 克隆默认传输（含 `ProxyFromEnvironment`），**不绑定网卡**，走系统路由（FlClash TUN）。超时 60 秒，响应上限 8 MiB，流式解码，只读取 `snapshot_at` 和 `providers[]` 的 `id / chip_family / memory_gb / current_model / requests_served / tokens_generated`。不保存原始 JSON。
- **价格**：`GET /v1/pricing`（公开），`output_price` 单位为微美元 / 百万 token。每轮与统计一并拉取；失败时沿用内存中最近一次成功价格。没有官方价格的模型使用兜底价：先取 `fallback_model` 的官方价，否则用 220000，标注 `price_source: "fallback"`。
- **已下载模型**：复用 `control.Manager.Models()` 的本地列表（`darkbloom models list --json --all`）。失败时本轮不切换，原因记为 `models_unavailable`。

## 对照组与计算

1. 筛选：`chip_family ∈ chip_families` 且 `memory_gb == memory_gb`，Pro / Max 不区分；`current_model` 为空的不计。
2. 对照组基线（provider id → model、requests、tokens，约 170 行）连同 `snapshot_at` 存入 SQLite，以便重启后续接。
3. 相邻快照的有效间隔：两次 `snapshot_at` 相差在 `[0.5, 2] × interval_minutes` 之内，否则只重建基线，不产生间隔。`snapshot_at` 未变化（命中上游缓存）时跳过，不更新基线。
4. 同一 provider 在两次快照中 `current_model` 相同时，计算 Δrequests、Δtokens；任一为负则丢弃该 provider。
5. 按模型汇总一行：`started_at, ended_at, model, providers, delta_requests, delta_tokens`。同一间隔内没有样本的模型不写入。
6. 窗口：最近 `window_samples`（6）个有效间隔，按 `ended_at` 取。每个模型：
   - `provider_hours = Σ providers × 间隔时长（小时）`
   - `requests_per_hour = Σ Δrequests / provider_hours`，`tokens_per_hour` 同理
   - `usd_per_hour = tokens_per_hour × output_price / 1e6`（整数微美元）
   - 平均在线台数 `avg_providers = Σ providers / 间隔数`；小于 `min_providers`（默认 3）的模型标为 `insufficient`，不参与排名
7. 推荐：在**已下载**且样本充足的模型中取 `usd_per_hour` 最大者。有效间隔不足 6 个时 `recommendation = null`（看板顶部置空），候选表照常展示。若所有候选都低于接近 0 阈值，推荐为 `fallback_model`（标记 `fallback: true`）。

## 自动切换（以下条件全部满足才切）

每轮采集完成后评估一次：

1. `auto_switch` 为开。
2. 开启时间 `auto_switch_since` 距今满 `switch_min_interval_minutes`（30 分钟），且已有 6 个有效间隔。
3. 距上次切换满 30 分钟。上次切换时间取以下两者的较晚值：控制记录中最近一条 `switch` 或 `start` 任务的 `created_at`（包含看板手动操作），以及自动切换检查点。
4. 当前没有执行中的控制任务；本机状态里能读到 `current_model`。
5. 目标选择：
   - 所有已下载候选都接近 0，或没有可用候选：目标为 Gemma。当前已是 Gemma 或 Gemma 未下载则不切（后者记原因 `fallback_not_downloaded`）。
   - 否则目标为推荐模型。如果它就是当前模型则不切。当前模型的估算取其窗口值，没有样本或样本不足按 0 处理；仅当 `目标估算 ≥ 当前估算 × (1 + switch_advantage_ratio)` 才切。
6. 失败退避：不在退避期内。

执行：通过 `control.Manager.Submit({request_id: "auto-<时间戳+随机>", action: "switch", models: [目标]})` 提交，与看板手动操作互斥并出现在控制记录中。提交前先把切换记录（状态 `running`，含 job id、从→到、原因、各候选估算摘要）写入 SQLite 并更新检查点；之后每轮按 job id 回填 `succeeded / failed / unconfirmed`。重启后由检查点和控制记录判断间隔，不会重复切换。

失败（提交被拒、任务失败或未确认）时记录固定错误码（`control_busy`、`control_unavailable`、`command_failed`、`command_timeout` 等），连续失败按 30 分钟 → 60 → 120 → … 退避，上限 6 小时。退避状态写入 `network-rates.json`。成功后清零。

## 配置

合并进现有 `Config`，新增 `network_rates` 对象，并补齐校验：

```json
{
  "network_rates": {
    "enabled": true,
    "interval_minutes": 5,
    "window_samples": 6,
    "auto_switch": false,
    "switch_min_interval_minutes": 30,
    "switch_advantage_ratio": 0.2,
    "fallback_model": "gemma-4-26b-qat-4bit",
    "chip_families": ["M4", "M5", "M6"],
    "memory_gb": 64,
    "min_providers": 3,
    "near_zero_micro_usd_per_hour": 10000
  }
}
```

校验范围：interval 1–60，window 2–48，switch 间隔 1–1440，ratio 0–10，chip_families 1–8 项且每项为 1–16 位字母数字，memory_gb 1–1024，min_providers 1–1000，near_zero 0–10^9，fallback_model 必须符合控制模块的模型 ID 规则。`enabled: false` 时不采集，接口返回 `enabled: false`。

## 存储（migration 008）

- `network_rate_baseline(provider_id PK, model, requests, tokens)` 加 `network_rate_state(id=1, snapshot_at_us)`：替换式写入，单事务完成。
- `network_rate_intervals(id, started_at_us, ended_at_us, model, providers, delta_requests, delta_tokens)`，`ended_at_us` 建索引；每轮删除 7 天前的数据。
- `network_rate_switches(id, at_us, from_model, to_model, reason, status, error, job_id, summary_json)`；保留最近 200 条。`summary_json` 只存候选摘要（model、usd_per_hour、avg_providers），小于 4 KiB。

## 接口（Bearer 认证，无查询参数）

- `GET /v1/device/network-rates`：
  ```json
  {
    "enabled": true, "auto_switch": false, "auto_switch_since": null,
    "window_samples": 6, "samples": 4, "updated_at": "...", "snapshot_at": "...",
    "current_model": "Qwen3.5-9B",
    "recommendation": null,
    "candidates": [{ "model": "...", "downloaded": true, "avg_providers": 38.2,
      "requests_per_hour": 120.5, "tokens_per_hour": 380000,
      "usd_per_hour_micro": "83600", "price_source": "official", "insufficient": false }],
    "switch": { "next_eligible_at": "...", "blocked_reason": "warming_up", "backoff_until": null },
    "last_error": null
  }
  ```
  `recommendation`：`{ model, usd_per_hour_micro, fallback }` 或 `null`。`candidates` 包含窗口内出现过的所有模型，按估算降序，未下载的标 `downloaded: false` 且不参与推荐。`last_error` 只用固定代码：`stats_unavailable`、`pricing_unavailable`、`models_unavailable`。
- `POST /v1/device/network-rates/settings`：`{"auto_switch": true}`，`application/json`，拒绝未知字段。开启时把 `auto_switch_since` 设为当前时间；关闭时清空。返回与 GET 相同的结构。
- `GET /v1/device/network-rates/switches`：`{ "switches": [...] }`，最新在前，最多 50 条。

旧 Agent 返回 404 / 405 / 501 时，看板按「不支持」处理。

## 看板

- **首页顶部固定栏**：新增「推荐模型（估算）」项，显示模型名和 `$x.xx/h`；`recommendation` 为 null、接口不支持或出错时显示「—」。兜底推荐时显示「Gemma（兜底）」。
- **我的设备页**：新增「全网模型收益（估算）」面板，包含：
  - 「自动切换最优模型」开关，带确认，提交后刷新；
  - 状态行：样本 `n / 6`、下次可切换时间、阻止原因（中文映射）、退避；
  - 候选表：模型、已下载、对照组均台数、每台每小时接单数、输出 token、估算 $/h、价格来源（兜底价注明）；
  - 最近切换记录：时间（北京时间）、从 → 到、原因、状态、错误码，可展开查看候选估算摘要。
- 演示设备提供只读的演示数据；`dev/mock-agent.mjs` 增加对应接口。

## 测试

- Go：计算单元测试（筛选、模型变化剔除、负增量、间隔有效性、窗口、价格兜底、min_providers）；切换规则表驱动测试（各阻止原因、20% 阈值、兜底 Gemma、退避、手动切换重新计时、重启不重复）；存储迁移与保留；HTTP 接口（认证、方法、未知字段）；统计客户端使用 httptest，验证大小上限与流式解码。
- 看板：`node --test` 覆盖顶部推荐渲染与置空、开关提交、切换记录渲染、旧 Agent 不支持。
- 验证命令：`./scripts/check-agent.sh`、`make -C apps/agent check`、`pnpm --dir apps/dashboard test`、`pnpm --dir apps/dashboard check`。

## 文档

新增 `docs/network-rates.md`；更新 README 文档列表、`contracts/openapi.yaml`、`config.example.json`，以及 `docs/database.md`（schema 8）。
