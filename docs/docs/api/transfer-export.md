# 导出来源与迁移归档

[返回 API 入口](index.md) · [范围身份](scope-identity.md)

`POST /api/v1/transfer/export` 支持三种明确来源，统一生成可重新导入编辑区的迁移归档：

| source | 说明 | 取数 |
| --- | --- | --- |
| `editor` | 编辑区当前内容，可多环境/多渠道、可勾选 | 当前有效 scope 的配置与引用资源 |
| `active` | 用户看到的生效快照，单范围整份 | `expected_snapshot_id` 对应的 active 版本 |
| `version` | 指定发布版本，单范围整份 | 固定 `scope_id + version + snapshot_id` |

发布来源第一版不做跨 scope 混合版本或快照内裁剪。导入后只进入编辑区，不自动锁版、创建版本或切换 active。

## 1. 导出请求

```http
POST /api/v1/transfer/export
Content-Type: application/json

{
  "format": "zip",
  "source": "version",
  "environment_key": "prod",
  "pipeline_key": "main",
  "expected_scope_id": "11111111-1111-4111-8111-111111111111",
  "version": "v1.0",
  "expected_snapshot_id": "22222222-2222-4222-8222-222222222222"
}
```

- `source` 缺省 `editor`；显式空值或未知值 400。
- `format` 缺省 `zip`，只允许 `zip`/`tar.gz`。
- `active` 必须传 `expected_scope_id` 和 `expected_snapshot_id`，不能传 `version`；读取时若 active 已变化返回 409 `TRANSFER_ACTIVE_CHANGED`。
- `version` 必须传范围、`expected_scope_id`、`version`、`expected_snapshot_id`；快照不符返回 409 `RELEASE_SNAPSHOT_MISMATCH`，版本已删返回 410。
- `editor` 使用 `selections`；单条选择可省略 `environment_key`（全环境）、`pipeline_key`（整渠道）、`resource_keys`（整渠道配置）。明确选中对象时传 `expected_environment_id`/`expected_scope_id`。
- 历史无 `source` 的 editor 请求继续兼容；服务端在请求捕获点解析一次当前身份并固定整次导出。

## 2. 导出预览

```http
POST /api/v1/transfer/export-preview
```

请求体与导出相同，不生成包、不改配置。返回：

```json
{
  "code": 200,
  "msg": "OK",
  "data": {
    "resolved_source": {
      "kind": "version",
      "captured_at": "2026-10-25T02:00:00Z",
      "environment_key": "prod",
      "pipeline_key": "main",
      "environment_id": "...",
      "scope_id": "...",
      "version": "v1.0",
      "snapshot_id": "...",
      "scopes": [
        {
          "environment_key": "prod",
          "pipeline_key": "main",
          "environment_id": "...",
          "scope_id": "...",
          "version": "v1.0",
          "snapshot_id": "..."
        }
      ]
    },
    "counts": {"configs": 10, "assets": 4, "bytes": "12345"},
    "warnings": ["external_references_not_included:1"],
    "estimated_bytes": "12345"
  }
}
```

editor 预览不是锁版；预览后编辑区变化，实际导出反映捕获时内容，文件元数据记录实际 `captured_at`/counts。

## 3. 响应

成功返回 ZIP/GZIP 二进制：

```text
Content-Disposition: attachment; filename="rainbow-transfer-version-v1.0-<短snapshot>.zip"
Cache-Control: no-store
X-Rainbow-Export-Source: version
X-Rainbow-Snapshot-Id: ...
X-Rainbow-Version: v1.0
```

editor 只带 `X-Rainbow-Export-Source: editor`。错误返回真实 HTTP 状态与 `error_type`：

| HTTP | error_type | 场景 |
| --- | --- | --- |
| 400 | `TRANSFER_INVALID_REQUEST` | 混用字段、非法 source/format、缺选择参数 |
| 404 | `TRANSFER_SELECTION_NOT_FOUND` / `RELEASE_VERSION_NOT_FOUND` / `RELEASE_NOT_ACTIVE` | 配置、版本或生效版本不存在 |
| 409 | `TRANSFER_ACTIVE_CHANGED` / `RELEASE_SNAPSHOT_MISMATCH` | 选择后目标变化 |
| 409/410 | `SCOPE_IDENTITY_CONFLICT` / `SCOPE_DELETED` | 同名范围已替换或删除 |
| 410 | `RELEASE_VERSION_DELETED` | 指定版本已删 |
| 413 | `TRANSFER_EXPORT_LIMIT_EXCEEDED` | 未压缩输出超过 192 MiB |
| 503 | `TRANSFER_RESOURCE_INCOMPLETE` / `TRANSFER_EXPORT_TIMEOUT` | 资源缺失或导出超时 |

编辑器导出在业务写协调/备份维护保护下捕获；维护中返回既有维护错误。发布来源只读不可变快照，已开始的导出在切版/删除后仍可完成，删除后的新请求拒绝。

## 4. 迁移归档格式

继续使用 `configs.json` + `files/<file_id>/<file_name>`，新增：

```json
{
  "archive_format": "rainbow-transfer-v2",
  "export_source": {
    "kind": "version",
    "captured_at": "2026-10-25T02:00:00Z",
    "producer": {"app_version": "...", "git_commit": "..."},
    "scopes": [
      {
        "environment_key": "prod",
        "pipeline_key": "main",
        "environment_id": "...",
        "scope_id": "...",
        "version": "v1.0",
        "snapshot_id": "..."
      }
    ]
  },
  "environments": [],
  "pipelines": [],
  "business_configs": [],
  "assets": [],
  "resources": [
    {"file_id": "...", "file_name": "a.png", "content_type": "image/png", "size": 12, "sha256": "..."}
  ],
  "warnings": []
}
```

`configs.json` 的 `business_configs` 保留 `resource_key`、`alias`、`name`、`environment_key`、`pipeline_key`、`type`、`content`、`remark`、`is_perm`、`description`；数字使用无损 JSON 解析，不经过 float64。

版本来源：

- 从快照 payload 与发布清单取内容，不从在线编辑区或原始上传目录读取。
- `release-asset://<asset_id>` 按现有标签/JSON/富文本规则改写为 `asset://<导出file_id>`，保留 query/fragment。
- 导出 file_id 由固定 UUID v5 命名空间对 `(snapshot_id, asset_id)` 生成；同快照重复导出稳定，不同快照不共用身份。
- 任何缺清单/缺文件/摘要不符整体失败；外部超链接只保留并写 warning。

导入：

- 迁移包通过 `POST /api/v1/transfer/import` 的 multipart 字段 `archive` 上传；目标范围用 `environment_key`/`pipeline_key`，并应同时传 `expected_environment_id`/`expected_scope_id`。
- v2 正常预览/导入；旧无标识包兼容；未知格式 400；runtime 静态包和整机备份包明确提示类型不符。
- 导入到目标 scope 时，资源身份按 `(目标 scope_id, 归档 file_id)` 做 UUID v5 映射，并重写配置引用；同包重复导入同目标复用同字节资源，不同目标不共用错误归属。
- 不导入来源 freeze/head/版本号/active；不复活来源已删除 scope。
- 目标是同名重建后的新 scope 时，必须传当前 `expected_scope_id`；旧 scope 身份返回 410/409，不会把旧包写进新代。

## 5. 与其它包的区别

- **迁移归档**：`configs.json`，可重新导入编辑区。
- **runtime 静态包**：`config.json` + `manifest.json` + `assets/`，用于业务部署，不导入编辑区。
- **整机业务备份**：`manifest.json` + `database/business.sqlite` + 原始资源/快照资源，用于整实例恢复，见 [备份 API](backup.md)。