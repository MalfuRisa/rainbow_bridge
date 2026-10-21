# Runtime Config 上手指南

## 范围（本次分析）

本轮先回答“虹桥是什么、一个业务应用怎样按环境和渠道拿到配置”，并追踪首页“运行时配置”从 Vue 页面到数据库的完整链路。

后续轮次暂不展开：配置增删改、环境/渠道管理、资源上传与 MinIO、本地静态包导出、导入导出/迁移、K8s 实际资源核验。

## 一句话认识虹桥

虹桥是一个自部署的配置与静态资源中台。运营或研发人员在管理页面里维护配置，调用方通过“环境 + 渠道”选择一套配置；配置可以实时走 API 获取，也可以连同图片等资源导出成静态包。项目定位和能力列表见 `D:/kylin/rainbow_bridge/README.md:31` 的“项目概述”。

可以把两个核心维度理解为：

- 环境（Environment）：开发、测试、预发、生产等隔离空间。
- 渠道（Pipeline）：同一环境里的不同版本线、客户线或实验分支。

## 仓库与技术栈

- 这是一个前后端同仓库项目；Go 服务入口是 `D:/kylin/rainbow_bridge/main.go:29 main`，Vue 应用位于 `vue/`。
- 后端是 Go + CloudWeGo Hertz + GORM，数据库适配 SQLite、MySQL 和 PostgreSQL；驱动选择见 `D:/kylin/rainbow_bridge/pkg/database/database.go:21 Open`。
- 前端是 Vue 3 + Vite + Pinia + Tailwind，依赖声明见 `D:/kylin/rainbow_bridge/vue/package.json`。
- 生产模式可由同一个 Go 进程同时提供 API 和打包后的前端静态文件；SPA 静态文件处理见 `D:/kylin/rainbow_bridge/main.go:78 setupStaticFileServer`。
- 服务启动时加载配置、连数据库、自动建表、补默认环境/渠道、可选连接 Redis，最后注册中间件和路由，见 `D:/kylin/rainbow_bridge/initialize.go:44 Initialize`。

## 请求链路（前端 → 后端 → 存储）

| 环节 | 层级 | 证据（文件:行 + 符号） | 大白话说明 |
| --- | --- | --- | --- |
| 首页入口 | 前端展示 | `D:/kylin/rainbow_bridge/vue/src/views/HomePage.vue:5 RuntimeConfig` | 首页把“运行时配置”组件放到项目概览页。 |
| 环境/渠道选择 | 前端展示 | `D:/kylin/rainbow_bridge/vue/src/components/RuntimeConfig.vue:44 fetchConfigs` | 用户选好环境和渠道后，页面请求并展示这一组配置。 |
| API 函数 | 接口传输 | `D:/kylin/rainbow_bridge/vue/src/lib/api/runtime.ts:20 runtimeApi.config` | 发出 `GET /api/v1/runtime/config`，把环境、渠道分别放进 `x-environment`、`x-pipeline` 请求头。 |
| URL 拼装 | 接口传输 | `D:/kylin/rainbow_bridge/vue/src/lib/api/client.ts:118 ApiClient.getUrl` | 生产环境把 `/rainbow-bridge` 之类的 base path 加到 API 前面；开发环境由 Vite 代理到后端 8080 端口。 |
| 开发代理 | 基础设施 | `D:/kylin/rainbow_bridge/vue/vite.config.ts:21 server.proxy` | 本地 3000 端口收到 `/rainbow-bridge/api` 后转发到 `127.0.0.1:8080`。 |
| 后端路由 | 接口传输 | `D:/kylin/rainbow_bridge/biz/router/runtime/runtime.go:17 Register` | Hertz 将 `/api/v1/runtime/config` 交给 `runtime.GetConfig`。这个文件是生成代码，不应手改。 |
| Handler | 业务逻辑 | `D:/kylin/rainbow_bridge/biz/handler/runtime/runtime_service.go:26 GetConfig` | 读取并校验两个请求头，然后调用 Service；HTTP 外层通常仍返回 200，业务错误写在响应体的 `code/error` 中。 |
| Service | 业务逻辑 | `D:/kylin/rainbow_bridge/biz/service/runtime_service.go:67 GetRuntimeConfig` | 先确认环境和渠道确实存在，再查配置，处理资源 URL，最后组装返回值。 |
| 缓存 | 数据访问 | `D:/kylin/rainbow_bridge/biz/service/logic_config.go:206 ListConfigs` | Redis 可用时先读缓存；未命中再查数据库，并缓存 30 分钟。Redis 失败不会阻断主流程。 |
| DAO 查询 | 数据访问 | `D:/kylin/rainbow_bridge/biz/dal/db/config_dao.go:115 ListByEnvironmentAndPipelineWithFilter` | GORM 按 `environment_key + pipeline_key` 查 `resource_config` 表，并按更新时间倒序返回。 |
| 数据模型 | 数据访问 | `D:/kylin/rainbow_bridge/biz/dal/model/config.go:10 Config` | 一条配置包含环境、渠道、稳定资源 ID、别名、名称、内容、类型、备注等字段。 |
| 数据库选择 | 基础设施 | `D:/kylin/rainbow_bridge/pkg/database/database.go:21 Open` | 部署时可在 SQLite、MySQL、PostgreSQL 中选一个作为持久化数据库。 |

把它串成一句话：页面选择“prod + main” → 浏览器带两个 Header 请求 API → 后端确认 prod 环境和 main 渠道存在 → 先看 Redis 缓存 → 没有就去关系型数据库查 → 把配置和资源地址整理好后返回。

## K8s 与当前访问地址

- 仓库中较完整的 PostgreSQL + MinIO 示例把应用部署成 1 个副本，容器监听 8080，挂载配置和数据卷，见 `D:/kylin/rainbow_bridge/deploy/kubernetes/pgsql-minio/rainbow-bridge-deployment.yaml:1`。
- 该示例使用 PostgreSQL 保存配置、MinIO 保存文件，Redis 默认关闭，base path 为 `/rainbow-bridge`，见 `D:/kylin/rainbow_bridge/deploy/kubernetes/pgsql-minio/rainbow-bridge-configmap.yaml:7`。
- 仓库内的 Service 是 `ClusterIP`，见 `D:/kylin/rainbow_bridge/deploy/kubernetes/pgsql-minio/rainbow-bridge-service.yaml:7`。用户提供的 `10.60.226.43:31880` 很像额外配置的 NodePort 或外围转发，但这只是根据端口形式做的推断，仓库文件不能证明线上实际拓扑。
- `standalone` 示例疑似落后于当前代码：其 ConfigMap 使用 `server.port/host`、`database.type/path`，而当前配置结构要求 `server.address`、`database.driver` 等字段；其探针请求 `/api/ping`，当前路由实际是 `/ping`。证据分别见 `D:/kylin/rainbow_bridge/deploy/kubernetes/standalone/rainbow-bridge-configmap.yaml:7`、`D:/kylin/rainbow_bridge/pkg/config/config.go:81 ServerConfig`、`D:/kylin/rainbow_bridge/deploy/kubernetes/standalone/rainbow-bridge-deployment.yaml:38` 和 `D:/kylin/rainbow_bridge/biz/router/register.go:50 GeneratedRegister`。不要直接把这套旧示例当作线上真实配置。

## 关键配置

- URL 前缀：`D:/kylin/rainbow_bridge/main.go:40 main`，优先级为配置文件 `server.base_path` > `BASE_PATH` 环境变量 > 编译参数。
- 默认监听：`D:/kylin/rainbow_bridge/pkg/config/config.go:145 defaultConfig`，默认 `:8080`、SQLite `data/resource.db`。
- 当前仓库根配置：`D:/kylin/rainbow_bridge/config.yaml:1`，配置为 `/rainbow-bridge`、PostgreSQL、Redis 和 MinIO；它不等于线上 Pod 一定使用的配置。
- 鉴权现状：`D:/kylin/rainbow_bridge/biz/middleware/auth.go:19 Auth` 只在请求带 JWT 时补充用户信息，并不强制登录；公开范围是否符合生产要求需要单独做安全核验。

## 待确认

- [ ] 当前环境无法直连 `http://10.60.226.43:31880/rainbow-bridge/`：直连超时，经代理返回 502，因此没有确认线上页面、健康状态和版本号。
- [ ] 当前机器没有 `kubectl`，无法核对线上 Namespace、Deployment、Service/NodePort、Ingress、Pod 状态和实际 ConfigMap。
- [ ] 线上究竟使用 SQLite，还是 PostgreSQL + MinIO，以及镜像对应哪个 Git commit，仓库本身无法证明。

## 下一步阅读

1. `D:/kylin/rainbow_bridge/vue/src/components/RuntimeConfig.vue` — 先看用户在页面上如何选择环境、渠道并触发请求。
2. `D:/kylin/rainbow_bridge/vue/src/lib/api/runtime.ts` — 看页面请求与 REST 接口怎样对应。
3. `D:/kylin/rainbow_bridge/biz/handler/runtime/runtime_service.go` — 看后端入口如何校验参数和组织响应。
4. `D:/kylin/rainbow_bridge/biz/service/runtime_service.go` — 看这一功能真正的业务步骤。
5. `D:/kylin/rainbow_bridge/biz/dal/db/config_dao.go` — 最后看配置如何落到数据库查询。
