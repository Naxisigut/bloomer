# 全网模型收益估算与自动切换

Agent 定期拉取公开统计与官方价格，按对照组差值估算各模型「同配置机器每台每小时」的接单量、输出 token 与估算 $/h，并在看板展示推荐。开启自动切换后，由 Agent 进程内按规则提交 `darkbloom switch`，与看板手动操作共用控制通道。

看板「全网数据」里的切换/下载只负责提交控制任务，不在本页轮询或提示结果：执行状态与记录统一在「我的设备 → 远程操作」查看，模型清单与当前模型在下一次刷新时更新。

## 估算口径

- 统计来自 `GET /v1/stats`，主机名取自配置 `upstream_url` 的主机部分；价格来自 `GET /v1/pricing` 的 `input_price` / `output_price`（微美元 / 百万 token）。
- 收益按官方价格表计算，平台不抽成、缓存不影响收益：`$/h = 每台每小时输出 token × 综合单价`，`综合单价 = output_price + 输入比 × input_price`（`effective_price_micro`，每百万输出 token）。
- 公开统计里单台机器只有输出 token，输入 token 用「输入比」（提示词 token / 输出 token）推算，`ratio_source` 标明来源：
  - `own`：本账号近 7 天该模型的收益记录（只计 `completion_tokens > 0` 的记录，至少 30 条）；
  - `network`：全网近 24 小时 `last_24h_prompt_tokens / last_24h_completion_tokens`；
  - `none`：两者都没有时只算输出价。
- 无官方价的模型使用兜底价（先取 `fallback_model` 的官方价，否则输入 42000 / 输出 220000），标注 `price_source: fallback`。
- 金额在接口中以微美元整数字符串返回（`usd_per_hour_micro`）。

## 对照组

- 筛选 `chip_family ∈ chip_families` 且 `memory_gb == memory_gb`；`current_model` 为空的 provider 不计入。
- 配置了 `chip_tiers`（默认 `["Pro"]`）时，另按 `chip_tier` 汇总一份同档数据。推荐和自动切换仍按模型选择：同档平均台数 ≥ `min_providers` 时用同档（`cohort: tier`），否则退回全组（`cohort: all`）。`chip_tiers` 设为 `[]` 时始终用全组。看板展示与此独立：刷新按钮左侧可在「全组 / 同档」间切换，所有模型使用同一口径，不再按模型混用。
- 相邻快照间隔需在 `[0.5, 2] × interval_minutes` 内才产生有效差值；`snapshot_at` 未变化时跳过本轮写入。
- 估算窗口为最近 `window_samples`（默认 5，约 10 分钟）个有效间隔，用于推荐、自动切换和看板候选表。接口仍计算 `hour`（最近 60 分钟），看板不再展示。
- 平均在线台数低于 `min_providers` 的模型标 `insufficient`，不参与推荐排名。
- 每个间隔另记有单台数和单台最大输出：`busy_ratio` 为有输出的机器占比；`trimmed_usd_per_hour_micro` 为每轮去掉输出最多的一台后再平均的估算，用来判断收益是否集中在少数机器。升级前的旧间隔没有这两项，对应字段为 null。

## 自动切换规则

采集与切换独立调度：采集仍按 `interval_minutes`（默认 2 分钟）拉取统计；切换不绑定采集轮次。冷却到期后立刻用最近 `window_samples`（默认 5）个有效间隔的当前推荐评估并提交。采集进行中或本轮统计失败，只要已有足够样本，仍可切换。

以下条件**全部**满足才会提交切换：

1. Agent 开关 `auto_switch` 为开（持久化在数据目录 `network-rates.json`）。看板不再提供此开关。
2. 开启时间距今满 `switch_min_interval_minutes`（默认 30 分钟），且已有 5 个有效间隔。
3. 距上次切换满 30 分钟：取控制记录中最近 `switch` / `start` 的 `created_at` 与自动切换检查点中的较晚者。
4. 无执行中的控制任务；本机最新设备状态（10 分钟内）能读到 `current_model`。
5. 目标选择：全部已下载候选接近 0 或无可用候选时切到 `fallback_model`（需已下载）；否则切到推荐模型，且目标估算 ≥ 当前估算 × (1 + `switch_advantage_ratio`)。
6. 不在失败退避期内（30 分钟起指数退避，上限 6 小时，写入 `network-rates.json`）。

提交前写入 SQLite 切换记录（`running`）并更新检查点；按 job id 回填 `succeeded` / `failed` / `unconfirmed`。提交后立刻把 `next_eligible_at` 更新为本次切换时间 + 30 分钟。

## 配置

合并进 Agent 配置 `network_rates`；`enabled: false` 时不采集，接口返回 `enabled: false`。`auto_switch` 仅为首次创建 `network-rates.json` 的默认值，运行中由该文件或 Agent 的 `POST /v1/device/network-rates/settings` 覆盖；看板不再提交此设置。

示例见 `apps/agent/packaging/macos/config.example.json`。

## 接口

Bearer 认证，无查询参数：

- `GET /v1/device/network-rates`：推荐、候选、样本数、`interval_minutes`、切换门控（`next_eligible_at`、`blocked_reason`、`backoff_until`）、`last_error`（`stats_unavailable` / `pricing_unavailable` / `models_unavailable`）。另含 `long_window_minutes`（60）、`switch_advantage_ratio`、`chip_tiers`、`memory_gb`、`min_providers`，以及候选的 `tier_avg_providers` / `all_avg_providers` 和短窗口 `all` / `tier` 两套数字。看板候选表用 `window_samples` × `interval_minutes` 在表下标注窗口（默认近 10 分钟），四列分开展示有单率、每台输出 token/h、估算 $/h、去头 $/h，并用同档数/全组数对照「相对当前模型」的涨跌。顶部可切换全组或同档，表与趋势共用这一选择。看板不展示近 1 小时列、切换门控与阻止原因。采集未运行时 `enabled` 为 false，`interval_minutes` 为 0。
- `POST /v1/device/network-rates/settings`：`{"auto_switch": true|false}`，返回与 GET 相同结构。
- `GET /v1/device/network-rates/switches`：`{ "switches": [...] }`，最新在前，最多 50 条。看板不请求此接口。
- `GET /v1/device/network-rates/token-trend`：`{ "series", "all", "tier": [{ "model", "points": [{ "at", "tokens" }] }] }`，最近 24 小时。每个模型一条序列，点从旧到新。`tokens` 与候选表「每台每小时输出 token」相同：`round(delta_tokens / (providers * hours))`，不是各模型合计。`series` 仍按每个模型当前自动选择的对照组（兼容旧看板）；`all` / `tier` 分别为全组、同档，看板切换时所有模型走同一套。有候选时只返回这些模型且顺序一致；候选为空时返回间隔中出现过的全部模型，按名称排序。跳过 `providers<=0`、`hours<=0` 或 `delta_tokens<0` 的间隔，不补 0。没有样本时各数组为空。

契约细节见 `contracts/openapi.yaml`。

## 存储与保留

SQLite schema **9**（migration 008 建表，009 为间隔汇总增加 `scope` / `busy_providers` / `max_provider_tokens`）：对照组基线、按模型与对照组范围的间隔汇总（保留 7 天）、切换记录（最近 200 条，摘要 JSON ≤ 4 KiB）。开关与退避状态在 `network-rates.json`（0600，原子写）。

## 网络说明

统计与价格请求使用独立 `http.Client`，传输层克隆系统默认（含 `ProxyFromEnvironment`），**不绑定物理网卡**，走系统路由（如 FlClash TUN）。不保存原始 stats JSON 或 CLI 输出。
