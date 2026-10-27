# 范围身份、删除与同名重建

[返回 API 入口](index.md) · [整机业务备份](backup.md) · [导出来源](transfer-export.md)

本页对应“解锁后删除环境/渠道并允许同名重建”的后端合同。业务 key（`environment_key`/`pipeline_key`）继续用于展示和兼容调用，但不再是数据库唯一身份；每一代范围都有独立的 `environment_id` / `scope_id`。

## 1. 身份字段

| 对象 | 字段 |
| --- | --- |
| 环境 | `environment_id`（UUID，每次创建新值） |
| 渠道 | `scope_id`、`environment_id` |
| 配置、资源 | `scope_id` |
| 冻结状态/事件 | `scope_id` |
| 发布版本/生效指针/事件 | `scope_id` |

删除后 `active_slot` 置空，旧行保留 `deleted_at`；同一业务 key 再创建时插入新行、新 UUID，不复用旧行，也不继承旧配置、资源、freeze/head 或版本。

## 2. 读取合同

- 环境 list/detail/create/update 返回 `environment_id`。
- 渠道 list/detail/create/update、runtime overview 返回 `scope_id` 和 `environment_id`。
- 配置、资源、freeze、release 元数据返回 `scope_id`。
- 管理列表/详情可选传 `scope_id` 定位当前代；传旧代返回 410 `SCOPE_DELETED`，不会回退到同名新代。
- 未携带身份键的运行时调用继续解析当前有效范围；同名重建后会读取新范围。需要长期固定的调用方应使用 `scope_id`/`snapshot_id`。
- 旧 `snapshot_id`/`file_id` 属于旧代，范围删除后新请求返回 410。

## 3. 删除接口

### 删除环境

```http
POST /api/v1/environment/delete
Content-Type: application/json

{
  "environment_key": "prod",
  "expected_environment_id": "11111111-1111-4111-8111-111111111111"
}
```

- 只检查**当前有效渠道**是否锁版；任一 locked 返回 409 `SCOPE_FROZEN`，整体不删除。
- 全部解锁即可删除，不再因为生效版本、历史版本或已删版本存在而阻止。
- 删除事务内：环境与有效渠道置逻辑删除，配置/原始资源逻辑删除，未删除版本补 `scope_deleted` 删除标记，清空生效指针，保留冻结/发布历史，写范围删除审计。
- 文件不立即删除。

成功：

```json
{
  "code": 200,
  "msg": "OK",
  "data": {
    "deleted": true,
    "environment_id": "...",
    "deleted_at": "2026-10-25T02:00:00Z",
    "pipelines": 2,
    "configs": 10,
    "assets": 4,
    "versions": 3
  }
}
```

### 删除渠道

```http
POST /api/v1/pipeline/delete
Content-Type: application/json

{
  "environment_key": "prod",
  "pipeline_key": "main",
  "expected_scope_id": "22222222-2222-4222-8222-222222222222"
}
```

只检查自身冻结状态，不影响其他渠道或环境名称。

### 身份与错误

| HTTP | error_type | 场景 |
| --- | --- | --- |
| 400 | `SCOPE_INVALID_REQUEST` | 缺少/非法身份 UUID |
| 404 | `SCOPE_NOT_FOUND` | 业务 key 从未存在 |
| 409 | `SCOPE_IDENTITY_CONFLICT` | 同 key 已重建，身份不同 |
| 410 | `SCOPE_DELETED` | 指定身份已删除 |
| 409 | `SCOPE_FROZEN` | 有待删除的有效渠道仍 locked |

删除请求没有 `request_id`；结果丢失后查询对象身份，禁止仅凭同 key 再次自动删除。

## 4. 写入必须携带身份

- 更新环境、创建渠道：`expected_environment_id`。
- 修改/删除渠道，以及配置创建/更新/删除、资源上传、freeze lock/unlock、release create/activate/delete：`expected_scope_id`。
- 服务层先校验 UUID 与当前身份匹配，再执行原有 edit/freeze/head revision 与 request_id 规则；不能只在 Handler 比较，也不能缺省跳过。
- 旧请求命中旧 scope 返回 410，命中同名新 scope 的身份不符返回 409；即使数字 revision 相同也不会修改新对象。
- 导入/迁移在写协调内解析目标身份一次并绑定整个操作；归档里的来源身份只用于追溯，不作为目标身份落库。

## 5. 默认对象与启动迁移

- 启动时先完成身份回填、索引替换，再开放 HTTP。
- 已有行获得稳定身份；无法唯一判定归属或存在冲突时拒绝启动并输出脱敏诊断，不猜测写入。
- 默认环境/渠道只在从未初始化的数据库创建。用户删除后重启不会复活；用户显式创建同名 `default` 会得到全新身份。
- 旧版索引（`uk_environment_key`、`uk_env_pipeline`、`uk_config_resource`、`uk_freeze_scope`、`uk_release_scope_version`、`uk_release_head_scope`）由迁移移除，并创建按身份的约束与别名唯一索引。

## 6. 与备份恢复

- 备份 schema_version 当前为 `3`：schema 2 的范围身份（含已删除 scope、`active_slot`、删除审计）加上 `config_release_head.last_version_number`。
- schema 1 旧包仍可恢复：在隔离候选库中先按旧白名单校验，再执行确定性身份回填，并补编号列/回填计数后按 schema 3 校验/恢复；不执行包内 DDL。
- schema 2 包仍可恢复：先按 schema 2 白名单校验，再在隔离候选库补编号列并用历史 `vN` 回填；旧二进制不能读取 schema 3 包。
- 恢复后所有配置重新生成 `edit_revision`；管理员账号保留、旧会话失效。