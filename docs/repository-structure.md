# Bloomer 仓库结构

更新日期：2026-10-16

## 1. 仓库组织

Bloomer 采用轻量 monorepo：在根目录维护一个 Git 仓库，包含设备端和看板端两个独立项目。两端可以使用不同的语言和构建工具，分别管理依赖、测试、构建及版本发布。

设备端功能与验收要求见[设备端开发目标](agent-development-goals.md)，具体实施见[设备端开发方案与实施计划](agent-development-plan.md)。看板的技术栈和产品形态尚未最终确定，后续单独维护其开发目标文档。

## 2. 目标目录

```text
Bloomer/
├── .git/                              # 根目录唯一的 Git 仓库
├── apps/
│   ├── agent/                         # Go 设备服务，独立 go.mod
│   └── dashboard/                     # 看板项目，依最终选型放置构建配置
├── contracts/
│   └── openapi.yaml                   # 两端共享接口契约
├── docs/
│   ├── agent-development-goals.md      # 设备端开发目标
│   ├── agent-development-plan.md       # 设备端开发方案与实施计划
│   └── repository-structure.md         # 仓库组织与目录约定
├── scripts/                           # 构建、安装、发布辅助脚本
├── README.md                          # 项目入口与开发说明
└── .gitignore                         # 生成文件与私有数据排除规则
```

以上是目标结构。P0/P1 已创建 `apps/agent`、`contracts/openapi.yaml` 草案、验证脚本、项目入口和排除规则；`apps/dashboard` 尚未创建，后续按实际选型实施。

## 3. 目录职责

| 目录 | 职责 |
| --- | --- |
| `apps/agent` | 设备端采集、数据库、查询接口、恢复与清理逻辑，以及对应测试和构建配置 |
| `apps/dashboard` | 看板界面、设备连接、数据展示及对应测试和构建配置 |
| `contracts` | 双方遵循的接口版本、请求、响应和错误结构 |
| `docs` | 各项目开发目标、架构决策和使用说明 |
| `scripts` | 仓库层面的构建、安装、发布等辅助脚本 |

## 4. 独立开发与共享边界

- 修改设备端不要求同步构建或部署看板，反之亦然。
- 两端通过接口契约协作，避免看板依赖设备端内部实现。
- 设备端使用独立 `go.mod`；如果看板采用 Web/TypeScript，则在其目录使用独立 `package.json`。
- 接口变更同步更新 `contracts/openapi.yaml`，并考虑已发布客户端的兼容性。
- 初期无须额外引入复杂的 monorepo 编排工具；仅有一个 Go 项目时也无须为目录组织增加 `go.work`。

## 5. Git 与运行数据约定

- Git 仓库仅在 Bloomer 根目录初始化，不在两个项目目录内分别创建嵌套仓库。
- 数据库、WAL、日志、令牌和设备私有配置不提交到 Git。
- 设备运行数据保存在仓库之外的用户应用数据目录，升级代码不应覆盖运行数据。
- 后续创建 `.gitignore` 时，覆盖依赖目录、构建产物、运行文件和私有配置；公开的配置示例可以提交。
