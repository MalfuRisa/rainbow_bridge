# 发布差异与集中发布流程

[返回 API 入口](index.md) · [范围身份](scope-identity.md)

本页包含两件事：

1. `POST /api/v1/release/compare`：按 `comparison_mode` 比较“当前生效版本 → 目标版本”或任意两个已发布版本。
2. 前端按顺序复用现有 lock/create/compare/activate 接口完成一次集中发布；后端不提供“一键发布”长事务。

## 1. 比较请求

### active（缺省）

```http
POST /api/v1/release/compare
Content-Type: application/json

{
  "environment_key": "prod",
  "pipeline_key": "main",
  "expected_scope_id": "11111111-1111-4111-8111-111111111111",
  "version": "v2",
  "expected_snapshot_id": "22222222-2222-4222-8222-222222222222",
  "expected_active_revision": "3",
  "expected_active_snapshot_id": "33333333-3333-4333-8333-333333333333",
  "offset": 0,
  "limit": 20
}
```

- `comparison_mode` 省略或传 `active`；显式空串或其他值返回 400 `RELEASE_INVALID_REQUEST`。
- `expected_scope_id`、`version`、`expected_snapshot_id`、`expected_active_revision` 必填。
- `expected_active_revision` 是非负十进制字符串；不要转成 JS number。
- 无生效版本时 `expected_active_snapshot_id` 传空串或省略，但 revision 仍必须传实际 head revision（首次通常 `"0"`）。
- 有 active 时若 `expected_active_snapshot_id` 为空或不符，返回 409 `RELEASE_COMPARE_BASELINE_MISMATCH`，不会自动补当前 active。
- active 模式拒绝提交 `baseline_version` 或 `expected_baseline_snapshot_id`（即使空串也拒绝），避免混用。

### fixed（任意两个版本）

```json
{
  "comparison_mode": "fixed",
  "environment_key": "prod",
  "pipeline_key": "main",
  "expected_scope_id": "11111111-1111-4111-8111-111111111111",
  "baseline_version": "v2",
  "expected_baseline_snapshot_id": "22222222-2222-4222-8222-222222222222",
  "version": "v5",
  "expected_snapshot_id": "55555555-5555-4555-8555-555555555555",
  "offset": 0,
  "limit": 20
}
```

- 表示“从 `baseline_version` 到 `version`”：before 是 baseline，after 是目标。
- 双方 version/snapshot 都必填，且必须是当前同一 scope 的未删除版本；不存在 404、已删除 410、snapshot 不符 409。
- fixed 拒绝提交 `expected_active_revision` 或 `expected_active_snapshot_id`（即使空串也拒绝）。
- fixed 不读写 head 来选择结果，因此不要求 active 存在，也不会因比较期间切换 active 而改变两侧；双方被删除或 scope 被删除后新请求仍返回 410。
- 交换 baseline 与 target 即可反转方向：modified/unchanged 数量不变，added 与 removed 对调，before/after 对调。同一版本与自身比较合法，返回 `items:[]` 和零变化。
- fixed 结果不能直接作为发布确认依据；要切换生效时必须重新执行 active compare，并把该响应的 `active_revision` 交给 activate。

### 公共规则

- `expected_scope_id`、目标 `version`、`expected_snapshot_id` 在两种模式都必填。
- `offset` 默认 0；`limit` 缺省/0 为 20，负值或大于 100 返回 400。
- 只读：不创建版本、head、审计事件或比较记录，返回 `Cache-Control: no-store`。
- 不接受 editor、跨 scope 比较；旧 scope 返回 410。

## 2. 比较响应

```json
{
  "code": 200,
  "msg": "OK",
  "data": {
    "scope_id": "...",
    "comparison_mode": "active",
    "target": {"version": "v2", "snapshot_id": "..."},
    "baseline": {"version": "v1", "snapshot_id": "..."},
    "active_revision": "3",
    "target_is_active": false,
    "has_changes": true,
    "summary": {"added": 1, "modified": 1, "removed": 1, "unchanged": 2},
    "items": [
      {
        "resource_key": "site_title",
        "change_type": "modified",
        "changed_fields": ["content"],
        "before": {"alias": "site_title", "name": "网站标题", "type": "text"},
        "after": {"alias": "site_title", "name": "网站标题", "type": "text"}
      }
    ],
    "total_changes": 3,
    "offset": 0,
    "limit": 20,
    "has_more": false,
    "next_offset": null
  }
}
```

- `comparison_mode` 总是返回实际模式。
- active 模式返回字符串 `active_revision` 和布尔 `target_is_active`；fixed 模式两者固定为 `null`，不暗示此刻哪个版本生效。
- active 的 `baseline` 无生效版本时为 `null`，目标全部算 added；fixed 的 baseline 必不为 `null`。
- `items` 只含 added/modified/removed，不输出 unchanged；按 `resource_key` 的 UTF-8 字节序升序。
- added 的 before 为 null，removed 的 after 为 null，changed_fields 为 `[]`。
- modified 的 `changed_fields` 顺序固定：`alias`、`name`、`type`、`content`、`remark`、`is_perm`、`description`。
- before/after 只是表格摘要（alias/name/type）；完整内容用 `GET /api/v1/release/detail` 分别读取目标与基线的版本，核对 `snapshot_id`。
- `has_more` 时 `next_offset` 是下一页 offset，否则为 `null`。
- `has_changes:false` 只表示配置与受管资源语义相同，不代表资源健康或发布成功。

## 3. 比较规则

- 比较键是 `(scope_id, resource_key)`。
- 不直接比较 runtime URL、payload 总 SHA 或快照内随机 asset_id。
- 受管引用按 `(sha256,size,content_type,file_name)` 加 query/fragment 后缀形成内部比较形式；图片重新快照但字节/名称/类型/引用后缀相同视为未变。
- object/keyvalue 使用无损 JSON：对象键顺序和空白不算变化；数组顺序、数字词元（`1` 与 `1.0`）按不同处理；大整数不经过 float64。
- 富文本/CSS/srcset 复用现有 release 标记扫描规则。
- 用户普通文本与内部资源标记不会碰撞。
- 不读取资源字节；payload/清单摘要损坏或引用缺清单时整体返回 503，不返回部分差异。

## 4. 错误

| HTTP | error_type | 说明 |
| --- | --- | --- |
| 400 | `RELEASE_INVALID_REQUEST` / `SCOPE_INVALID_REQUEST` | 字段、分页、revision 或身份非法 |
| 401/403 | 既有 AUTH 错误 | 会话、CSRF、Origin 不满足 |
| 404 | `RELEASE_VERSION_NOT_FOUND` / 范围不存在 | 目标/基准或范围不存在；fixed 错误带 `side:baseline|target` |
| 409/410 | `SCOPE_IDENTITY_CONFLICT` / `SCOPE_DELETED` | 同名重建或已删除 |
| 410 | `RELEASE_VERSION_DELETED` | 目标或固定的基准版本已删除；fixed 错误带 `side` |
| 409 | `RELEASE_SNAPSHOT_MISMATCH` | version 与 snapshot 不匹配；fixed 错误带 `side` |
| 409 | `RELEASE_ACTIVE_REVISION_CONFLICT` | head revision 不符，返回 `latest_active`（无 active 时 null） |
| 409 | `RELEASE_COMPARE_BASELINE_MISMATCH` | revision 相同但 active snapshot 不符 |
| 503 | `RELEASE_INCOMPLETE` | payload/清单损坏或引用缺清单 |
| 503 | `RELEASE_COMPARE_TIMEOUT` | 计算超时，可按原参数重试 |

## 5. 集中发布流程

| 步骤 | 接口 | 客户端固定 |
| --- | --- | --- |
| 读取状态 | 环境/渠道、`GET freeze/status`、`GET release/active` | 当前 `scope_id`、freeze revision、active snapshot 和始终存在的 `data.active_revision` |
| 锁版 | `POST freeze/lock` | `expected_scope_id`、`expected_revision` |
| 创建候选 | `POST release/create` | 同一 scope、锁版后的 freeze revision、独立 create request_id；不提交 version，使用响应中的实际编号 |
| 查看差异 | `POST release/compare` | 目标 version/snapshot、比较时 active snapshot/revision |
| 确认生效 | `POST release/activate` | 同一 scope、目标 version、compare 返回的 active_revision、独立 activate request_id |
| 核对 | `GET release/active` / `release/detail` | 原请求与目标身份；区分“操作曾成功”和“此刻仍生效” |

- 已锁范围直接使用当前 freeze revision 创建，不重复锁版。
- 已有未生效版本可直接 compare → activate，无需重新创建。
- 首次无 active 可发布：compare 与空基准比较，activate 使用 `GET release/active` 返回的 `data.active_revision` 字符串；不能因为 `active:null` 就假设 revision 一定是 0。
- `active_revision` 非零但 `active:null` 是兼容边界状态，当前没有正常公开入口主动制造；遇到时应按真实 revision 做 compare/activate，必要时由管理员核对，不要重置 revision 或自动加一。
- create 与 activate 使用两个独立 request_id；响应丢失时原样重放。自动编号在 create 事务内分配，重放不会再次消耗编号。
- 发布确认只能使用 active compare；fixed compare 的 `active_revision` 为 null，不能拿来静默切换。
- compare 后 active 被其他人切换：activate 用旧 revision 返回 409；重新调用 `release/active` 取得新 `active_revision`，重新 compare，再由用户确认。不能先刷新 head 静默切换，也不能从错误文字提取数字自动重试。
- activate 不要求 compare 令牌；服务端仍校验 scope、目标未删和 revision CAS。
- 取消或切换失败：候选版本保留且未生效，active 不变；可继续 compare 或显式删除版本。
- 冻结后取消整个流程不自动解锁；用户可显式 unlock。
- 当前 scope 被删除重建后，旧流程终止，不迁移候选、revision 或 request_id。

## 6. 维护与鉴权

- compare 是只读 POST，但仍要求管理员会话与 CSRF/Origin；不在公开 runtime 白名单。
- 备份维护期间 compare 可用；lock/create/activate 等写接口仍受原维护保护返回 503。
- 比较不占用全局业务写闸门，不阻塞用户直到确认。
- 单次请求最长 60 秒，沿用每快照 5000 配置/64 MiB payload 上限。