# Darkbloom 远程操作

在看板的“我的设备”页选择真实设备，使用“远程操作”面板。设备需升级为包含本功能的 Bloomer Agent，在运行 Darkbloom 的同一 macOS 用户下运行，且已完成 Darkbloom 安装和账户登录。旧 Agent 返回明确的升级提示；演示设备只读。

## 操作

| 操作 | 行为 |
| --- | --- |
| 切换模型 | 多选本地模型，调用 `darkbloom switch --model <id> ... --timeout 600`；替换整套托管模型，保持现有进程与连接。需要设备版本和协调端支持热切换 |
| 下载模型 | 从官方目录选择单个模型，调用 `darkbloom models download <id>`；下载到设备自己的模型缓存，校验由官方 CLI 完成，不会自动切换 |
| 开启 | 多选本地模型，调用 `darkbloom start --model <id> ... --timeout 600`；跳过模型选择提示，沿用已保存的空闲内存策略等配置 |
| 关闭 | 调用 `darkbloom stop --timeout 600`，等待已接收任务完成后持久停止服务 |
| 重启 | 调用 `darkbloom restart --timeout 600 --startup-timeout 180`，沿用现有模型选择和配置 |

手动重启不传递 `--force`，不自动杀死推理 daemon。官方命令的排空超时可能保留 draining 状态；失败时应核对设备状态，再决定下一步。开启与重启成功表示 CLI 已确认其启动步骤，不证明后续持续联网、模型加载就绪或产生收益；设备状态仍按每 60 秒的采样周期更新。

## Qwen3.5 9B 超时强制重启

远程操作面板有独立开关，默认关闭。开启后由 Agent 在本机判断，不依赖看板是否打开：

- 当前模型是 `Qwen3.5-9B`（也接受带组织前缀，或空格、下划线写成的 Qwen3.5 9B）。
- `inference_active` 连续为真，且官方状态里的 `requests_served` 没有增加。计数增加视为新任务，重新计时。状态里没有 `requests_served` 时无法区分任务，不会触发。
- 同一任务从首次观察到超过 10 分钟。恰好 10 分钟不重启。采样间隔约 60 秒，因此实际触发会落在 10 分钟之后的下一次采样。
- 状态文件超过 90 秒、进程身份变化，或两次有效采样间隔超过 3 分钟，都无法证明仍是同一任务，重新计时。

满足条件时调用 `darkbloom restart --force --timeout 600 --startup-timeout 180`，中断未完成任务后重启，并记入操作记录，动作为强制重启。它与其他远程操作互斥；正在执行别的操作时本轮跳过，下一轮再试。同一次任务提交后 15 分钟内不重复提交。远程操作被配置关闭，或设备状态采集关闭时，开关可以保存，但不会发出重启。

`long_task_restart_enabled` 只决定第一次没有 `long-task-restart.json` 时的默认值。看板开关立即写入该私有文件，重启 Agent 后仍然有效。

本地模型来自 `models list --json --all`；目录来自 `models catalog --json`，两者独立处理错误。目录不可达时保留本地模型操作，界面提示网络问题。每次发现最多 4 秒，超时后可手动重试。设备运行时与内存预算仍由官方 CLI 校验，清单中的模型不保证能在当前设备上加载。

模型清单在看板内是**每设备的全局数据**：一次请求同时返回本地模型与官方目录，看板按设备保留一份快照，整个会话复用。进入「我的设备」不会重复请求；「本地模型」和「下载模型」两个区块各有「获取最新」按钮，面板右上角的「刷新模型与记录」也会同时重取，此外不做定时刷新。自动轮询只更新操作记录，因此发现失败（含 409 冲突）不会在每次进入页面时反复重试，也不会清空已有列表。读取失败时两个区块显示失败原因并保留「获取最新」入口；单侧失败只影响对应区块，另一侧照常可用。正在读取时只有依赖清单的操作与选择框被禁用，关闭与重启不受影响。下载任务成功后自动重取一次，因为本地模型已变化。

## 配置与认证

旧配置升级后默认启用，默认 CLI 路径为 `~/.darkbloom/bin/darkbloom`。可合并以下字段到 Agent 配置，重启 Agent 生效：

```json
{
  "remote_control_enabled": true,
  "long_task_restart_enabled": false,
  "darkbloom_binary": "~/.darkbloom/bin/darkbloom"
}
```

设置 `remote_control_enabled: false` 可禁用提交操作及模型发现，历史仍可查。二进制缺失或控制历史损坏不影响收益采集和状态查询，远程操作显示不可用。自定义 CLI 路径必须在设备端配置；请求不能指定程序、参数、路径或任意 URL。

控制接口使用既有 Bloomer 独立 Bearer 令牌，因此持有该令牌的客户端在启用本功能后也具有设备操作权限。Agent 仍仅监听回环地址，通过现有 Tailscale Serve 私有入口连接。浏览器在本地存储连接信息并携带 Bearer 直接调用 Agent；Agent 1.0.7 起允许任意网页来源，合法 OPTIONS 预检不需要令牌，实际 GET/POST 始终校验令牌。无需来源配置，CLI 调用兼容。设备端读取令牌命令和升级步骤见[连接设备指南](connect-device.md)。命令通过固定程序和参数数组执行，不经过 shell；标准输入关闭，输出和错误文本不返回或持久化。

## 后台任务与恢复

每个设备最多一项控制操作执行中，包括下载。同一 `request_id` 在最近 50 条记录保留期间重试时返回已有任务；参数不一致为 409。网页提交前展示设备、动作和完整模型选择。提交断网时浏览器保存原请求编号，先查操作记录，再用相同编号重试。

任务接受后立即返回 202；由 Agent 独立执行，不随浏览器断开而取消。状态为 `running`、`succeeded`、`failed`、`unconfirmed`；看板在设备页每 3 秒刷新执行中的任务，完成后刷新模型和设备状态。关闭/重启/切换/开启最多执行 15 分钟，下载最多 6 小时。到时终止本次 CLI 及其子进程，不推断 daemon 的最终状态。

最近 50 条操作保存到 Agent 私有数据目录的 `control-jobs.json`（0600）。先原子保存意图再启动命令；保存失败拒绝执行。Agent 退出会取消控制命令，重启时把遗留执行中记录标记为 `unconfirmed / agent_interrupted`，不自动重放。结果持久化失败时禁用新的控制操作，重启后仍需核对设备实际状态。记录只包含动作、模型 ID、请求编号、时间、状态和固定错误代码，不包含 CLI 原始输出、凭证或本地缓存路径。

## 接口

- `GET /v1/device/control`：返回 `enabled`、`available`、`jobs`。
- `GET /v1/device/control/models`：返回 `local`、`catalog`；可带 `local_error` / `catalog_error`，不返回 CLI 输出里的私有路径。
- `GET /v1/device/control/jobs`：返回最近操作，最新在前。
- `POST /v1/device/control/jobs`：JSON 对象，必须提供 `request_id` 和 `action`；开启/切换需提供 1～16 个唯一 `models`，下载恰好一个，关闭/重启不接受模型。拒绝未知字段、额外 JSON、查询参数、非法模型及参数注入。

```json
{
  "request_id": "9a433cb9-79c7-4a04-98d0-c0d2c19115f0",
  "action": "switch",
  "models": ["Qwen3.5-9B"]
}
```

错误代码：`control_disabled`（403）、`control_busy` / `request_conflict`（409）、`control_unavailable`（503）、`invalid_request`（400）。看板将旧 Agent 的 404/405/501 转为 `control_unsupported`。

## 验证范围

自动测试覆盖固定命令参数、注入拒绝、认证和来源限制、并发互斥、幂等、私有持久化、进程中断恢复、缺失 CLI、禁用、保存失败、模型目录部分失败及浏览器直连接口。浏览器使用合成设备验证模型选择、确认框、异步提交与完成状态；不会为测试执行真实设备的启停、切换或大模型下载。本次静态化只进行本地模拟验证；真实 Tailscale HTTPS 及设备控制联调尚未执行。实际使用前需要升级目标设备 Agent。

官方行为依据：[CLI 参考](https://github.com/Layr-Labs/d-inference/blob/master/docs/provider/cli-reference.md)及本机安装版命令帮助；不硬编码模型目录。
