# 连接设备指南

本指南适用于个人使用：公司 Mac（Tailscale IP `100.88.173.51`）打开静态看板，家中 Mac（`100.101.88.84`）运行 Darkbloom 和 Bloomer Agent。两台 Mac 已在同一 Tailscale 网络，无需配对码、账号系统或中心服务。看板通过现有 Tailscale HTTPS 入口直接调用 Agent。

## 1. 家中设备安装并启动 Agent

在**家中 Mac 上、安装和运行 Darkbloom 的同一 macOS 用户**下，按[安装说明](install.md)安装 1.0.7 或更新版本 Agent。已有安装直接升级，保留 `agent.db`、历史、现有 `config.json` 和 `access.token`；不要重新初始化数据目录，也不要用示例配置覆盖自己的配置。升级后无需编辑网页来源配置。

两台 Mac 都登录同一 Tailscale 网络并保持连接；家中用户保持登录，设备不休眠。家中终端可检查 Agent：

```sh
MANAGER="$HOME/Library/Application Support/Bloomer/agent/management/bloomerctl"
"$MANAGER" status
"$MANAGER" health
```

这里操作的是 Bloomer 后台服务。无需为了连接看板去启停 Darkbloom、切换模型或下载模型。

## 2. 找到 Agent 的 HTTPS 地址

在家中设备终端执行：

```sh
tailscale serve status
```

找到映射到 `http://127.0.0.1:17890` 的现有 HTTPS 入口。安装器默认使用 **8443**。也可查看安装时输出的地址或私有 `~/Library/Application Support/Bloomer/agent/install-state.json`。

看板应填写实际域名及端口，例如：

```text
https://home-mac.your-tailnet.ts.net:8443
```

示例域名需要替换；地址**包含实际 HTTPS 端口、不附加 `/v1`、路径、查询串或令牌**。若现有入口使用 443，填写 `:443` 即可（浏览器会将默认端口规范化）。浏览器自行在其后添加 `/v1/...`。

`100.101.88.84` 用于确认家中设备的 Tailscale 连通性；不能直接替换 HTTPS 域名，否则证书名称通常不匹配。公司 IP `100.88.173.51` 也不是家中 Agent 地址。不要改为公开 Funnel，不需要调整现有 Serve 路由。

## 3. 升级后无需配置网页来源

Agent 1.0.7 起允许任意网页来源，返回 `Access-Control-Allow-Origin: *`。无需在 Agent 中配置看板的域名或端口。

合法 GET/POST 的 OPTIONS 预检不需要令牌；实际健康、收益、状态和控制请求均必须携带有效 Bloomer Bearer 令牌，缺失或错误仍返回 401。预检显式允许 Authorization、Content-Type 和 Accept，合法预检响应缓存 2 小时（`Access-Control-Max-Age: 7200`），不启用跨域 Cookie 凭证。成功和错误响应都带 CORS 头；无 Origin 的 CLI 调用兼容。

通过安装器升级会启动新版 Agent，家中终端可确认运行状态：

```sh
BASE="$HOME/Library/Application Support/Bloomer/agent"
"$BASE/bin/current/bloomer-agent" -version
"$BASE/management/bloomerctl" health
```

手动替换程序的用户需正常停止并重启 Agent，使新版生效；不需要启停 Darkbloom。升级保留现有数据库、历史与令牌，不需要额外组网、配对或调整 Serve 路由。

## 4. 公司 Mac 启动静态看板

普通静态服务器只需提供 `apps/dashboard/public` 内的 HTML、CSS、JS 和 SVG。无需 `npm start`、Node 后端、第三方前端依赖或构建。公司终端例如执行（有 Python 3 时）：

```sh
cd /Users/sdj_mac_air/Desktop/Repos/Bloomer
python3 -m http.server 18080 --bind 127.0.0.1 --directory apps/dashboard/public
```

保持终端运行，浏览器固定打开 **http://127.0.0.1:18080**。端口被占用时先检查占用来源，也可改用其他本地端口，无需修改家中 Agent。任何等价静态服务器或普通静态托管都可使用。托管内容必须是自己信任的版本，因为该来源的脚本可以访问浏览器保存的连接令牌。无需向静态托管方提交令牌。

**不要双击 `file://` 页面作为正式运行方式**，其来源不稳定且 ES 模块和跨域行为受限。HTTPS 看板也不能请求 HTTP Agent；远程设备使用 Tailscale HTTPS。

## 5. 获取 Bloomer access.token

必须在**运行 Agent 的家中设备端终端**执行，使用**安装和运行 Agent 的同一 macOS 用户**：

```sh
cat "$HOME/Library/Application Support/Bloomer/agent/access.token"
```

若配置了自定义 `access_token_file`，使用实际路径；若自定义 `data_dir` 且未指定令牌文件，默认是该数据目录下的 `access.token`。文件不存在时先确认用户、Agent 安装及配置路径，不要另生成一份令牌。

这是 Bloomer 自己的访问令牌，**不是 `~/.darkbloom/auth_token`**。Darkbloom 官方令牌只由家中 Agent 读取，看板不获取、不保存它。

令牌只需填写到自己的看板，不应提交仓库、粘贴到聊天、放进网页文件、URL、日志或截图。不要运行把令牌直接写进命令参数的示例。该令牌在启用远程操作时也具有设备操作权限。

## 6. 测试并保存

1. 打开「我的设备」，点击设备列表末尾的 **+**。
2. 填写名称（例如“家中 Mac”）、实际 Tailscale HTTPS 地址（含端口）和 Bloomer `access.token`。
3. 点击“测试连接”；浏览器携带 `Authorization: Bearer ...` 直接请求家中 Agent 的 `/v1/health`。
4. 看到成功后点击“保存”。保存时会再次测试，只有测试成功才写入浏览器。采集尚无数据也可连接，页面会说明数据状态。

关闭窗口会清空输入框的令牌。已保存的名称、地址、令牌及所选设备存于该浏览器、该网页来源的 localStorage；刷新或再次打开同一来源自动恢复。移除设备只删除浏览器连接和该连接的待核对请求记录，不停止 Agent、不删除设备历史。

## 7. 浏览器存储与来源隔离

在「我的设备」页设备列表末尾点击 **+** 可添加设备。连接信息仅保存在当前浏览器、当前网页来源的 localStorage 中。

浏览器存储**按来源隔离**：更换浏览器、用户配置、协议、主机名、端口，使用无痕模式或清除网站数据后，需要重新添加。不要认为在 `localhost:18080` 保存的设备会出现在 `127.0.0.1:18080`。Agent 不需要配置新来源。浏览器存储禁用或空间不足会明确报告无法保存。

## 8. 常见问题

浏览器的网络异常可能统一表现为“Failed to fetch”。看板无法可靠区分断网、证书和 CORS，必须逐项核对，不能仅凭该提示确定原因。

| 现象 / 检查项 | 排查方法 |
| --- | --- |
| Tailscale 断开 | 两台 Mac 检查 Tailscale 连接与同一 tailnet，确认可到达家中 `100.101.88.84` |
| 家中设备离线 / 休眠 | 唤醒设备，确认对应用户登录，执行 `bloomerctl status`、`health`；检查家中网络 |
| 地址或端口错误 | 在家中执行 `tailscale serve status`，确认完整 `.ts.net` 域名和实际 HTTPS 端口；不附加 `/v1` |
| 证书问题 | 在公司浏览器直接打开实际 Agent 地址加 `/v1/health`；未带令牌返回 401 是正常的，证书错误应修复域名、时间、证书或 Tailscale 状态，不关闭证书校验 |
| 浏览器仍提示 CORS | 确认运行的是 1.0.7 或更新 Agent；检查反向代理是否保留 OPTIONS 和 `Access-Control-Allow-Origin: *`，不需要填写来源白名单 |
| 令牌错误 / 401 | 在家中使用正确 macOS 用户重读 Bloomer `access.token`，确认不是官方令牌；移除旧连接再添加。不要把令牌贴入排查记录 |
| 旧 Agent / 仍拒绝浏览器 | 升级至 1.0.7 或更新程序并确认已启动；`health` 中核对运行版本 |
| 能连接但没有收益 | 查看采集健康、首次采集、官方凭证、限流、历史覆盖与过期状态；不要当成零收益或改用演示数据 |

只读连通性验证足以排查连接；无需通过启停 Darkbloom 或下载模型测试网络。真实控制操作必须确认目标与动作，失败后先核对后台任务和设备实际状态，再用同一请求编号安全重试。
