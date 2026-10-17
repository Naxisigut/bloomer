# Bloomer

运行在 Darkbloom 设备上的收益采集 Agent，与独立看板共用接口契约。当前已完成 **P0～P3**：接口契约、工程与数据库、收益采集、恢复与留存。

程序启动后立即采集，之后每 60 秒从 50 条开始，按 50 → 100 → 300 → 1000 扩窗。首次导入、达到 30 分钟中断或时钟异常时读取 1000 条。收益查询接口在 P4 实现，目前只提供需要独立 Bearer 令牌的健康接口。

## 开发与构建

环境：Go 1.27，macOS / Apple Silicon；SQLite 使用纯 Go 驱动，不依赖本机 SQLite 或 CGo。

```sh
cd apps/agent
make check
make race
make build
./bin/bloomer-agent -version
```

依赖版本与校验和保存在 `go.mod` / `go.sum`。`make build` 编译 Agent 和 Go 管理工具，并生成 `apps/agent/dist/bloomer-agent-1.0.0-darwin-arm64.dmg` 及 `.dmg.sha256` 校验文件，不进入 Git。仅编译可运行 `make binary`，可执行文件位于 `apps/agent/bin/`。构建、打包和安装管理均不依赖 Python。

打开 DMG，双击 **安装 Bloomer.app**，即可安装并启动当前用户的后台服务。安装条件、命令行启停、升级和恢复见 [安装说明](docs/install.md)。

## 本地启动

可以直接使用用户目录默认配置：

```sh
cd apps/agent
./bin/bloomer-agent
```

或指定公开示例（首次使用请检查路径，私有副本放在仓库外）：

```sh
./bin/bloomer-agent -config packaging/macos/config.example.json
```

启动时创建运行目录、SQLite 数据库和随机的独立访问令牌，默认位置：

- 数据：`~/Library/Application Support/Bloomer/agent/`
- 令牌：数据目录下 `access.token`（0600）
- 日志：`~/Library/Logs/Bloomer/agent/`
- 监听：`127.0.0.1:17890`

默认启用采集，每次请求重新读取官方凭证文件，支持凭证轮转。官方凭证缺失或网络失败时服务继续运行，健康接口报告降级与数据过期；`collection_enabled: false` 可显式关闭采集，清理仍按计划执行。首次成功采集后数据库绑定该账户，拒绝将其他账户数据写入其中；也可提前配置 `account_id` 固定预期账户。配置的相对路径以配置文件目录为基准，`~/` 通过用户目录 API 展开。不存在的 `-config`、无效路径、非回环监听、端口占用、重复启动和不支持的数据库版本均明确报错。访问令牌只在首次缺失时生成，重启沿用。

健康检查示例（在运行 Agent 的设备上执行，管理工具内部读取私有配置与令牌，不输出令牌）：

```sh
"$HOME/Library/Application Support/Bloomer/agent/management/bloomerctl" health
```

终端运行时使用 Ctrl+C 或 SIGTERM 正常停止。退出时取消在途任务，等待事务提交或回滚后保存正常关闭时间；强制退出依靠最近成功检查点恢复。LaunchAgent 安装与 Tailscale Serve 发布在 P5 交付。

## 恢复与保留

采集失败不推进成功时间，扩窗中途失败不保存部分明细。429 等待截止时间持久化，重启不能绕过。普通失败在下一周期重试，不堆积漏过的任务。

收益明细按 UTC 发生时间保留滚动 365 天；本地读取与合计始终过滤过期记录。每满 7 天补做一次物理清理，每批最多 1000 条，可从失败批次继续。官方累计与余额不受明细留存截断。达到窗口上限或恢复无法衔接旧历史时记录疑似缺口，后续成功不自动抹去尚在保留期内的缺口。

数据库已升级为 schema 2。已有 schema 1 数据库升级前自动建立一致备份；旧版本程序不能直接打开升级后的数据库，恢复方法见数据库文档。

## 验证

在仓库根目录运行离线进程测试；所有测试状态均在临时目录，结束后清理：

```sh
./scripts/check-agent.sh
make -C apps/agent binary
python3 scripts/smoke-agent.py
```

进程测试使用不存在的官方凭证验证降级查询、启动清理、认证、单实例、SIGTERM、SIGKILL 重启、持久化限流等待与私有路径。Go 测试包含本机 HTTPS 模拟服务，需要允许绑定回环端口；所有令牌和收益均为合成数据。

## 文档

- [开发目标](docs/agent-development-goals.md)
- [实施计划](docs/agent-development-plan.md)
- [P2/P3 实现与验收记录](docs/p2-p3-validation.md)
- [P0/P1 验证记录](docs/p0-p1-validation.md)
- [官方接口复验与样本说明](docs/upstream-contract.md)
- [数据库迁移与恢复](docs/database.md)
- [DMG 安装与服务管理](docs/install.md)
- [共享 OpenAPI 草案](contracts/openapi.yaml)
- [仓库结构](docs/repository-structure.md)

真实凭证、私有配置、数据库、WAL、备份、日志和构建产物不进入 Git。Agent 的运行目录应位于仓库之外。
