# Bloomer

运行在 Darkbloom 设备上的收益采集 Agent，与独立看板共用接口契约。设备端已提供采集、恢复、留存、认证查询、Darkbloom 远程操作和 macOS 安装管理；看板第一版位于 `apps/dashboard`。

程序启动后立即采集，之后每 60 秒从 50 条开始，按 50 → 100 → 300 → 1000 扩窗。首次导入、达到 30 分钟中断或时钟异常时读取 1000 条。健康、账户概览、收益明细和统计接口均使用独立 Bearer 令牌。

## 收益看板

看板为原生 HTML / CSS / JavaScript 静态网页。普通静态服务器即可运行，例如：

```sh
python3 -m http.server 18080 --bind 127.0.0.1 --directory apps/dashboard/public
```

固定打开 <http://127.0.0.1:18080>。浏览器保存设备名称、地址及 Bloomer `access.token`，通过 Tailscale HTTPS 直接请求 Agent `/v1`；不需要 Node 看板服务、`npm start` 或构建。无设备时显示明确标注的只读演示数据，连接失败不会自动换成演示数据。

家中 Agent 需升级并在原配置合并 `"allowed_origins": ["http://127.0.0.1:18080"]` 后重启。旧配置省略该字段时仍可启动，默认不允许浏览器来源；无 Origin 的原调用继续工作。来源取看板网页的协议、主机、端口，与公司 Mac 的 Tailscale IP 无关。

请按[完整连接设备指南](docs/connect-device.md)获取 Tailscale HTTPS 地址及端口、配置来源并读取设备端令牌。看板不获取 Darkbloom 官方令牌，不再自动读取本机 Agent 文件；可手动添加或导入旧版连接文件。不要双击 `file://` 运行。

功能与开发验证见[看板说明](apps/dashboard/README.md)，架构见[静态看板架构](docs/dashboard.md)。

## 远程操作设备

在看板“我的设备”页选择真实设备，可远程切换模型、下载模型，以及开启、关闭和重启 Darkbloom。操作以后台任务执行，显示执行结果并保存最近 50 条记录。需要升级设备端 Agent；完整配置、恢复规则与接口见[远程操作说明](docs/remote-control.md)。

## 开发与构建

环境：Go 1.27，macOS / Apple Silicon；SQLite 使用纯 Go 驱动，不依赖本机 SQLite 或 CGo。

```sh
cd apps/agent
make check
make race
make build
./bin/bloomer-agent -version
```

依赖版本与校验和保存在 `go.mod` / `go.sum`。仅编译可运行 `make binary`，可执行文件位于 `apps/agent/bin/`。构建、打包和安装管理均不依赖 Python。

## 打包与安装

打包需要安装 Go 1.27 的 macOS / Apple Silicon 开发机，仅运行安装包的目标设备无需安装 Go。在仓库根目录执行：

```sh
make -C apps/agent build
open apps/agent/dist
```

默认版本统一定义在 `apps/agent/internal/release/version.go` 的 `Version` 常量中，当前为 `1.0.5`。日常发布只修改这一处，Agent、管理工具、安装器和 DMG 文件名会使用同一版本，无需打包时手动填写。临时构建仍可用 `VERSION=1.0.6` 覆盖。`make build` 会自动编译 Agent 和 Go 管理工具、生成图形安装器并打包 DMG，同时校验包内文件、应用签名及 DMG 完整性。`make dmg` 和 `make package` 执行相同流程。

生成的文件位于 `apps/agent/dist/`，不进入 Git：

- `bloomer-agent-1.0.5-darwin-arm64.dmg`：设备端安装包。
- `bloomer-agent-1.0.5-darwin-arm64.dmg.sha256`：安装包的 SHA-256 校验文件。

需要校验安装包时，在仓库根目录执行：

```sh
(cd apps/agent/dist && shasum -a 256 -c bloomer-agent-1.0.5-darwin-arm64.dmg.sha256)
```

将 DMG 复制到目标设备，在运行 Darkbloom 的同一 macOS 用户下打开，双击其中的 **安装 Bloomer.app**，即可安装并启动后台服务。已有版本时会执行升级，沿用配置、访问令牌和数据库，升级前自动创建一致备份。安装完成后可以推出 DMG。

当前安装包采用本地 ad-hoc 签名，尚未配置 Developer ID 签名和 Apple 公证，跨设备下载后可能受 Gatekeeper 限制。安装条件、命令行启停、升级和恢复见 [安装说明](docs/install.md)。

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

数据库已升级为 schema 5。已有 schema 1 / 2 / 3 / 4 数据库升级前自动建立一致备份；旧版本程序不能直接打开升级后的数据库，恢复方法见数据库文档。

设备端默认每 60 秒独立采集 CPU、内存、GPU 利用率、系统热压力、磁盘余量及可读取的 Darkbloom 运行状态，保存 365 天历史，支持最新状态和时间范围查询。空闲判断结合本机任务标志和精确匹配到该设备的最新收益（超过 5 分钟），无需网页登录令牌；热压力与内存压力直接从 macOS 采集，摄氏温度仍为空。参数、数据口径及接口见[设备状态采集](docs/device-state.md)。

## 验证

在仓库根目录运行离线进程测试；所有测试状态均在临时目录，结束后清理：

```sh
./scripts/check-agent.sh
make -C apps/agent binary
python3 scripts/smoke-agent.py
```

进程测试使用不存在的官方凭证验证降级查询、启动清理、认证、单实例、SIGTERM、SIGKILL 重启、持久化限流等待与私有路径。Go 测试包含本机 HTTPS 模拟服务，需要允许绑定回环端口；所有令牌和收益均为合成数据。

## 文档

- [静态看板架构](docs/dashboard.md)
- [连接设备指南](docs/connect-device.md)
- [官方接口复验与样本说明](docs/upstream-contract.md)
- [数据库迁移与恢复](docs/database.md)
- [DMG 安装与服务管理](docs/install.md)
- [macOS 下 Tailscale DNS 与 FlClash TUN 共存及撤销](docs/tailscale-dns-with-flclash.md)
- [Darkbloom 远程操作](docs/remote-control.md)
- [共享 OpenAPI 契约](contracts/openapi.yaml)

真实凭证、私有配置、数据库、WAL、备份、日志和构建产物不进入 Git。Agent 的运行目录应位于仓库之外。
