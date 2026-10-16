# Bloomer

运行在 Darkbloom 设备上的收益采集 Agent，与独立看板共用接口契约。当前已完成 **P0（契约与环境）和 P1（工程与数据库）**。

当前程序可离线启动，提供需要独立 Bearer 令牌的健康接口；收益采集、恢复清理和收益查询分别在 P2、P3、P4 实现。

## 开发与构建

环境：Go 1.27，macOS / Apple Silicon；SQLite 使用纯 Go 驱动，不依赖本机 SQLite 或 CGo。

```sh
cd apps/agent
make check
make race
make build
./bin/bloomer-agent -version
```

依赖版本与校验和保存在 `go.mod` / `go.sum`。构建产物为 `apps/agent/bin/bloomer-agent`，不进入 Git。

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

P1 不读取官方凭证或调用官方接口，即使官方令牌文件不存在也可启动。配置的相对路径以配置文件目录为基准，`~/` 通过用户目录 API 展开。不存在的 `-config`、无效路径、非回环监听、端口占用、重复启动和不支持的数据库版本均明确报错。访问令牌只在首次缺失时生成，重启沿用。

健康检查示例（在运行 Agent 的设备上执行，令牌只在 Python 进程内使用）：

```sh
python3 - <<'PY'
from pathlib import Path
import urllib.request
secret = (Path.home() / 'Library/Application Support/Bloomer/agent/access.token').read_text().strip()
request = urllib.request.Request('http://127.0.0.1:17890/v1/health', headers={'Authorization': 'Bearer ' + secret})
with urllib.request.urlopen(request, timeout=5) as response:
    print(response.read().decode())
PY
```

终端运行时使用 Ctrl+C 或 SIGTERM 正常停止。P1 保存正常关闭时间，但采集恢复判断在 P3 实现。LaunchAgent 安装与 Tailscale Serve 发布在 P5 交付。

## 验证

在仓库根目录运行离线进程测试；所有测试状态均在临时目录，结束后清理：

```sh
./scripts/check-agent.sh
make -C apps/agent build
python3 scripts/smoke-agent.py
```

进程测试验证认证、单实例、SIGTERM 保存关闭时间、SIGKILL 后重启、令牌复用和文件权限，需要允许绑定回环临时端口。数据库测试使用真实临时 SQLite，包含迁移失败回滚、一致性备份和只读连接约束。

## 文档

- [开发目标](docs/agent-development-goals.md)
- [实施计划](docs/agent-development-plan.md)
- [P0/P1 验证记录与未验证边界](docs/p0-p1-validation.md)
- [官方接口复验与样本说明](docs/upstream-contract.md)
- [数据库迁移与恢复](docs/database.md)
- [共享 OpenAPI 草案](contracts/openapi.yaml)
- [仓库结构](docs/repository-structure.md)

真实凭证、私有配置、数据库、WAL、备份、日志和构建产物不进入 Git。Agent 的运行目录应位于仓库之外。
