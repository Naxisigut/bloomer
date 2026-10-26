# 全网模型收益估算与自动切换实现计划

> 规格：`docs/superpowers/specs/2026-10-22-network-rates-auto-switch-design.md`（以规格为准）。代码由子代理编写，本计划只锁定任务边界与接口。

**Goal：** Agent 每 2 分钟采集对照组差值，估算各已下载模型每台每小时收益并推荐，可选自动切换；看板展示推荐、开关与切换记录。

**Architecture：** 新包 `apps/agent/internal/netrates` 负责拉取、计算与切换决策；SQLite migration 008 存基线、间隔汇总和切换记录；`control.Manager` 作为唯一切换通道；新 HTTP 接口位于 `/v1/device/network-rates`；看板为原生 ES 模块。

**Tech Stack：** Go 1.27（纯 Go SQLite）、原生 HTML/CSS/JS、`node --test`、pnpm。

## Global Constraints

- 不绑定网卡；统计请求使用克隆的 `http.DefaultTransport`（保留 `ProxyFromEnvironment`）。
- 不保存原始 stats JSON；只保存对照组基线（约 170 行）与按模型汇总的差值。
- 金额使用整数微美元；价格单位为微美元 / 百万 token（`/v1/pricing` 的 `output_price`）。
- 错误只记录固定代码，不记录 CLI 输出、URL 凭证或私有路径。
- 子代理不执行 `git commit`，不格式化与自身改动无关的代码；看板使用 pnpm。

---

### Task 1：设备端数据层（config + migration + store + 统计/价格客户端 + 计算）

**Files：**
- Create：`apps/agent/internal/config/netrates.go`、`apps/agent/migrations/008_network_rates.sql`、`apps/agent/internal/netrates/{types.go,client.go,compute.go}` 及对应 `_test.go`、`apps/agent/internal/store/netrates.go` 及测试
- Modify：`apps/agent/internal/config/config.go`（`Config.NetworkRates`、默认值、`Validate`）、`config_test.go`

**Produces（后续任务依赖，名称必须一致）：**

```go
// package config
type NetworkRates struct {
	Enabled                 bool     `json:"enabled"`
	IntervalMinutes         int      `json:"interval_minutes"`
	WindowSamples           int      `json:"window_samples"`
	AutoSwitch              bool     `json:"auto_switch"`
	SwitchMinIntervalMinutes int     `json:"switch_min_interval_minutes"`
	SwitchAdvantageRatio    float64  `json:"switch_advantage_ratio"`
	FallbackModel           string   `json:"fallback_model"`
	ChipFamilies            []string `json:"chip_families"`
	MemoryGB                int      `json:"memory_gb"`
	MinProviders            int      `json:"min_providers"`
	NearZeroMicroUSDPerHour int64    `json:"near_zero_micro_usd_per_hour"`
}
func DefaultNetworkRates() NetworkRates
func (n NetworkRates) Validate() error

// package netrates
type Cohort struct { ChipFamilies []string; MemoryGB int }
type ProviderSnap struct { ID, Model string; Requests, Tokens int64 }
type Snapshot struct { At time.Time; Providers []ProviderSnap } // 已过滤为对照组
type Interval struct { StartedAt, EndedAt time.Time; Model string; Providers int; DeltaRequests, DeltaTokens int64 }
type Candidate struct {
	Model string `json:"model"`; Downloaded bool `json:"downloaded"`
	AvgProviders float64 `json:"avg_providers"`; RequestsPerHour float64 `json:"requests_per_hour"`
	TokensPerHour float64 `json:"tokens_per_hour"`; USDPerHourMicro int64 `json:"-"` // HTTP 层以字符串输出
	PriceSource string `json:"price_source"`; Insufficient bool `json:"insufficient"`
}
type Recommendation struct { Model string; USDPerHourMicro int64; Fallback bool }
type SummaryItem struct { Model string `json:"model"`; USDPerHourMicro int64 `json:"usd_per_hour_micro"`; AvgProviders float64 `json:"avg_providers"` }
type SwitchRecord struct { ID int64; At time.Time; From, To, Reason, Status, Error, JobID string; Summary []SummaryItem }

func NewClient(baseURL string, hc *http.Client) (*Client, error) // baseURL 形如 https://api.darkbloom.dev；hc 为 nil 时使用克隆的默认传输与 60s 超时
func (c *Client) Stats(ctx context.Context, cohort Cohort) (Snapshot, error) // 流式解码，8 MiB 上限
func (c *Client) Pricing(ctx context.Context) (map[string]int64, error)     // model -> output_price
func Diff(prev, next Snapshot, interval time.Duration) (intervals []Interval, valid bool)
func Estimate(intervals []Interval, window int, prices map[string]int64, fallbackModel string, downloaded map[string]bool, minProviders int) (cands []Candidate, samples int)
func Recommend(cands []Candidate, samples, window int, nearZero int64, fallbackModel string) *Recommendation

// package store（*Store 方法）
func (s *Store) LoadNetRateBaseline(ctx context.Context) (netrates.Snapshot, bool, error)
func (s *Store) SaveNetRateRound(ctx context.Context, snap netrates.Snapshot, intervals []netrates.Interval, retainAfter time.Time) error
func (s *Store) NetRateIntervals(ctx context.Context, since time.Time) ([]netrates.Interval, error)
func (s *Store) InsertNetRateSwitch(ctx context.Context, r netrates.SwitchRecord) (int64, error) // 超过 200 条时删除最旧的
func (s *Store) UpdateNetRateSwitch(ctx context.Context, id int64, status, errCode string) error
func (s *Store) NetRateSwitches(ctx context.Context, limit int) ([]netrates.SwitchRecord, error) // 最新在前
```

验证：`cd apps/agent && make check`，以及 `./scripts/check-agent.sh`。

### Task 2：设备端运行器、切换规则、接口与文档

**Files：**
- Create：`apps/agent/internal/netrates/{settings.go,runner.go}` 及测试、`apps/agent/internal/httpapi/netrates.go` 及测试、`docs/network-rates.md`
- Modify：`apps/agent/internal/control/control.go`（只读查询方法）、`apps/agent/internal/httpapi/handler.go`（路由）、`apps/agent/internal/service/service.go`（启动循环）、`contracts/openapi.yaml`、`apps/agent/packaging/macos/config.example.json`、`docs/database.md`、`README.md`

**Consumes：** Task 1 的全部接口。
**Produces：** HTTP 接口按规格中的「接口」一节定义；看板（Task 3）依赖其 JSON 结构。

### Task 3：看板

**Files：**
- Create：`apps/dashboard/public/network-rates.js`（数据获取与渲染）、`apps/dashboard/test/network-rates.test.mjs`
- Modify：`index.html`（顶部「推荐模型（估算）」、设备页面板）、`app.js`、`agent-client.js`、`demo.js`、`styles.css`、`dev/mock-agent.mjs`、`package.json` 的 `check` 脚本、`apps/dashboard/README.md`

**Consumes：** 规格中的接口 JSON。可与 Task 1 并行。

验证：`pnpm --dir apps/dashboard test`、`pnpm --dir apps/dashboard check`。
