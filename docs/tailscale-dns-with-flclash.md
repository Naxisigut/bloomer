# macOS 下 FlClash TUN 与 Tailscale 的 DNS 共存配置及撤销

整理日期：2026-10-18

## 适用场景

访问 Bloomer 的 Mac 同时运行 Tailscale 和 FlClash，并开启 FlClash 的 TUN 模式。启用 Tailscale 的“使用 Tailscale DNS”后，现有代理受到影响，因此保持该选项关闭，但仍需要通过完整的 `.ts.net` 域名访问 Bloomer 的私网 HTTPS 接口。

本方法在**访问 Bloomer 的客户端 Mac** 上操作。每台有相同需求的客户端分别配置；提供 Bloomer 服务的设备无需因此重新安装或修改 Serve。

## 原理与前提

为自己的 tailnet 域名添加一个 macOS 专用解析器：

- 该域名及其子域名的系统 DNS 查询交给 `100.100.100.100`，即 Tailscale 的本机 DNS 服务。
- 其他域名继续使用现有 DNS 配置，FlClash 的 TUN 保持开启。
- Tailscale 客户端继续关闭“使用 Tailscale DNS”；tailnet 管理后台的 MagicDNS 仍需启用。

macOS 根据 `/etc/resolver/` 下的文件名匹配域名，选择匹配最具体的解析器。配置文件会保留到重启后；使用时仍需 Tailscale 已连接。这条解析规则不能替代 Bloomer 服务启动、Serve 配置或 HTTPS 授权。

本次排查已确认：直接向 `100.100.100.100` 查询能够获得目标设备的 Tailscale IP，且临时指定域名到 IP 的映射后，跨设备 HTTPS 请求和证书校验均通过。**专用解析器配置本身尚未在本次排查中执行，应用后应按下文验证。**

## 1. 填写自己的域名

在客户端 Mac 的终端中填写以下变量，将示例值替换为实际值：

```sh
BLOOMER_TAILNET_DOMAIN='tail-example.ts.net'
BLOOMER_HTTPS_HOST='device.tail-example.ts.net'
BLOOMER_HTTPS_PORT='8443'
BLOOMER_RESOLVER_FILE="/etc/resolver/$BLOOMER_TAILNET_DOMAIN"
```

完整 HTTPS 地址可以从服务设备的安装输出或 `~/Library/Application Support/Bloomer/agent/install-state.json` 的 `endpoint` 字段获取。例如 `https://device.tail-example.ts.net:8443` 对应上述主机名和端口；tailnet 域名是 `tail-example.ts.net`，不包含设备名、协议或端口。

后续步骤在同一个终端窗口中执行。新开窗口时需要重新填写变量。

## 2. 确认 Tailscale DNS 可以直接查询

保持 FlClash TUN 开启，执行：

```sh
dig @100.100.100.100 "$BLOOMER_HTTPS_HOST" A +time=2 +tries=1 +short
```

预期返回目标设备的 Tailscale IPv4 地址，通常是 `100.x.x.x`。如果超时、返回空值或返回错误地址，应先检查 Tailscale 连接和 FlClash 对该 DNS 地址的访问；专用解析器不能修复上游不可达的问题。

这里使用 `dig @100.100.100.100` 仅验证指定 DNS 服务。macOS 的普通 `dig`、`host`、`nslookup` 不一定使用系统的域名专用解析器，不能据此判断下一步配置是否生效。

## 3. 创建域名专用解析器

先检查同名文件：

```sh
if [ -e "$BLOOMER_RESOLVER_FILE" ]; then
  cat "$BLOOMER_RESOLVER_FILE"
else
  echo '该域名尚未配置专用解析器'
fi
```

以下创建和删除步骤适用于**原先不存在该文件**的情况。若文件已经存在，应先保留原内容，再合并所需配置；撤销时恢复原内容。

确认文件原先不存在后，逐条执行：

```sh
sudo mkdir -p /etc/resolver
printf 'nameserver 100.100.100.100\n' | sudo tee "$BLOOMER_RESOLVER_FILE" >/dev/null
sudo dscacheutil -flushcache
sudo killall -HUP mDNSResponder
```

`sudo` 会要求输入当前 Mac 的管理员密码，输入时不显示字符。最后两条命令刷新系统 DNS 缓存并通知解析服务重载。

## 4. 验证域名、HTTPS 和原有代理

先验证系统解析：

```sh
dscacheutil -q host -a name "$BLOOMER_HTTPS_HOST"
```

预期能看到目标设备的 Tailscale IP。需要查看解析器注册情况时运行 `scutil --dns`，查找自己的 tailnet 域名及对应的 `100.100.100.100`。

再通过正常域名请求健康接口：

```sh
curl --noproxy '*' --connect-timeout 5 --max-time 10 \
  -sS -o /dev/null -w 'HTTP %{http_code}\n' \
  "https://${BLOOMER_HTTPS_HOST}:${BLOOMER_HTTPS_PORT}/v1/health"
```

**预期返回 `HTTP 401`。** 此请求没有携带令牌，`401` 是 Bloomer 的正常鉴权拒绝响应，说明 DNS、网络和 HTTPS 已连通。该命令使用正常证书校验，不需要 `-k`，也不使用 `--resolve`。

`--noproxy '*'` 绕过 curl 的显式 HTTP/SOCKS 代理设置，FlClash 的 TUN 仍保持工作。随后再访问一个平时通过代理使用的网站，确认原有代理行为正常。

如果 `100.100.100.100` 直查成功，但系统解析或浏览器仍失败，需要进一步检查 FlClash 的 DNS 劫持、域名分流或浏览器自己的安全 DNS 设置。修改代理规则时，分别核对该 tailnet 域名的 DNS 查询和 Tailscale 目标 IP 的路由。

## 5. 撤销配置

若此文件是本方法新建的，在配置过的同一台 Mac 上重新填写第 1 步的变量，然后执行：

```sh
sudo rm "$BLOOMER_RESOLVER_FILE"
sudo dscacheutil -flushcache
sudo killall -HUP mDNSResponder
```

如果操作前已存在同名文件，应恢复备份的原内容，再执行上述两条缓存刷新命令，而不是删除文件。

撤销后运行 `scutil --dns`，确认本方法添加的域名解析器已经消失。原有 FlClash 设置和 Tailscale 的 DNS 接受选项保持原状。若仍关闭“使用 Tailscale DNS”，且没有其他解析配置，`.ts.net` 域名可能再次无法解析，这是恢复到原配置后的预期结果。

本操作只撤销客户端的域名解析规则，不会关闭服务设备的 Tailscale Serve 路由，也不会删除 Bloomer 数据。

## 参考资料

- [Tailscale：关闭 DNS 接受后仍可通过 100.100.100.100 查询](https://tailscale.com/docs/reference/faq/dns-resolv-conf)
- [Tailscale：MagicDNS 与 macOS 查询工具的差异](https://tailscale.com/docs/features/magicdns)
- [Apple：resolver(5)，域名专用解析器与匹配策略](https://github.com/apple-oss-distributions/libresolv/blob/main/resolver.5)
