# Rainbow Bridge API（main_v1）

本目录描述 MalfuRisa Fork 的二次开发接口，面向接入研发。2026-10-21 按 `main_v1` 提交 `cd673dc` 的后端实现核对；后续接口改动应同步维护本目录。

虹桥提供读取生效配置、读取指定版本、导出版本配置与资源三种业务能力；管理员还可以维护编辑区、锁版、创建版本、切换生效和删除非生效版本。

## 阅读入口

- [业务系统接入：运行时读取与静态包](runtime.md)
- [管理员接入：登录、编辑、锁版与发布](management.md)
- [本 Fork 源码](https://github.com/MalfuRisa/rainbow_bridge/tree/main_v1)

本入口不依赖应用前端重构。原作者文档站不代表本分支的接口合同。

## 快速接入：读取默认或指定版本

业务应用通常只需要以下三项接口，均无需管理员登录：

| 能力 | 方法与路径 | 作用 |
| --- | --- | --- |
| 读取生效配置 | GET `/api/v1/runtime/release` | 跟随当前环境、渠道的生效版本 |
| 读取指定版本 | GET `/api/v1/runtime/release?version=v1.0` | 固定读取该范围的 v1.0，后续切换不改变读取目标 |
| 导出版本资源包 | GET `/api/v1/runtime/release/static?version=v1.0&expected_snapshot_id=<UUID>` | 下载与已读取快照对应的配置、资源 ZIP |

例如读取默认范围的生效版本（Bash；Windows 可使用 curl.exe 并调整换行）：

```bash
curl --fail-with-body 'http://127.0.0.1:18080/rainbow-bridge/api/v1/runtime/release' \
  -H 'x-environment: default' \
  -H 'x-pipeline: default'
```

成功响应的 `data.configs` 是配置列表，`data.version` 是配置发布版本号，`data.snapshot_id` 是快照 ID。固定导出时传入同一次读取返回的 version 与 snapshot_id。没有生效版本返回 404；不会自动读取编辑区。完整响应示例、raw 精度模式、错误码和旧接口兼容规则见 [运行时文档](runtime.md)。

## 地址与范围

示例部署地址为 `http://127.0.0.1:18080/rainbow-bridge`。本文 `/api/v1/...` 路径需要在前面拼接部署地址，**不要重复添加 `/api/v1` 或 `/rainbow-bridge`**。实际域名、端口、协议与 BasePath 由部署决定；根路径部署没有 `/rainbow-bridge` 前缀。

业务运行时按 `x-environment`、`x-pipeline` 两个请求头选择范围；旧静态导出使用查询参数；管理接口一般使用查询或 JSON 字段，按各接口说明调用。

配置 `version` 是环境/渠道内人为指定的发布版本名，`snapshot_id` 是快照的唯一标识。`GET /api/v1/version` 返回虹桥程序的构建版本，与配置发布版本不同。

## 响应兼容约定

不要假定所有模块的成功 code 相同。

| 模块 | 当前成功响应 |
| --- | --- |
| auth | HTTP 200，正文 `code: 0`、`msg: "success"` |
| freeze、release、runtime/config、runtime/release | HTTP 200，正文 `code: 200`、`msg: "OK"` |
| 旧环境/渠道/配置等 CRUD | 通常正文 `code: 200`；部分错误仍通过 HTTP 200 返回，需检查正文 code |
| 静态包与下载 | 成功返回 ZIP 或文件字节，不统一使用 response.json() |

运行时/发布/冻结错误使用实际 HTTP 状态和机器可读 `error_type`；认证错误主要说明放在 `msg`，发布错误可能同时含 `error`。不保证所有历史接口及空字段都有相同结构，详情见专题。

管理接口需要会话，管理写请求需要 CSRF；公开业务读取不需要管理员 Cookie。旧 Bearer 不再授予管理员权限。客户端同时检查 HTTP 状态、正文 code 及所需字段，不只检查 HTTP 200。

## 业务路由索引

以下覆盖当前 `idl/biz/` 声明的路由，均拼接部署前缀；细节以专题、实际 handler 和对应 IDL 为准。表格列的是导航，不代表各模块字段与错误协议相同。

| 分类 | 方法与路径 | 权限及依据 |
| --- | --- | --- |
| 认证 | POST `/api/v1/auth/login`、`/api/v1/auth/logout`、`/api/v1/auth/password`；GET `/api/v1/auth/me` | 自身会话规则；[管理文档](management.md) |
| 环境 | GET `/api/v1/environment/list`、`/api/v1/environment/detail`；POST `/api/v1/environment/create`、`/api/v1/environment/update`、`/api/v1/environment/delete` | 管理会话；[environment.proto](../../../idl/biz/environment.proto) |
| 渠道 | GET `/api/v1/pipeline/list`、`/api/v1/pipeline/detail`；POST `/api/v1/pipeline/create`、`/api/v1/pipeline/update`、`/api/v1/pipeline/delete` | 管理会话；[pipeline.proto](../../../idl/biz/pipeline.proto) |
| 编辑配置 | GET `/api/v1/config/list`、`/api/v1/config/detail`；POST `/api/v1/config/create`、`/api/v1/config/update`、`/api/v1/config/delete` | 管理会话；[config.proto](../../../idl/biz/config.proto) |
| 原始资源 | GET `/api/v1/asset/list`；POST `/api/v1/asset/upload`；GET `/api/v1/asset/file/{file_id}/{file_name}` | 列表/上传需会话，下载公开；[asset.proto](../../../idl/biz/asset.proto) |
| 锁版 | GET `/api/v1/freeze/status`、`/api/v1/freeze/history`；POST `/api/v1/freeze/lock`、`/api/v1/freeze/unlock` | 管理会话；[管理文档](management.md) |
| 发布 | GET `/api/v1/release/list`、`/api/v1/release/detail`、`/api/v1/release/active`、`/api/v1/release/history`；POST `/api/v1/release/create`、`/api/v1/release/activate`、`/api/v1/release/delete` | 管理会话；[管理文档](management.md) |
| 业务运行时 | GET `/api/v1/runtime/overview`、`/api/v1/runtime/config`、`/api/v1/runtime/static`、`/api/v1/runtime/release`、`/api/v1/runtime/release/static` | 公开；[运行时文档](runtime.md) |
| 快照资源 | GET `/api/v1/release/asset/{snapshot_id}/{asset_id}` | 公开，受版本删除和完整性检查约束 |
| 迁移与归档 | GET `/api/v1/transfer/export-tree`；POST `/api/v1/transfer/import`、`/api/v1/transfer/export`、`/api/v1/transfer/import-preview`、`/api/v1/transfer/import-selective`、`/api/v1/transfer/migrate` | 管理会话；[transfer.proto](../../../idl/biz/transfer.proto) |
| 程序版本 | GET `/api/v1/version`、`/api/v1/version/latest` | 公开；不是配置发布版本 |

健康检查 `GET /ping` 同样按部署拼接 BasePath，不在 `/api/v1` 下。

## 维护依据与文档站

IDL 适合查请求模型和路由，但不等同于实际 HTTP 输出。例如 runtime 的 object/keyvalue content 由手写 handler 输出原生 JSON；auth/password 实际字段为 `reauth_required`；部分 IDL 注释仍描述旧 Bearer。文档以实际行为为准，不把 proto 称为已经生成的 OpenAPI。

- [运行时 handler](../../../biz/handler/runtime/runtime_service.go)
- [发布 handler](../../../biz/handler/release/release_service.go)
- [认证 handler](../../../biz/handler/auth/auth_service.go)
- [管理鉴权](../../../biz/middleware/admin_session.go)
- [HTTP 契约测试](../../../biz/handler/release/release_http_test.go)

本入口可直接在 GitHub 阅读。现有 Pages 工作流仅监听 main；更新 main_v1 的 Markdown 不等于已发布 Fork 文档站。本次先维护这份 Fork Markdown 入口，不宣称原作者站点已随本分支更新。
