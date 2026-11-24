# rainbow_bridge CHANGELOG

## [v5.4.2] — 2026-11-24

- `GET /api/v1/runtime/config` 新增可选 `version` 和 `content_format`。省略 `version` 读取当前生效快照，指定 `version` 读取同一环境和渠道的固定版本。
- 省略 `content_format` 时保留 Insights 现有响应格式。新调用方可显式传 `json` 读取网页链接对象，或传 `raw` 读取字符串。EWork 使用的 `/runtime/release` 和两个 ZIP 下载入口继续接受现有请求。

## [v5.4.1] — 2026-11-20

- 恢复公开的 `GET /api/v1/runtime/config` 和 `GET /api/v1/runtime/static`，兼容已有 Insights 客户端和品牌资源打包。两条入口沿用 `v5.2.0` 的调用方式，读取当前生效快照。`GET /api/v1/runtime/release` 和 `GET /api/v1/runtime/release/static` 继续用于版本读取和固定导出。
- 渠道更新恢复 `v5.3.0` 的表单、Protobuf 和 JSON 兼容行为。旧请求体中的 `sort_order: 0` 保留已有排序。虹桥管理台通过 `set_sort_order` 查询参数提交明确的排序值。

## [v5.3.0] — 2026-11-13

### **BREAKING**：对外旧 runtime HTTP 入口退役

旧配置入口 `GET /api/v1/runtime/config` 与旧静态包入口 `GET /api/v1/runtime/static`
（含路由注册、handler、IDL service 项、公开访问白名单条目）全部删除。
**旧客户端必须改用现行入口**：

- 读配置 → `GET /api/v1/runtime/release`（`x-environment`/`x-pipeline` 头，可选 `version`、`scope_id`、`content_format`）
- 下载静态包 → `GET /api/v1/runtime/release/static`（同头，可选 `version`、`scope_id`、`expected_snapshot_id`）

行为差异：旧入口对非 UUID `scope_id` 返回 400 `SCOPE_INVALID_REQUEST`；
现行入口与 `runtime/release` 口径一致，未知/非法 `scope_id` 返回 404 `SCOPE_NOT_FOUND`。

### 其他清理

- 删除仅测试使用的 `Service.ExportActiveReleasePackage`（空 pin 兼容转发），测试改用 `ExportActiveReleasePackageWithScope`
- 删除 `Logic.GetConfigByKey`（无 scope 的 legacy 查询死代码）及仅为它保留的 `ConfigDAO.GetByResourceKeyOnly`
- 删除 `RuntimeReleaseLegacy`/`normalizeLegacyScopeID` legacy 适配层；`ExportActiveReleasePackageWithScope` 的 scope pin 校验与 `runtime/release` 统一
- 管理台前端删除零调用的 `runtimeApi.config`/`runtimeApi.exportStatic` 旧客户端方法及配套死类型
- 维护切换验收、e2e/性能/探针/Postman 资产同步迁移到现行入口
