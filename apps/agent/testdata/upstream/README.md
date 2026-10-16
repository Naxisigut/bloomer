# P0 接口样本

所有文件均不含真实凭证、账户标识或收益。结构依据与观测摘要见仓库 `docs/upstream-contract.md`。

| 文件 | 来源与用途 |
| --- | --- |
| `observed-redacted.json` | 目标设备 0.9.22 接口结构；身份、金额、Token 和时间全部替换，数组截为两条并调整计数 |
| `normal.json` | 合成正常数据，含超出 JavaScript 安全整数范围的 ID、文本 ID、负调整、零 Token 和时区偏移 |
| `duplicate-identical.json` | 合成同 ID、相同内容重复，解码折叠 |
| `duplicate-conflict.json` | 合成同 ID、金额冲突，整份快照失败 |
| `missing-amount.json` | 合成缺失必填金额；不能按 0 入账 |
| `overflow.json` | 合成超出 int64 的金额 |
| `malformed.json` | 故意无效的 JSON，用于解码失败测试 |
| `unauthorized.json` | 合成错误文本，结构与实测 401 一致；代码和消息不声称来自真实服务 |
| `rate-limited.json` | 合成 429 响应形状，未声称真实触发限流 |
