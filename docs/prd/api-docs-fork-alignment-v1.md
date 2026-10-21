# Fork API 文档同步工作 PRD

日期：2026-10-21。仓库：`D:/kylin/rainbow_bridge`；核对基线：`main_v1` 的 `cd673dc`。

## 1. 目标与终点

为二次开发后的虹桥编写面向研发的 API 文档，覆盖业务读取、生效版本、固定版本、静态包、管理会话、锁版和发布生命周期。仅处理文档，不依赖应用前端重构。

本批次交付 PRD 和专业文档源文件；不修改首页。首页面向领导展示三种能力，具体参数、响应和异常放在专业文档。不得为补文档顺带重构后端或改变接口。

## 2. 工作位置和保护范围

- 修改范围：`docs/docs/api/*.md`、本 PRD。
- Fork：`https://github.com/MalfuRisa/rainbow_bridge`；远程目标分支为 `main_v1`。
- 专业文档入口：`https://github.com/MalfuRisa/rainbow_bridge/blob/main_v1/docs/docs/api/index.md`。
- 不改 `vue/`、`biz/`、`idl/`、生成代码、运行实例、数据库、启动和发布工作流。当前另一窗口的 Vue 未提交改动必须保留，不得混入暂存区。
- 如后续独立开发需要分支，使用 `codex/api-docs-alignment`，从已提交基线建立；本轮共用工作区写 Markdown 不切换分支。
- 本轮不自动提交、推送或部署。后续获授权后，只显式提交本任务文件，推送 Fork，不向原上游提 PR。

## 3. 现状与证据

| 当前问题 | 本批次解决方式 |
| --- | --- |
| 首页链接指向原作者文档站 | 确定 Fork 专业入口，前端后续独立替换链接 |
| 文档说尚未强制认证 | 补 Cookie、可信 Origin、CSRF 和公开读取范围 |
| 所有响应都写 code=0 | 区分 auth 的 0、运行时/发布的 200 和历史 HTTP 200 业务错误 |
| 运行时响应仍是旧结构 | 按实际 handler 重写 configs、版本和快照元数据 |
| 锁版、发布切换、逻辑删除缺文档 | 补请求、响应结构和调用顺序 |
| 静态包仍写 index.html | 改为实际 config.json、manifest.json、assets 路径及用途 |
| 文档站 repo/branch 仍指向上游，发布只监听 main | 本轮采用 GitHub Markdown 入口，不声称 main_v1 更新已发布网站 |

真实合同依据：`biz/handler/{auth,freeze,release,runtime}/` 的实际 HTTP 输出、`biz/middleware/admin_session.go`、相关 service 和契约测试。IDL 适合核对路由和输入，但不是已生成的 OpenAPI，也不能覆盖手写 JSON 的实际语义。

## 4. 职责划分

| 文件 | 职责 |
| --- | --- |
| `docs/docs/api/index.md` | 专业入口、地址/范围规则、响应兼容说明和路由索引 |
| `docs/docs/api/runtime.md` | 默认/固定读取、raw、静态导出、旧接口迁移、错误及缓存 |
| `docs/docs/api/management.md` | 会话、编辑区结构、锁版、发布切换、删除、幂等和冲突处理 |

研发应阅读 `$engineering-quality-preserving-refactor`（`C:/Users/MalfuRisa/.codex/skills/engineering-quality-preserving-refactor/SKILL.md`）。本次适用职责拆分、复用事实来源、保持行为；不引入 API 服务、SDK、新依赖或算法优化。

## 5. 必须写清的合同

### 业务读取

- `/runtime/release` 的环境和渠道来自请求头；没有 version 跟随 active，显式空 version 返回 400。版本号是范围内的配置发布版本，不是程序构建版本。
- `content_format` 缺省 json，可选 raw；raw 全部 content 为字符串，但再次普通 JSON.parse 仍可能丢失大整数精度。
- 描述真实响应、十进制字符串 revision、RFC3339 时间、空 configs=[]，fixed 模式不含 active_revision/activated_at。
- `/runtime/release/static` 必填 version、expected_snapshot_id；ID 来自读取响应，防止导出与展示不同快照。
- ZIP 是 config.json、manifest.json、assets/<asset_id>.bin；不是 transfer 导入包、网页或完整备份。
- 旧 `/runtime/config` 同样读取 active，但不接受 version/raw；旧 `/runtime/static` 范围来自查询参数，导出请求解析到的 active。
- 未激活 404、已删除 410、快照不完整 503，不回退编辑区；说明 no-store，不用五分钟缓存掩盖版本切换。

### 管理调用

- 管理会话与公开业务读取分开；登录必须可信 Origin，Cookie 由浏览器接收，写请求附 CSRF，不再每次输入密钥。
- 说明 auth 成功 code=0、发布/运行时成功 code=200，以及部分历史 CRUD 需要同时检查 HTTP 和正文 code。
- 配置创建/修改请求为 `{ "config": {...} }`，环境/渠道遵循各自嵌套结构，不能复制旧文档的扁平示例。
- 锁版前读 revision；创建版本必须已锁版且 revision 匹配；创建不会激活。
- 锁版、解锁、创建、切换、删除的 reason 均选填，trim 后最多 500 个 Unicode 字符。
- 仅非生效版本可逻辑删除；版本号保留、文件不回收、无恢复接口，删除后的新读取 410。
- 创建/切换/删除使用 UUID request_id；不确定结果时原样重发，改内容则新 ID。冻结接口没有这一套 request_id 合同。
- 资源跨范围、缺文件、revision 冲突都说明实际错误码，不通过去掉检查规避。

## 6. 执行顺序与验收

1. 核对代码和工作区，保留前端改动；重写入口和业务读取，再补管理专题。
2. 核对示例 JSON、链接、IDL 路由索引、请求嵌套和错误码；复用已有 HTTP 契约测试。
3. 在本 PRD 登记实际结果；不为中间对齐另写桌面报告。
4. 后续获得推送授权后提交指定文件并核对 Fork 远程内容。若单独发布文档站，再处理 VuePress 的 repo/docsRepo/docsBranch/docsDir、main_v1 发布触发与 Pages 权限，不先使用未经验证的新站点地址。

| 验收 | 具体预期 |
| --- | --- |
| D01 范围 | 本任务只改指定 Markdown，不暂存其他窗口文件 |
| D02 读取 | 区分 active/fixed、空 version=400、未生效=404、删除=410 |
| D03 导出 | ID 来源清楚，错 ID=409，ZIP 结构符合实际测试 |
| D04 精度 | raw/json 字段类型、空数组、active 专属字段说明正确 |
| D05 鉴权 | 合法 Origin 登录；管理无会话 401、缺 CSRF 403；业务读取公开 |
| D06 管理 | 请求嵌套、revision/request_id、原因选填、删除规则符合实现 |
| D07 兼容 | 旧 runtime 不读编辑区；旧 static 范围是查询参数 |
| D08 验证 | 示例 JSON 可解析，相对链接存在，路由导航覆盖 IDL，相关测试通过 |
| D09 状态 | 区分本地完成、提交、推送、文档站发布，不混写 |

建议检查命令，Windows TEMP 使用可正常重命名的短路径：

```powershell
go test -mod=readonly -tags=dev -count=1 ./biz/handler/auth ./biz/handler/release ./biz/router
go test -mod=readonly -tags=dev -count=1 ./biz/service -run 'Test(ActiveRuntimeExport|ReleaseStaticExport|ReleaseActiveReadDuringSwitchKeepsOldSnapshot|ReleaseSnapshotPayloadAndFixedRuntime|ReleaseRuntimeIntegrityFailures|ReleaseDeleteInFlightReadAndExport)'
```

无需启动或更换 18080/18081，无需构建应用前端。Markdown 检查通过不等于 VuePress 整站已构建或部署。

## 7. 前端后续交接

首页仅展示读取生效配置、读取指定版本、导出版本资源包三项能力。技术参数放在完整文档；首页“查看完整 API”链接替换为 §2 的 Fork 地址。前端重构完成后独立改这个链接，不阻塞本批次。

## 8. 本轮交付登记

本地文档已完成：重写 API 入口，新增运行时、管理接口两篇专题。后端实现及首页均未修改，前端窗口的改动保留在工作区。

本轮实际检查：

- `go test -mod=readonly -tags=dev -count=1 -timeout 120s ./biz/handler/auth ./biz/handler/release ./biz/router` 通过，涵盖认证/发布 HTTP 契约与路由保护。
- §6 的 service 专项命令（附 `-timeout 120s`）通过，覆盖固定读取、完整性、切换期间读取/导出、删除及导出超时等相关现有用例。
- 使用本机 Node 临时检查文档：8 个 json 围栏示例均可解析，20 处相对链接目标存在，路由索引按方法及规范化路径与当前 IDL 的 47 条声明完全匹配。
- 已检查文档 diff 和格式；没有启动/更换实例，没有执行应用前端构建。

这些是本地文档和既有后端契约的验证，不是新的生产接入验证。VuePress 整站构建、Pages 发布以及远程新文档可见性本轮未验证。未派发其他聊天，未提交、未推送、未部署文档站；Fork 远程在推送前仍是旧文档。首页链接替换交给前端后续独立处理。

### 8.1 独立复核结果（2026-10-21，第二会话按 §6 重跑）

复核原则：不采信上文声称，全部按 §6 命令与自写脚本重跑核对；复核对脚本为工作区已忽略的 `.smoke-migrate/verify-docs.mjs`（可重复执行）。本轮只读核对，未修改 `docs/docs/api/` 三份文档，只写本节登记。

| 核对项 | 方法 | 结果 |
| --- | --- | --- |
| 路由索引覆盖 IDL | 脚本提取 `idl/biz/*.proto` 的 `option (api.*)` 声明与 index.md 索引表格，按方法与规范化路径比对 | IDL 47 条、文档 47 条，集合一致；`/ping` 在正文单独说明 |
| 文档路由真实注册 | 脚本按 Group 前缀解析 `biz/router/**` 的注册调用，与 IDL 比对 | 47 条 IDL 路由全部已注册（另有 `/ping`，共 48）；`release/asset/*filepath`、`asset/file/*filepath` 正对应文档的 `{snapshot_id}/{asset_id}`、`{file_id}/{file_name}` 写法，通配写法差异不是缺失 |
| JSON 围栏 | 逐段 `JSON.parse` | 8 段全部可解析，0 失败 |
| 相对链接 | 逐个解析为绝对路径并检查文件存在 | 20 处全部存在 |
| 错误码 | 提取文档声明的 36 个 `*_ERROR/_NOT_FOUND/...` 常量，在 `biz/`、`pkg/` 非测试源码中查找 | 36/36 有实现依据，没有编造的错误码 |
| 字段与响应头 | 21 个名称（`reauth_required`、`resource_import`、`resource_issues`、`blocked_scopes`、`X-Rainbow-*` 等）在源码中查找 | 21/21 有实现依据 |
| 抽样语义核对（只读代码） | 针对易写错的合同逐条查实现 | auth 成功 code=0（`auth_service.go:199/216/240/271`）；导出 192 MiB（`release_export.go:35`）与 60 秒总期限（`:37`）；MinIO 源创建 503 `RELEASE_STORAGE_UNSUPPORTED`（`release_errors.go:141`）；版本名正则（`release_service.go:23`）；`release/active` 字段无 `omitempty`，未设置返回 `active:null`（`release_service.go:530-532`、`:593`）；fixed 模式经 `omitempty` 省略 `active_revision`/`activated_at`（`:627`、`:633`）；会话缺省空闲 2h／绝对 8h（`auth_session.go:291-292`）；auth 请求体上限 4 KiB 并拒绝未知字段（`auth_service.go:27`、`:140`）；版本列表缺省 20／上限 100（`release_types.go:7`、`ReleaseVersions`）；旧 runtime 拒绝 `version/expected_snapshot_id/content_format`（`runtime_active_adapter.go:15`）；选择性导出为 `POST /api/v1/transfer/export` + `selections`（`transfer.proto:172`、`handler/transfer/transfer_service.go:225/239`） |
| 契约测试 | §6 两条命令（附 `-timeout 120s`） | `biz/handler/auth`、`biz/handler/release`、`biz/router` 全部 ok；`biz/service` 指定 6 个用例 ok（12.0s） |

复核结论：文档与 `cd673dc` 实现一致，无需修改三份 Markdown。状态边界与上文一致：未做 VuePress 构建、Pages 发布、提交或推送；工作区其他窗口的 Vue 改动与未跟踪样式文件保持原样，未暂存、未改动。`docs/docs/api/*.md` 与 `docs/prd/api-docs-fork-alignment-v1.md` 仍为未提交状态。
