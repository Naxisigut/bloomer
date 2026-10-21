# Bark 设备告警

Agent 1.0.8 起支持。默认关闭；启用后在每次本机 telemetry 采集完成后判定，不改变原有收益、空闲、模型或 `provider_connected` 字段的含义。采集每 60 秒一次；发送和重试在独立后台任务中执行，不等待网络完成再采集。

## 配置与 Key

在 iPhone 安装并打开 [Bark](https://bark.day.app/)，允许通知。首页推送地址形如 `https://api.day.app/你的Key/推送内容`，**`api.day.app/` 后面到下一条 `/` 之间的那段就是 Key**。不要把完整 URL 当作 Key。严重告警使用 `critical` 和 `alarm`，需在 iOS 中允许 Bark 的重要警告权限。

用本机文本编辑器把 Key 单独保存到 `~/Library/Application Support/Bloomer/agent/bark.key`，文件只包含 Key（允许末尾换行），然后设置权限：

```sh
chmod 600 "$HOME/Library/Application Support/Bloomer/agent/bark.key"
```

Key 必须为权限 **0600** 的普通文件，不接受符号链接、空文件或超过 4 KiB 的内容。每次发送重新读取，轮换 Key 无需重启。Key 不进入配置 JSON、日志、数据库、API 或 Git；仓库忽略 `*.key`。不要把含 Key 的 Bark 首页地址粘贴进日志或版本库。

把下面对象合并进现有配置并重启 Agent（缺少 `alert` 的旧配置默认关闭）：

```json
{
  "alert": {
    "enabled": true,
    "bark_key_file": "~/Library/Application Support/Bloomer/agent/bark.key",
    "bark_server": "https://api.day.app",
    "bind_interface": "en1",
    "repeat_minutes": 30,
    "dashboard_url": "",
    "device_name": "我的 Mac",
    "disconnect_seconds": 180,
    "consecutive_samples": 2,
    "flap_window_minutes": 60,
    "flap_changes": 5,
    "no_earnings_minutes": 30,
    "night_no_earnings_minutes": 60,
    "night_start_hour": 0,
    "night_end_hour": 8
  }
}
```

所有规则阈值与重复提醒间隔均可配置，正整数范围 1–10080；夜间起止小时为 0–23，起点包含、终点不包含，支持跨午夜，相等表示无夜间窗口。省略字段使用上表默认值。设备名省略时使用系统主机名。`dashboard_url` 非空时，点击通知打开该 URL。配置路径支持 `~/` 和相对于配置文件的路径；只配置 Key 文件路径。

`device_state_enabled` 必须保持开启才能获得新样本并判定。Agent、电脑、通知服务或互联网本身停止期间无法即时发送；重启后从持久化状态继续，不补造休眠期间的样本。

## 规则与恢复

| 类型 | 默认触发条件 | 级别 |
| --- | --- | --- |
| `coordinator_disconnected` | 确认 provider 运行、状态文件新鲜，授权心跳距采集时严格超过 180 秒，连续 2 次 | critical |
| `connection_flapping` | 新鲜样本中 `session_id` 在过去 60 分钟内变化至少 5 次；首次观察到的会话不计为变化 | active |
| `provider_not_running` | `provider_running=false` 且质量 ok，连续 2 次 | critical |
| `model_not_ready` | `model_ready=false` 且质量 ok、provider 运行及状态文件新鲜，连续 2 次 | critical |
| `no_earnings` | provider 运行且授权心跳正常，本设备 `last_device_earning_at` 距采集时严格超过 30 分钟；北京时间 00:00–08:00 放宽至 60 分钟 | active |
| `provider_state_unknown` | 进程证据不可用，或运行中但状态文件过期/无效，或缺少有效授权心跳，连续 2 次 | critical |
| 恢复事件 `recovered` | 已触发的对应规则获得明确正常证据 | active |

`trust.status=online` 只表示授权裁定，不能证明 socket 在线。新增 `trust_received_at` 来自 `trust.received_at`，保留浮点 Unix 秒；`session_id` 来自 `trust.authorization.session_id`，与收益 Provider ID 对应。两者仅在进程存活且 `written_at` 新鲜时使用，不改写旧占位字段 `provider_connected`。

沿用原有状态文件规则：`written_at` 距采集时超过 90 秒为 stale，超前超过 5 秒为 invalid。此时不把旧授权心跳当作断线证据，走“进程停止 / 状态未知”分支。未知值打断尚未触发的连续计数，也不会凭空恢复已触发告警。恢复必须再次获得对应的正常证据：例如状态文件未知不能恢复断线，provider 停止不能恢复无收益。连续样本间隔超过两次采集周期会重置未完成计数；重复/倒序样本不计数。时间回拨后等待采集时钟超过已记录时间再判定。

无本设备收益时间、收益时间在未来或连接证据缺失时，无收益规则不判定。不会用账户总收益代替设备收益。收益同步失败时沿用样本中已知的最后一笔本机收益；仍以该笔实际时间计算间隔。夜间只是放宽阈值，已有无收益告警可在进入夜间且符合新阈值时恢复。波动告警在窗口内变化次数降至阈值以下、且再次观察到有效会话时恢复。

首次达到条件推送一次，仍异常时默认每 30 分钟再次提醒。首次触发时间、最后成功推送时间、恢复标志、连续计数和会话变化窗口存入 `agent.db`。重启不会因内存清空再次触发；恢复通知包含从首次触发到恢复的时长，例如“已恢复，持续 4 小时 21 分钟”。持续提醒也包含经过时长。推送标题、正文和开始时间为简体中文、北京时间；API 时间使用 RFC 3339 UTC，看板转换为北京时间。

## 网络与重试

每条请求为 `POST {bark_server}/push`，JSON 包含 `device_key`、`title`、`body`、`group=darkbloom`、`level`；critical 增加 `sound=alarm`，配置后增加 `url`。实现依据 [Bark 官方接口文档](https://github.com/Finb/bark-server/blob/master/docs/API_V2.md)。HTTP 2xx 且响应 JSON 的 `code=200` 才记为成功；不跟随重定向，不保存服务端返回的正文。

`bind_interface` 应填写物理网卡名，用 `networksetup -listallhardwareports` 核对，Wi-Fi 不一定是 en1。发送时重新取得该网卡 IPv4 并设置 `net.Dialer.LocalAddr`；在 macOS 上还使用 [`IP_BOUND_IF`](https://raw.githubusercontent.com/apple-oss-distributions/xnu/main/bsd/netinet/in.h) 将 socket 限制到该网卡，DNS 查询也使用相同网卡，不使用 HTTP/HTTPS 代理环境变量。网卡不存在、未启用或无 IPv4 时记录警告并回退默认路由；空字符串表示使用默认路由。

这绕开 TUN 路由与应用代理，仍需要物理网络和可用的 DNS、Bark 服务。如果 FlClash 同时把系统 DNS 指向自身的本地代理或返回 fake-IP，需给 Bark 域名配置直连和可独立工作的真实 DNS，并实际执行下面的断代理测试；仅绑定网卡不能修复失效的 DNS 或物理断网。回退默认路由时也可能再次经过 TUN。

请求总超时 10 秒。初次失败后依次间隔 1、5、15 分钟重试，**最多额外重试 3 次，共 4 次尝试**；队列与下次时间持久化，重启继续原退避。每次尝试的结果与次数写日志和记录，错误使用固定安全代码 `bark_send_failed`。到达重试上限后，本次记录标为失败，持续异常仍按重复提醒周期创建新提醒（以最后入队时间节流，避免失败风暴）。恢复事件会取消尚未送达的同类型旧告警，旧记录标为 `superseded`，避免延迟发送过时告警。

成功检查点持久化后重启不会重发。Bark 没有提供本实现可用的幂等确认：若服务端实际已接收但连接超时，或成功响应后进程在数据库落盘前突然退出，重试可能重复；采用持久化待发送队列优先保留恢复通知。

## API、看板与验证

两个接口均沿用 Bearer `access.token` 鉴权和原有 CORS：

- `GET /v1/alerts`：返回 `{ "items": [...] }`，按记录 ID 倒序最近 50 条；关闭告警后仍可查询历史。
- `POST /v1/alerts/test`：空请求体，启用后返回 HTTP 202 和一条 `test` 记录，后台发送 active 测试推送；关闭时返回 503 / `alerts_disabled`。测试请求也会重试并留下记录。

记录包括类型、事件（首次、提醒、恢复、测试）、开始时间、持续秒数、是否恢复、最后成功推送时间、尝试次数、下次重试时间、推送状态和安全错误码。记录保留最近 50 条；规则状态和待发送队列独立保存，不会因清理历史丢失恢复通知。schema 6 升级沿用一致备份流程；设备样本的新增字段继续随原有 365 天留存清理。

在看板“告警记录”页选择真实设备，点击“发送测试推送”，刷新观察推送成功状态并确认 iPhone 收到通知。演示设备只显示模拟记录，按钮禁用。也可从设备端调用：

```sh
# access.token 是看板的独立令牌，不是 Bark Key。
curl -X POST -H "Authorization: Bearer $(cat "$HOME/Library/Application Support/Bloomer/agent/access.token")" \
  http://127.0.0.1:17890/v1/alerts/test
curl -H "Authorization: Bearer $(cat "$HOME/Library/Application Support/Bloomer/agent/access.token")" \
  http://127.0.0.1:17890/v1/alerts
```

验证 TUN 故障：先确认普通测试推送成功；在可恢复的测试环境中使 FlClash 代理不可用，重复调用测试接口，确认仍能收到通知。连接告警约在授权心跳超时后连续两次采集判定；provider 停止和模型未就绪默认第二次采集触发。不要通过修改真实 daemon-state.json 制造故障，使用测试环境或下面的单元测试。

```sh
cd apps/agent
make check
make race
cd ../dashboard
npm run check
npm test
```

自动化覆盖各规则触发/恢复、两次计数、时间边界/夜间阈值、节流、重启恢复、stale 分支、会话窗口、50 条清理、持久化退避、Bark 格式和 Key 不泄露。测试仅使用 `httptest` 本机服务器，不联系真实 Bark 或读取真实 Key。
