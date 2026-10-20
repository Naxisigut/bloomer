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

不传递 `--force`，不自动杀死推理 daemon。官方命令的排空超时可能保留 draining 状态；失败时应核对设备状态，再决定下一步。开启与重启成功表示 CLI 已确认其启动步骤，不证明后续持续联网、模型加载就绪或产生收益；设备状态仍按每 60 秒的采样周期更新。

本地模型来自 `models list --json --all`；目录来自 `models catalog --json`，两者独立处理错误。目录不可达时保留本地模型操作，界面提示网络问题。每项发现命令最多 4 秒，超时后可手动重试。设备运行时与内存预算仍由官方 CLI 校验，清单中的模型不保证能在当前设备上加载。

## 配置与认证

旧配置升级后默认启用，默认 CLI 路径为 `~/.darkbloom/bin/darkbloom`。可合并以下字段到 Agent 配置，重启 Agent 生效：

```json
{
  "remote_control_enabled": true,
  "darkbloom_binary": "~/.darkbloom/bin/darkbloom"
}
```

设置 `remote_control_enabled: false` 可禁用提交操作及模型发现，历史仍可查。二进制缺失或控制历史损坏不影响收益采集和状态查询，远程操作显示不可用。自定义 CLI 路径必须在设备端配置；请求不能指定程序、参数、路径或任意 URL。

控制接口使用既有 Bloomer 独立 Bearer 令牌，因此持有该令牌的客户端在启用本功能后也具有设备操作权限。Agent 仍仅监听回环地址，通过现有 Tailscale Serve 私有入口连接。看板验证本机 Host 和同源 Origin，服务端转发 Bearer，浏览器不直接连接 Agent，也不接收已保存令牌。Agent 拒绝带 Origin 的直接浏览器请求。命令通过固定程序和参数数组执行，不经过 shell；标准输入关闭，输出和错误文本不返回或持久化。

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

自动测试覆盖固定命令参数、注入拒绝、认证和来源限制、并发互斥、幂等、私有持久化、进程中断恢复、缺失 CLI、禁用、保存失败、模型目录部分失败及看板代理。浏览器使用合成设备验证模型选择、确认框、异步提交与完成状态；不会为测试执行真实设备的启停、切换或大模型下载。实际使用前需要升级目标设备 Agent，并用该设备的 Darkbloom 版本联调。

官方行为依据：[CLI 参考](https://github.com/Layr-Labs/d-inference/blob/master/docs/provider/cli-reference.md)及本机安装版命令帮助；不硬编码模型目录。
