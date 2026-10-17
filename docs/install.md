# macOS 安装、升级、停止与恢复

适用：运行 Darkbloom 的同一 macOS 用户、Apple Silicon、已登录的用户会话。无需目标设备安装 Go。LaunchAgent 在登录后运行，注销和休眠会暂停采集。

## 构建与安装

开发机需要 Go 1.27 和 macOS / Apple Silicon。在仓库根目录执行：

```sh
make -C apps/agent build
```

默认产物：

- `apps/agent/dist/bloomer-agent-1.0.1-darwin-arm64.dmg`
- `apps/agent/dist/bloomer-agent-1.0.1-darwin-arm64.dmg.sha256`

`make build` 完成编译和 DMG 打包；`make binary` 仅编译 `bin/bloomer-agent` 与 `bin/bloomerctl`，适合开发调试。`make dmg` / `make package` 同样先编译再打包。默认版本来自 `apps/agent/internal/release/version.go` 的 `Version` 常量，当前为 `1.0.1`；发布时修改这一处即可，无需额外传参。临时覆盖版本可使用 `make -C apps/agent build VERSION=1.0.2`。直接使用 Go 编译时也使用该常量作为默认版本。版本、Git 提交号及工作区是否有未提交修改写入程序和清单。

整个构建、打包、安装和管理流程均不依赖 Python。DMG 使用 macOS 自带的 `osacompile`、`codesign` 和 `hdiutil` 生成；后台服务与管理工具均为不依赖 CGo 的 Go 可执行文件。打包时校验内部 SHA-256 清单、应用签名及 DMG 完整性。默认使用本地 ad-hoc 签名，尚未配置 Developer ID 签名和 Apple 公证；这不是已公证的公开发行包，跨设备下载后可能受 Gatekeeper 限制。

将 DMG 放到目标设备，双击打开，再双击其中的 **安装 Bloomer.app**。安装器在当前用户下完成安装、启动后台服务并配置登录后运行；已有版本时保留配置和数据并执行升级。完成后可以推出 DMG，服务不依赖挂载卷。DMG 只负责承载安装器，打开镜像本身不会自动运行程序。

需要在终端预览或执行安装时，可从已挂载卷调用（版本对应卷名）：

```sh
PACKAGE="/Volumes/Bloomer 1.0.1/安装 Bloomer.app/Contents/Resources/package"
"$PACKAGE/bloomerctl" install --package-dir "$PACKAGE" --plan
"$PACKAGE/bloomerctl" install --package-dir "$PACKAGE"
```

`--plan` 只校验包并输出路径，不创建运行文件或操作服务。实际安装检查校验和、GUI launchd 域、官方凭证可读性与私有权限、回环端口可用性和 Tailscale 已连接。默认监听 `127.0.0.1:17890`，通过 Tailscale Serve 的 **8443 HTTPS 端口**提供 tailnet 私网访问。已有同端口的其他服务、其他路径或公开 Funnel 设置会使安装失败，不执行 `serve reset`。

停止旧服务前还会检查本设备的 Tailscale HTTPS 证书能力。若尚未就绪，先在 Tailscale 管理后台启用 MagicDNS 和 HTTPS Certificates；若已启用但设备尚未收到状态，重新连接或更新 Tailscale 后重试。需要网页授权时，在可见终端运行 `tailscale serve --bg --https=8443 http://127.0.0.1:17890` 并按提示完成授权。已有相同的私网路由会直接复用。

程序与版本目录：`~/Library/Application Support/Bloomer/agent/bin/`；私有配置位于同目录上一级的 `config.json`。首次成功安装生成独立 `access.token`；凭证从 `~/.darkbloom/auth_token` 读取，不复制到安装包。

终端安装输出包含实际私网 HTTPS 地址，不包含令牌；图形安装器报告安装成功或错误。实际地址也保存到私有文件 `~/Library/Application Support/Bloomer/agent/install-state.json`。看板或调用端使用独立访问令牌。跨设备验证令牌时在目标用户的私有环境中读取，不粘贴到聊天、命令历史或日志。

管理工具在前置检查前即安装到 `management/bloomerctl`，即使 HTTPS 配置失败也保留以便诊断和恢复。`install-progress.json` 单独记录最近一次安装尝试的版本、阶段、状态和时间，权限为 `0600`；`bloomerctl status` 的 `installation` 字段会显示这份记录。`failed` 表示前置检查失败，`rolled_back` 表示失败后已回滚，`rollback_failed` 表示回滚仍有未完成事项，`complete` 才表示本次安装完整结束。异常断电或强制结束时，文件会保留最后写入的阶段，不能只根据 `installing` 判断进程仍在运行。

`install-state.json` 仅在全部验证通过后写入；升级失败保留上一次成功记录。外部命令通常限时 30 秒，备份与恢复限时 2 分钟；超时会清理本次命令的进程组，并最多再等待 2 秒关闭残留输出管道。失败信息包括安装阶段、原始错误以及有长度限制的标准输出和错误输出，图形安装器会展示该信息。

## 启动、停止和状态

安装后的 Go 管理工具位于：

```sh
MANAGER="$HOME/Library/Application Support/Bloomer/agent/management/bloomerctl"
"$MANAGER" status
"$MANAGER" health
"$MANAGER" stop
"$MANAGER" start
```

`stop` 禁用此 LaunchAgent 并执行 bootout，等待进程释放数据库锁；`start` 重新启用并 bootstrap。不要仅用 kill 判断服务已停止，launchd 会自动拉起异常退出的进程。停止后 Serve 路由仍存在，但本地应用不再提供响应。需要撤销私网路由时，单独运行 `tailscale serve --https=8443 off`。

应用日志在 `~/Library/Logs/Bloomer/agent/agent.log`，按 5 MB / 10 文件 / 14 天策略轮转。launchd 标准输出和错误流指向 `/dev/null`，不产生无界启动错误文件。若反复启动失败，查看 `launchctl print gui/$(id -u)/dev.bloomer.agent` 的退出状态，停止服务后前台运行程序或执行 `-check` 排查。

## 升级和回退

打开新版本 DMG 后双击安装器即可升级，或使用新包中的管理工具：

```sh
"$PACKAGE/bloomerctl" upgrade --package-dir "$PACKAGE"
```

配置、独立令牌和数据库沿用。升级先验证候选程序与配置，再正常停止旧服务，建立 SQLite 一致备份，切换版本链接并检查启动及 HTTPS 路由。失败时恢复旧程序链接、plist 和数据库备份，并按原运行状态恢复服务。回滚只撤销本次尝试新建且仍匹配 Bloomer 的路由，保留预先配置的相同路由；路由清理失败会明确报错，但不会阻止已安全恢复的旧 Agent 继续本机采集。数据库不能仅复制活跃的 `.db` 主文件。

升级前备份保存在私有数据目录的 `backups/`。迁移本身还会在 schema 升级前建立 `.pre-vN-*.bak`。验证成功后按维护需要处理这些恢复保障副本；它们不属于自动周期备份。旧程序不支持新 schema 时，必须同时恢复兼容备份才能回退。

## 一致备份和数据库恢复

先停止服务，再使用绝对路径：

```sh
BASE="$HOME/Library/Application Support/Bloomer/agent"
"$BASE/management/bloomerctl" stop
"$BASE/bin/current/bloomer-agent" -config "$BASE/config.json" -backup "$BASE/backups/manual.db"
"$BASE/management/bloomerctl" restore --backup "$BASE/backups/manual.db"
"$BASE/management/bloomerctl" start
```

备份目标必须不存在，父目录须已创建。`-backup` 和 `-restore` 都要求获取单实例锁；运行中的服务会拒绝维护操作。恢复只接受完整性和迁移历史校验通过的独立一致备份，拒绝活跃 WAL 数据源和不支持的 schema。

恢复前保留当前数据库的一致副本；旧 WAL / SHM 单独移到恢复保障文件名，不与恢复后的数据库混用。恢复源文件不被修改。需要修改数据目录或监听端口时先停止服务，再编辑私有配置并重新安装/启动；HTTPS 路由应与实际监听一致。

## 连接方式

本项目使用 [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve) 提供私网 HTTPS，不启用 Funnel。后台采用 [Apple LaunchAgent](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html)；运行 Darkbloom 的用户需要保持登录，不承诺无人登录或休眠时采集。
