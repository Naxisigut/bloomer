# 全网模型收益估算与自动切换

Agent 定期拉取公开统计与官方价格，按对照组差值估算各模型「同配置机器每台每小时」的接单量、输出 token 与估算 $/h，并在看板展示推荐。开启自动切换后，由 Agent 进程内按规则提交 `darkbloom switch`，与看板手动操作共用控制通道。

## 估算口径

- 统计来自 `GET /v1/stats`，主机名取自配置 `upstream_url` 的主机部分；价格来自 `GET /v1/pricing` 的 `output_price`（微美元 / 百万 token）。
- 只按**输出 token × 官方输出单价**估算，界面标「估算」；无官方价的模型使用兜底价（先取 `fallback_model` 的官方价，否则 220000），标注 `price_source: fallback`。
- 金额在接口中以微美元整数字符串返回（`usd_per_hour_micro`）。

## 对照组

- 筛选 `chip_family ∈ chip_families` 且 `memory_gb == memory_gb`；`current_model` 为空的 provider 不计入。
- 相邻快照间隔需在 `[0.5, 2] × interval_minutes` 内才产生有效差值；`snapshot_at` 未变化时跳过本轮写入。
- 窗口为最近 `window_samples`（默认 6）个有效间隔；平均在线台数低于 `min_providers` 的模型标 `insufficient`，不参与推荐排名。

## 自动切换规则

采集与切换独立调度：采集仍按 `interval_minutes`（默认 5 分钟）拉取统计；切换不绑定采集轮次。冷却到期后立刻用最近 `window_samples`（默认 6）个有效间隔的当前推荐评估并提交。采集进行中或本轮统计失败，只要已有足够样本，仍可切换。

以下条件**全部**满足才会提交切换：

1. 看板开关 `auto_switch` 为开（持久化在数据目录 `network-rates.json`）。
2. 开启时间距今满 `switch_min_interval_minutes`（默认 30 分钟），且已有 6 个有效间隔。
3. 距上次切换满 30 分钟：取控制记录中最近 `switch` / `start` 的 `created_at` 与自动切换检查点中的较晚者。
4. 无执行中的控制任务；本机最新设备状态（10 分钟内）能读到 `current_model`。
5. 目标选择：全部已下载候选接近 0 或无可用候选时切到 `fallback_model`（需已下载）；否则切到推荐模型，且目标估算 ≥ 当前估算 × (1 + `switch_advantage_ratio`)。
6. 不在失败退避期内（30 分钟起指数退避，上限 6 小时，写入 `network-rates.json`）。

提交前写入 SQLite 切换记录（`running`）并更新检查点；按 job id 回填 `succeeded` / `failed` / `unconfirmed`。提交后立刻把 `next_eligible_at` 更新为本次切换时间 + 30 分钟。

## 配置

合并进 Agent 配置 `network_rates`；`enabled: false` 时不采集，接口返回 `enabled: false`。`auto_switch` 仅为首次创建 `network-rates.json` 的默认值，运行中由看板开关覆盖。

示例见 `apps/agent/packaging/macos/config.example.json`。

## 接口

Bearer 认证，无查询参数：

- `GET /v1/device/network-rates`：推荐、候选、样本数、切换门控（`next_eligible_at`、`blocked_reason`、`backoff_until`）、`last_error`（`stats_unavailable` / `pricing_unavailable` / `models_unavailable`）。
- `POST /v1/device/network-rates/settings`：`{"auto_switch": true|false}`，返回与 GET 相同结构。
- `GET /v1/device/network-rates/switches`：`{ "switches": [...] }`，最新在前，最多 50 条。

契约细节见 `contracts/openapi.yaml`。

## 存储与保留

SQLite schema **8**（migration 008）：对照组基线、按模型间隔汇总（保留 7 天）、切换记录（最近 200 条，摘要 JSON ≤ 4 KiB）。开关与退避状态在 `network-rates.json`（0600，原子写）。

## 网络说明

统计与价格请求使用独立 `http.Client`，传输层克隆系统默认（含 `ProxyFromEnvironment`），**不绑定物理网卡**，走系统路由（如 FlClash TUN）。不保存原始 stats JSON 或 CLI 输出。
