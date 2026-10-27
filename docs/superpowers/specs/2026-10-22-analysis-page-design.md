# 收益分析页重写设计

日期：2026-10-22

## 目标

推倒重写看板「收益分析」页（`#analysis`）。用户选定设备、时间段和模型后点击「查询」，页面对该期间的数据做多图表分析。页面数据完全由筛选决定，不自动刷新，不受首页每分钟刷新影响。

收益明细页（`#records`）保持现有功能，但与分析页彻底解耦，不再共享筛选面板与状态。

## 数据口径

- **只统计所选设备本机产生的收益**：按 Agent 本地 `device_provider_ids` 中属于本机的 Provider ID 过滤收益明细，而不是该 Agent 保存的整个账户收益。
- 本机 Provider ID 尚未识别时，接口返回明确错误 `device_identity_unavailable`，看板提示「设备身份尚未识别，暂时无法统计本机收益」，不回退为账户数据。
- 时区固定 `Asia/Shanghai`，日期范围按中国标准时间日历转换为 UTC 半开区间；金额全程使用整数微美元字符串，仅在绘图时转为数字。
- 查询范围受 365 天明细保留期约束，超出部分截取并在页面提示。

## 设备端新接口

`GET /v1/earnings/analysis`（Bearer 认证，只读快照，沿用 10 秒查询超时）

参数：

| 参数 | 说明 |
| --- | --- |
| `from` / `to` | RFC3339，UTC 半开区间，必填，跨度 ≤ 366 天 |
| `timezone` | IANA 时区，必填 |
| `granularity` | `hour` / `day` / `month`；`hour` 仅允许跨度 ≤ 31 天 |
| `model` | 可重复 0～12 次；为空表示全部模型。上限等于看板调色板颜色数（`public/palette.js` 的 `MAX_MODELS`），保证每个选中模型都有自己的颜色；后端只按模型名多几个分组，不构成性能约束 |

响应：

```json
{
  "timezone": "Asia/Shanghai", "granularity": "day",
  "from": "...", "to": "...",
  "totals": { "amount_micro_usd": "123", "record_count": 4 },
  "models": [{ "model": "m", "amount_micro_usd": "123", "record_count": 4, "active_buckets": 2 }],
  "series": [{ "from": "...", "to": "...", "items": [{ "model": "m", "amount_micro_usd": "1", "record_count": 1 }] }],
  "hour_of_day": [{ "hour": 0, "model": "m", "amount_micro_usd": "1", "record_count": 1 }],
  "weekday_hour": [{ "weekday": 1, "hour": 0, "amount_micro_usd": "1", "record_count": 1 }],
  "meta": { "...": "与现有查询接口相同的 meta" }
}
```

- `models`：期间内本机全部模型的汇总（**不受 `model` 筛选影响**），按金额降序；用于模型选择列表与占比图。
- `totals`、`series`、`hour_of_day`、`weekday_hour`：受 `model` 筛选影响。
- `series` 只返回有数据的分桶，按时间升序；看板负责补齐空桶为 0（仅限已过去的时间）。
- `weekday_hour` 为所选模型合计；`weekday` 1=周一 … 7=周日，按请求时区计算。
- 一次请求、一个读快照返回全部聚合，避免多次往返与数据不一致。
- 错误码：参数错误 `invalid_request`，身份未识别 `device_identity_unavailable`，其余沿用现有映射。

## 看板页面

### 筛选栏（表单，点「查询」才执行）

- 设备：单选，含演示设备。
- 时间：快捷项「今天 / 昨天 / 近 7 天 / 近 30 天 / 近 90 天 / 近一年」及自选起止日期。
- 粒度：按小时 / 按天 / 按月；按范围给出默认值（≤ 2 天按小时，≤ 90 天按天，否则按月），不合法选项禁用。
- 模型：多选标签，「全部模型」与具体模型互斥；列表来自上一次查询结果的 `models`。已选但新范围内无数据的模型保留并标注「无数据」。
- 进入页面时若从未查询过，用默认条件（当前设备、近 7 天、全部模型）自动执行一次；之后只在点击「查询」时请求。切换菜单再回来保留上次条件与结果，不重查。
- 查询中可取消；新查询中止旧查询；失败保留上次结果并显示错误。

### 图表区

1. **指标卡**：总收益、任务数、单笔平均收益、日均收益、收益最高时段。
2. **收益趋势**：按粒度柱状图；多模型时按模型堆叠，可切换为折线；支持缩放。
3. **累计收益**：期间累计曲线；多模型时每个模型一条，并有合计线。
4. **模型收益占比**：环形图 + 排行表（金额、占比、任务数、单笔均值）。
5. **任务数与单笔均值**：任务数柱状 + 单笔均值折线（双轴）。
6. **24 小时时段分布**：按北京时间小时的收益；多模型时分系列，展示「该小时日均收益」。
7. **星期 × 小时热力图**：所选模型合计。
8. **模型分析**：
   - 模型 × 小时热力图：各模型在一天中哪个时段收益高；
   - 模型平均收益对比：单笔均值、日均收益、每活跃小时均值的分组柱状图。

图表无数据时显示空状态，不画假零；窗口缩放时自适应。

## 实现结构

- 设备端：`apps/agent/internal/store` 新增分析聚合（按本机 Provider ID 过滤，复用现有按设备查询收益的索引）；`apps/agent/internal/httpapi` 新增路由；更新 `contracts/openapi.yaml` 与文档。
- 看板：
  - `public/vendor/echarts.esm.min.mjs`：以 pnpm 安装 `echarts` 为开发依赖，`pnpm run vendor` 复制到 `public/vendor/` 并纳入仓库（静态托管无需构建）。
  - `public/analysis-data.js`：纯函数，负责参数构建、补齐空桶、累计、均值、最高时段等派生计算。
  - `public/analysis-charts.js`：纯函数，把派生数据转换为 ECharts option。
  - `public/analysis-page.js`：页面状态、筛选表单、查询与取消、图表实例生命周期。
  - `agent-client.js`：新增 `analysis` 路由、参数白名单（允许重复 `model`）与响应校验；旧 Agent 返回 404/405/501 时提示「该设备 Agent 尚不支持收益分析，请升级 Agent」。
  - `demo.js`：演示设备生成分析数据。
  - `app.js` / `index.html` / `styles.css`：移除旧分析页与共享筛选，`autoRefresh` 不再处理分析页；收益明细页独立保留原筛选。
- 文档：更新 `docs/dashboard.md` 与 `apps/dashboard/README.md` 中分析页说明。

## 测试

- Go：参数校验（跨度、粒度限制、模型数量）、本机 Provider 过滤、分桶与时区、星期计算、身份未识别错误、`models` 不受筛选影响。
- 看板（`pnpm test`）：派生计算纯函数、ECharts option 构建、客户端参数与响应校验、演示数据形状；`pnpm run check` 覆盖新文件。
