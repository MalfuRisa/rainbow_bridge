# 整机业务备份与恢复 API

[返回 API 入口](index.md) · [管理员会话](management.md)

本页对应后端第一版整机业务备份。备份包含业务数据库、原始资源和已提交版本快照；恢复需要重启服务，不在 HTTP 请求内替换正在使用的数据库。

## 1. 支持边界

第一版只支持以下部署同时成立：

- 单进程；
- SQLite 文件库；
- 本地 `uploads` 存储（`storage.type: local`）；
- 本地发布快照（`release_storage.type` 缺省或为 `local`），且全部版本库存（包括逻辑删除版本及已删范围历史）均无 MinIO locator；
- Redis 关闭；
- 数据库、上传目录、快照目录都位于 `<cwd>/data` 业务根内，且快照自定义目录解析后仍在业务根内；
- `control_dir` 是绝对路径、与业务根同一本地卷、位于业务根之外。

PostgreSQL、MySQL、原始资源或快照使用 MinIO、Redis 开启、多副本或跨卷目录会返回 `supported=false` 和具体原因。即使原始资源为 local，或已把快照全局设置改回 local，只要库存仍有 MinIO locator，就不能备份为完整文件包；对应原因包括 `snapshot_storage_not_local`、`snapshot_history_not_local`。此时实际操作返回 503 `BACKUP_UNSUPPORTED`，不会退化成“只备份一部分”。

生产仓库自带的 `config.yaml` 使用 PostgreSQL + MinIO + Redis，因此能力接口会明确显示不支持；需要 SQLite + 本地原始资源 + 本地快照（含全部历史）+ Redis 关闭的部署才能启用。MinIO 快照迁移与故障补清属于独立运维工具，不等于整实例备份或恢复。

## 2. 配置

```yaml
backup:
  enabled: true
  control_dir: "D:/RainbowBridge-control/local-demo"
  rollback_keep_count: 3   # 1-20；缺省/0 按 3
```

`rollback_keep_count` 只影响完整启动成功后保留的旧业务根副本数量，避免正常成功恢复后仍要求人工清理。0 使用默认 3；负数或大于 20 是配置错误，不会静默改成其他策略。

控制目录不能由请求指定。服务端在该目录下维护：

```text
identity.json            实例 ID
jobs/                    持久任务与 request_id 索引
backups/                 已完成备份 ZIP
imports/                 上传包与预览
staging/                 一致的私有副本、恢复候选
rollback/<operation>/data 每次切换前的业务根
rollback/<operation>/retention.json 完整启动成功后的保留证明
restore-journal.json     当前恢复切换记录
restore-journal.last.json 已成功启动后归档的最近一次切换记录
restore-plan.json        待重启安装计划
audit.jsonl              操作审计
instance.lock            操作系统实例锁
```

实例锁在打开数据库之前取得。同一 `control_dir` 的第二个进程会启动失败，不能靠“端口还空着”判断实例未运行。

## 3. 认证与响应

所有 `/api/v1/backup/*` 接口都要求现有管理员 Cookie 会话；POST 还要求 `X-CSRF-Token` 和合法 `Origin`。它们不属于公开 runtime 白名单。

所有 JSON 与下载都返回 `Cache-Control: no-store`。成功信封：

```json
{"code":200,"msg":"OK","data":{}}
```

异步受理返回 HTTP 202 与 `code:202`。错误返回真实 HTTP 状态：

```json
{"code":409,"msg":"backup is referenced by a restore job","error_type":"BACKUP_IN_USE"}
```

任务失败写在任务状态里（HTTP 仍可为 200），轮询方应看 `state` 和 `error`，不能把 HTTP 200 当成备份成功。

## 4. 接口总览

| 方法与路径 | 语义 |
| --- | --- |
| `GET /api/v1/backup/capabilities` | 支持边界、原因、固定限额、`restore_requires_restart`、`mode`、`rollback_retention:{keep_count,managed}` |
| `POST /api/v1/backup/create` | 异步创建备份，body：`request_id`、可选 `reason` |
| `GET /api/v1/backup/list` | 已完成且文件存在的备份，`limit` 默认 20、最大 100，`before_id` 为不透明游标 |
| `GET /api/v1/backup/status` | 单个任务，`job_id` 必填 |
| `GET /api/v1/backup/download` | 下载 ZIP，`backup_id` 必填，带 `X-Rainbow-Backup-Sha256` |
| `POST /api/v1/backup/delete` | `backup_id`、`expected_sha256`、`request_id` |
| `POST /api/v1/backup/restore/preview` | multipart 上传并异步校验 |
| `GET /api/v1/backup/restore/preview` | 查询预览，`preview_id` 必填 |
| `POST /api/v1/backup/restore/execute` | 校验预览后异步准备候选，最终状态 `restart_required` |
| `POST /api/v1/backup/restore/cancel` | 放弃尚待重启的计划并解除维护 |

## 5. 任务对象

```json
{
  "job_id": "b1111111-1111-4111-8111-111111111111",
  "request_id": "c1111111-1111-4111-8111-111111111111",
  "kind": "create",
  "state": "running",
  "phase": "copying_resources",
  "created_at": "2026-10-25T02:00:00Z",
  "updated_at": "2026-10-25T02:00:03Z",
  "actor": "admin",
  "reason": "before upgrade",
  "backup_id": null,
  "preview_id": null,
  "result": null,
  "error": null
}
```

- `kind`：`create`、`validate`、`restore`。
- `state`：`queued`、`running`、`succeeded`、`failed`、`interrupted`、`restart_required`、`cancelled`。
- `phase` 是给人看的阶段，不报告虚假百分比或 ETA。
- `result` 未完成时为 `null`；完成后的字段按任务类型出现。
- `error` 只包含分类与脱敏说明，不含 SQL、绝对路径或凭据。

服务重启时，未涉及切换的 `queued`/`running` 任务变为 `interrupted`，临时文件清理；`restart_required` 和切换 journal 继续按恢复流程处理。

## 6. 创建、列表、下载和删除

### 创建

```http
POST /api/v1/backup/create
Content-Type: application/json

{"request_id":"<UUID>","reason":"before upgrade"}
```

同一 `request_id` 重放返回原任务；同一 ID 携带不同字段返回 409 `BACKUP_REQUEST_CONFLICT`。受理响应里的 `backup_id` 在整个任务期间保持稳定，任务成功后再用该 ID 下载。

备份过程会先暂停新业务写并等待在途写结束（最多 30 秒），用 SQLite `VACUUM INTO` 取得一致快照，再复制数据库和文件。数据库与文件复制完成后恢复写入，随后只对私有副本打包。复制完成前不会产生“可下载”的包。

### 列表

```http
GET /api/v1/backup/list?limit=20&before_id=<上次返回的next_before_id>
```

只列出已完成、文件仍存在且可下载的备份：

```json
{
  "code": 200,
  "msg": "OK",
  "data": {
    "items": [{
      "backup_id": "...",
      "created_at": "2026-10-25T02:00:00Z",
      "consistent_at": "2026-10-25T02:00:01Z",
      "sha256": "...",
      "size": 6573,
      "counts": {"environments":2,"pipelines":2,"configs":10,"assets":4,
                 "versions":3,"deleted_versions":1,"freeze_events":2,
                 "release_events":6,"configs_deleted":0,"assets_deleted":0},
      "actor": "admin",
      "reason": "before upgrade"
    }],
    "has_more": false,
    "next_before_id": ""
  }
}
```

### 下载

```http
GET /api/v1/backup/download?backup_id=...
```

成功返回 ZIP，响应头含：

```text
Content-Disposition: attachment; filename="rainbow-business-backup-<id>.zip"
X-Rainbow-Backup-Sha256: <64位十六进制>
Cache-Control: no-store
```

同一备份最多允许 2 个并发下载。下载期间该备份不能删除。

### 删除

```http
POST /api/v1/backup/delete
Content-Type: application/json

{"backup_id":"...","expected_sha256":"...","request_id":"<UUID>"}
```

摘要不匹配返回 409 `BACKUP_TARGET_MISMATCH`；正在下载或被未完成恢复引用返回 409 `BACKUP_IN_USE`。第一版不自动删除旧包，最多保留 10 个已完成备份。

## 7. 恢复预览

```bash
curl -b cookies.txt \
  -H "Origin: http://127.0.0.1:8080" \
  -H "X-CSRF-Token: $CSRF" \
  -F "request_id=<UUID>" \
  -F "file=@backup.zip;type=application/zip" \
  http://127.0.0.1:8080/api/v1/backup/restore/preview
```

`request_id` 必须出现在 `file` 之前；文件按字节流式落盘，不只看 `Content-Length`。受理后轮询 `status`，任务成功时 `preview_id` 非空。查询：

```http
GET /api/v1/backup/restore/preview?preview_id=...
```

```json
{
  "code": 200,
  "msg": "OK",
  "data": {
    "preview_id": "...",
    "job_id": "...",
    "valid": true,
    "package_sha256": "...",
    "backup": {
      "backup_id": "...",
      "created_at": "2026-10-25T02:00:00Z",
      "consistent_at": "2026-10-25T02:00:01Z",
      "producer": {"app_version":"v1.2.3","git_commit":"abc123"},
      "schema_version": 4,
      "counts": {"environments":2,"pipelines":2,"configs":10,"assets":4,
                 "versions":3,"deleted_versions":1,"freeze_events":2,
                 "release_events":6,"configs_deleted":0,"assets_deleted":0},
      "files": 12
    },
    "warnings": ["external_references_not_included:1"],
    "issues": [],
    "expires_at": "2026-10-26T02:00:00Z",
    "target_instance_id": "...",
    "created_at": "2026-10-25T02:00:00Z"
  }
}
```

校验包括：ZIP 条目路径/类型/大小/数量、manifest 结构与摘要、数据库只读打开且 `sqlite_schema` 只含已知业务对象、完整性检查、外部键关系、发布 payload 摘要、生效版本指向、资源引用与实际文件。`valid=false` 时禁止执行；`issues` 与 `warnings` 都必须展示给管理员。

预览有效期 24 小时。过期返回 410 `BACKUP_PREVIEW_EXPIRED`，需要重新上传。

## 8. 准备恢复与重启

```http
POST /api/v1/backup/restore/execute
Content-Type: application/json

{
  "preview_id": "...",
  "package_sha256": "...",
  "target_instance_id": "...",
  "request_id": "<UUID>",
  "confirmation": "RESTORE_ALL_BUSINESS_DATA",
  "reason": "restore before upgrade"
}
```

要求：

- `confirmation` 必须完全等于 `RESTORE_ALL_BUSINESS_DATA`；
- `package_sha256` 必须与预览一致；
- `target_instance_id` 必须匹配当前实例；
- 同一 `request_id` 重放返回原任务，改字段返回 409；
- 存在未处理回退副本时拒绝执行，避免覆盖最后一个回退点。

准备阶段会重新校验包、在隔离目录构建候选业务根、复制资源、按服务端模型重建数据库并重新生成所有配置的 `edit_revision`。准备完成后任务进入 `restart_required`：

- 新业务写继续返回 503 `BACKUP_MAINTENANCE`，并带 `Retry-After: 5`；
- 公开读取仍返回旧数据；
- 管理员可以调用 cancel 放弃计划，候选目录会被清理，维护解除；
- 真正换库只发生在下一次进程启动、打开数据库之前。

### 取消

```http
POST /api/v1/backup/restore/cancel
Content-Type: application/json

{"job_id":"...","request_id":"<UUID>"}
```

仅允许 `state=restart_required` 且切换尚未开始。成功返回 `{"cancelled":true}`。

## 9. 重启时发生什么

启动顺序固定为：

1. 取得实例文件锁；
2. 读取 `restore-journal.json` 与 `restore-plan.json`；
3. 校验候选数据库与资源；
4. 只读读取旧实例管理员账号，并从旧根读取发布编号上界；
5. 把旧业务根重命名到 `rollback/<operation>/data`；
6. 把候选重命名到业务根；
7. 在候选库建立同一管理员账号、清空会话、保留凭据版本；
8. 校验新业务库与资源；
9. 记录 `committed` 和任务 `succeeded`；
10. 才进行常规初始化并开放 HTTP。

服务随后重新登录；旧 Cookie 一律 401。配置内容的业务字段和时间保留，但 `edit_revision` 全部更换，旧编辑页面不能拿旧基线继续保存。已发布版本 payload、资源 ID、锁版 revision、生效指针和历史事件按备份时点恢复。

失败处理：

- commit 前失败：尽量把旧业务根换回，任务 `failed`/`rolled_back`，不开放混合数据；
- 回退也失败：journal 记录 `rollback_required`，服务保持端口关闭，只能本机使用 CLI 处理；
- 成功切换保留 `rollback/<operation>/data`，并在正常初始化全部完成后写入 `rollback/<operation>/retention.json`；保留最近 N 份（`backup.rollback_keep_count`，默认 3），旧的成功副本按策略轮换。成功留存不再阻止下一次恢复。
- 正在准备/等待重启的计划、`rollback_required`、损坏或无法证实安全的副本仍会阻止下一次恢复；目录读取或控制记录读写失败返回存储错误，不会当成“没有冲突”。

再次恢复被拒绝时，HTTP 409 区分 `BACKUP_RESTORE_PENDING`（已有计划等待重启，先完成或取消计划）与 `BACKUP_ROLLBACK_REVIEW_REQUIRED`（异常或无法证实安全的旧副本，需要部署人员核对）。成功留存项不会返回后者。旧版服务器可能将这两种情况统一返回 `BACKUP_STATE_CONFLICT`。

恢复会把旧根的发布编号上界合并到候选库中相同 `scope_id` 的 head：即使备份创建后又成功生成过 v2/v3，恢复后下一次编号也不会倒退或把旧名字指向新内容。该合并只提升计数器，不导回备份后新建的配置或版本；恢复到另一套空实例只能从包内上界继续。需要跨独立实例长期固定的身份应使用 `snapshot_id`，不能依赖版本标签永久唯一。

## 10. 离线工具

```bash
# 校验包，不修改目标
backup-restore -config config.yaml -command validate -package backup.zip

# 校验并准备候选；随后按正常方式启动服务完成切换
backup-restore -config config.yaml -command stage -package backup.zip \
  -request-id <UUID> -reason "disaster recovery"

# 查看 journal、任务与 rollback 副本 operation id
backup-restore -config config.yaml -command status

# 放弃尚未切换的准备计划
backup-restore -config config.yaml -command cancel

# 显式回退上一次已提交恢复；保留当前管理员并清空会话
backup-restore -config config.yaml -command rollback -reason "restored data rejected"

# 异常处理时才需要显式清理某个副本；正常成功副本由保留策略管理
backup-restore -config config.yaml -command cleanup \
  -operation <operation_id> -confirm DELETE_ROLLBACK_COPY
```

正常运行不再要求普通用户为了下一次恢复手工归档或删除成功副本。`status` 仍列出副本；`rollback` 可回退最近一次成功恢复。手工保留在 `reviewed-rollback/` 的旧归档不属于 `rollback/` 自动轮换范围，不会被搬回或删除。

工具使用与服务器相同的实例文件锁，服务运行时拒绝操作。`rollback` 也会重建候选、重新生成 `edit_revision` 并写审计，不是简单复制旧目录；旧会话不会复活。

## 11. 限额与错误

| 限额 | 值 |
| --- | --- |
| 备份/上传包 | 2 GiB |
| 解压后数据库与文件合计 | 10 GiB |
| 普通文件数 | 50,000 |
| 单文件 | 2 GiB |
| manifest | 16 MiB |
| 创建/校验/准备 | 各 10 分钟 |
| 等待在途写排空 | 30 秒 |
| 完成备份保留 | 10 个 |
| 终态任务记录 | 最近 100 个 |
| 并行下载 | 2 |

| HTTP | error_type | 含义 |
| --- | --- | --- |
| 400 | `BACKUP_INVALID_REQUEST` / `BACKUP_PACKAGE_INVALID` | 请求或包结构/完整性不合法 |
| 401/403 | 既有 AUTH 错误 | 未登录、来源或 CSRF 不合法 |
| 404 | `BACKUP_NOT_FOUND` / `BACKUP_PREVIEW_NOT_FOUND` | 对象不存在 |
| 409 | `BACKUP_BUSY` / `BACKUP_REQUEST_CONFLICT` / `BACKUP_STATE_CONFLICT` | 已有任务、重用 ID、阶段不允许 |
| 409 | `BACKUP_TARGET_MISMATCH` / `BACKUP_IN_USE` | 摘要/实例不匹配，或包被引用/下载 |
| 409 | `BACKUP_RESTORE_PENDING` / `BACKUP_ROLLBACK_REVIEW_REQUIRED` | 有切换计划待重启 / 旧副本异常或无法证实安全 |
| 410 | `BACKUP_PREVIEW_EXPIRED` | 预览过期 |
| 413 | `BACKUP_LIMIT_EXCEEDED` | 包、条目、总量或数量超限 |
| 503 | `BACKUP_UNSUPPORTED` / `BACKUP_MAINTENANCE` / `BACKUP_SPACE_INSUFFICIENT` | 能力不支持、维护中、空间不足 |
| 500 | `BACKUP_STORAGE_ERROR` / `BACKUP_INTERNAL_ERROR` | 控制记录、磁盘或内部故障 |

## 12. 安全说明

- 备份 `schema_version` 为 4：保留 schema 2 的范围身份、`active_slot`、范围删除审计及 schema 3 的 `config_release_head.last_version_number`，新增版本内部存储定位列。schema 1/2/3 的旧包仍可验证和恢复，在隔离候选库按服务端模型补列、完成身份/编号升级；旧包缺失定位按 local 处理，不执行包内 DDL。包含 MinIO locator 的包不属于受支持的完整备份。
- `release_storage_attempt` 运行状态不进入备份包，恢复后不把历史 attempt 当成在线任务自动删除远程对象。
- 下载包不包含 `auth_users`、`auth_sessions`、密码哈希、Cookie、CSRF、`config.yaml`、MinIO/数据库凭据或控制目录记录。
- 私有一致性快照可能含认证表，只存在于控制目录 staging，任务结束即删除，绝不进入 ZIP。
- 配置正文本身可能包含业务敏感值；备份仍只允许管理员下载。
- SHA-256 用于发现损坏，不是来源认证或数字签名。第一版只允许导入可信备份。
- 不跟随 ZIP 链接、不自动解压嵌套归档、不执行上传包内的 SQL、触发器或迁移脚本。
