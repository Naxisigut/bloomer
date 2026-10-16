# macOS 安装、升级、停止与恢复

适用：运行 Darkbloom 的同一 macOS 用户、Apple Silicon、已登录的用户会话。无需目标设备安装 Go。LaunchAgent 在登录后运行，注销和休眠会暂停采集。

## 构建与安装

开发机在仓库根目录执行：

```sh
make -C apps/agent build
python3 scripts/package-agent.py
```

产物位于 `apps/agent/dist/bloomer-agent-1.0.0-darwin-arm64.tar.gz`。包内含可执行文件、版本/构建标识、SHA-256 清单、配置示例、LaunchAgent 模板、管理脚本及 OpenAPI。

将包放到目标设备的临时目录并解压，在解压目录执行：

```sh
python3 manage.py install --plan
python3 manage.py install
```

安装前检查校验和、GUI launchd 域、官方凭证可读性与私有权限、回环端口可用性和 Tailscale 已连接。默认监听 `127.0.0.1:17890`，通过 Tailscale Serve 的 **8443 HTTPS 端口**提供 tailnet 私网访问。已有同端口的其他服务或公开 Funnel 设置会使安装失败，不执行 `serve reset`。

程序与版本目录：`~/Library/Application Support/Bloomer/agent/bin/`；私有配置位于同目录上一级的 `config.json`。首次成功安装生成独立 `access.token`；凭证从 `~/.darkbloom/auth_token` 读取，不复制到安装包。

安装输出包含实际私网 HTTPS 地址，不包含令牌。看板或调用端使用独立访问令牌。跨设备验证令牌时在目标用户的私有环境中读取，不粘贴到聊天、命令历史或日志。

## 启动、停止和状态

安装后的管理脚本位于：

```sh
MANAGER="$HOME/Library/Application Support/Bloomer/agent/management/manage.py"
python3 "$MANAGER" status
python3 "$MANAGER" stop
python3 "$MANAGER" start
```

`stop` 禁用此 LaunchAgent 并执行 bootout，等待进程释放数据库锁；`start` 重新启用并 bootstrap。不要仅用 kill 判断服务已停止，launchd 会自动拉起异常退出的进程。停止后 Serve 路由仍存在，但本地应用不再提供响应。需要撤销私网路由时，单独运行 `tailscale serve --https=8443 off`。

应用日志在 `~/Library/Logs/Bloomer/agent/agent.log`，按 5 MB / 10 文件 / 14 天策略轮转。launchd 标准输出和错误流指向 `/dev/null`，不产生无界启动错误文件。若反复启动失败，查看 `launchctl print gui/$(id -u)/dev.bloomer.agent` 的退出状态，停止服务后前台运行程序或执行 `-check` 排查。

## 升级和回退

解压新包后执行：

```sh
python3 manage.py upgrade
```

配置、独立令牌和数据库沿用。升级先验证候选程序与配置，再正常停止旧服务，建立 SQLite 一致备份，切换版本链接并检查启动及 HTTPS 路由。失败时恢复旧程序链接、plist 和数据库备份，并按原运行状态恢复服务。数据库不能仅复制活跃的 `.db` 主文件。

升级前备份保存在私有数据目录的 `backups/`。迁移本身还会在 schema 升级前建立 `.pre-vN-*.bak`。验证成功后按维护需要处理这些恢复保障副本；它们不属于自动周期备份。旧程序不支持新 schema 时，必须同时恢复兼容备份才能回退。

## 一致备份和数据库恢复

先停止服务，再使用绝对路径：

```sh
BASE="$HOME/Library/Application Support/Bloomer/agent"
python3 "$BASE/management/manage.py" stop
"$BASE/bin/current/bloomer-agent" -config "$BASE/config.json" -backup "$BASE/backups/manual.db"
python3 "$BASE/management/manage.py" restore --backup "$BASE/backups/manual.db"
python3 "$BASE/management/manage.py" start
```

备份目标必须不存在，父目录须已创建。`-backup` 和 `-restore` 都要求获取单实例锁；运行中的服务会拒绝维护操作。恢复只接受完整性和迁移历史校验通过的独立一致备份，拒绝活跃 WAL 数据源和不支持的 schema。

恢复前保留当前数据库的一致副本；旧 WAL / SHM 单独移到恢复保障文件名，不与恢复后的数据库混用。恢复源文件不被修改。需要修改数据目录或监听端口时先停止服务，再编辑私有配置并重新安装/启动；HTTPS 路由应与实际监听一致。

## 连接方式

本项目使用 [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve) 提供私网 HTTPS，不启用 Funnel。后台采用 [Apple LaunchAgent](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html)；运行 Darkbloom 的用户需要保持登录，不承诺无人登录或休眠时采集。
