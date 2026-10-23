# 管理员会话、编辑区与发布 API

[返回 API 入口](index.md) · [业务运行时读取](runtime.md)

所有路径相对于实际部署地址。管理 API 面向控制台或管理工具；业务应用读取发布配置使用公开运行时接口，不需要拿管理员密码登录。

## 1. 登录和会话

### 登录

```http
POST /api/v1/auth/login
Origin: http://127.0.0.1:18080
Content-Type: application/json

{"username":"admin","password":"<管理员密码>"}
```

Origin 只有协议、主机、端口，不含 `/rainbow-bridge`，必须在部署的 trusted_origins 中精确匹配；登录缺少 Origin 也会被拒绝。不要把示例密码占位符当真实密码。

成功 HTTP 200，服务端 Set-Cookie，并返回：

```json
{
  "code": 0,
  "msg": "success",
  "data": {
    "user": {"id":"1","username":"admin","role":"admin"},
    "session": {
      "expires_at":"2026-10-21T10:00:00Z",
      "idle_expires_at":"2026-10-21T04:00:00Z"
    },
    "csrf_token":"<本次会话的令牌>"
  }
}
```

Cookie 为 HttpOnly、SameSite=Lax，名称和 Secure 由部署配置决定，Path 对应 BasePath；不要把开发 Cookie 名硬编码为所有部署值。服务端默认空闲期限 2 小时、绝对期限 8 小时，配置可调整；成功管理访问会续空闲期限，但不越过绝对期限。公开运行时读取不续管理员会话。

浏览器使用真实 Cookie jar；同源请求自动携带 Cookie，跨源 fetch 需 `credentials: 'include'`，还必须符合服务端精确来源 CORS、Cookie 域/路径及 SameSite/Secure 规则。不同来源不代表任意跨站点都能使用会话。

CSRF 只在内存保存，不写 URL、日志或持久化存储。管理员写请求使用 Cookie，并附 `X-CSRF-Token` 和可信 Origin；有 Origin 的管理读请求也校验来源。无 Origin 的非浏览器管理请求仍需有效 Cookie，写操作仍需 CSRF。旧 Bearer、自报角色或用户请求头不能替代会话。

### 会话接口

| 方法与路径 | 输入 | 成功 data / 行为 |
| --- | --- | --- |
| GET `/api/v1/auth/me` | 会话 Cookie | 同登录 data；恢复页面会话与 CSRF |
| POST `/api/v1/auth/logout` | Cookie、CSRF、空体或 `{}` | `{"logged_out":true}`，撤销会话并清 Cookie；无有效会话可幂等退出 |
| POST `/api/v1/auth/password` | Cookie、CSRF、JSON 的 current_password/new_password | `{"reauth_required":true}`，修改后撤销全部会话，重新登录 |

登录/改密仅接受 JSON，请求体上限 4 KiB，拒绝未知 JSON 字段。auth 成功正文 code=0，错误为实际 HTTP 状态及 `{code,msg,error_type}`。其他管理模块成功通常为 code=200，不要直接复用“code 必须等于 0”的判定。

| HTTP | error_type | 含义 |
| --- | --- | --- |
| 400 | AUTH_INVALID_REQUEST / AUTH_CURRENT_PASSWORD_INVALID | 参数不合法 / 当前密码错误 |
| 401 | AUTH_INVALID_CREDENTIALS / AUTH_REQUIRED | 凭据错误 / 需要登录 |
| 403 | AUTH_FORBIDDEN / AUTH_CSRF_INVALID / AUTH_ORIGIN_DENIED | 权限、CSRF 或来源失败 |
| 409 | AUTH_CREDENTIAL_CHANGED | 校验期间凭据变化，重新登录 |
| 413 / 415 | AUTH_INVALID_REQUEST | 请求体过大 / Content-Type 不受支持 |
| 429 | AUTH_RATE_LIMITED | 登录限流，遵守 Retry-After |
| 503 | AUTH_BUSY / AUTH_UNAVAILABLE | 密码计算繁忙 / 认证不可用 |

当前是简单管理员会话，不提供账号注册、多角色管理或完整 IAM 平台。未来外部 IAM 接入不在本合同中承诺。

## 2. 编辑区请求结构

编辑区与已发布版本独立；修改编辑区后需要重新锁版、创建版本并切换生效。锁版时配置写入、资源上传及相应范围变更会被阻止；读取和导出不因此停止。

配置列表：`GET /api/v1/config/list?environment_key=default&pipeline_key=default`，成功 data 为 `{total,list}`，不是旧文档里的 configs。单项详情 data 为 `{config}`。

配置创建请求：

```http
POST /api/v1/config/create
Content-Type: application/json
X-CSRF-Token: <当前会话令牌>

{
  "config": {
    "environment_key":"default",
    "pipeline_key":"default",
    "alias":"welcome",
    "name":"欢迎语",
    "type":"text",
    "content":"你好",
    "description":"",
    "remark":"",
    "is_perm":false
  }
}
```

alias 创建后不可改，name 可改；content 在管理请求中是字符串，object/keyvalue 需传编码后的 JSON 字符串。配置删除直接传 environment_key、pipeline_key、resource_key，不套 config。

环境 create/update 使用 `{"environment":{...}}`，字段含 environment_key、environment_name、description、sort_order、is_active。渠道 create/update 使用顶层 environment_key 与 `pipeline` 对象，内含 environment_key、pipeline_key、pipeline_name、description、sort_order、is_active。列表成功 data 为 `{total,list}`，详情为 `{environment}` 或 `{pipeline}`。

资源上传使用 multipart/form-data 的 file、environment_key、pipeline_key；不要手动给 FormData 设置缺少 boundary 的 Content-Type。成功 data 为 `{asset,reference}`，reference 是 `asset://<file_id>`，应使用返回值，不靠原始资源路径猜 ID。资源下载使用实际返回的 url（可能包含部署前缀及文件名）。跨范围引用会被锁版检查拒绝；迁移配置应走迁移接口并核对资源结果，不能只复制另一范围的 URL。

迁移/归档与发布静态包不同。迁移 JSON 字段是 source_environment_key、source_pipeline_key、target_environment_key、target_pipeline_key、resource_keys、overwrite；不是 source_environment 等旧文档字段。选择性导出入口实际为 `POST /api/v1/transfer/export`，JSON 包含 format 与 selections（范围和 resource_keys）；不能按内部方法名推测出另一个路径或改成 GET。完整路由见入口索引及 IDL。导入成功后还要核对 data.resource_import 和逐项问题，不能仅凭 HTTP 200 判断全部资源恢复。

部分历史 CRUD 的业务错误仍通过 HTTP 200 + 非 200 正文 code 返回，认证/冻结错误则会使用真实 HTTP 状态。此处只记录兼容现状，不声称本轮已统一历史错误协议。

### 配置编辑冲突保护

打开编辑时调用 `GET /api/v1/config/detail?environment_key=default&pipeline_key=default&resource_key=<配置键>`。详情直接读取数据库，返回 `Cache-Control: no-store`。列表、详情、创建及修改成功的配置均包含服务端生成的 `edit_revision`（小写标准 UUID v4）。

普通修改须原样回传打开时的标识，位于请求顶层：

```http
POST /api/v1/config/update
Content-Type: application/json
X-CSRF-Token: <当前会话令牌>

{
  "expected_edit_revision":"c29c6fbc-a274-4b88-8a21-a1e7cc51ba83",
  "config": {
    "environment_key":"default",
    "pipeline_key":"default",
    "resource_key":"<配置键>",
    "name":"欢迎语",
    "type":"text",
    "content":"欢迎使用虹桥",
    "description":"首页文案"
  }
}
```

成功返回 HTTP 200、`code: 200`、`data: {config}`，正文与新标识属于同一次提交。即使内容相同，成功保存也会换新标识；`config.edit_revision` 是只读字段，不能替代顶层 `expected_edit_revision`。

| HTTP | error_type | 处理方式 |
| --- | --- | --- |
| 400 | CONFIG_EDIT_REVISION_INVALID | 缺失、空值或非标准小写 UUID v4；修正客户端请求 |
| 409 | CONFIG_EDIT_CONFLICT | 他人或导入迁移已修改；保留草稿，重新加载并核对 |
| 404 | CONFIG_EDIT_TARGET_NOT_FOUND | 配置已删除；保留草稿并刷新列表，不自动重建 |

冲突正文为 `{code:409,msg:"error",error:"配置已被其他操作修改，请重新加载后核对再保存",error_type:"CONFIG_EDIT_CONFLICT",resource_key:"<配置键>"}`，不附带最新正文或标识。认证和 CSRF 仍先执行；合法请求若范围已冻结，优先返回原有 `409 SCOPE_FROZEN`。

编辑窗口提供“复制我的草稿”“重新加载”“取消”。重新加载须确认替换未保存内容，取消或读取失败均保留草稿。网络中断导致保存结果未知时，保留草稿并重新加载核对，不自动重发或强制覆盖。

升级需同步切换前后端及管理客户端；旧客户端缺少顶层标识会收到真实 HTTP 400。启动时会持久化回填旧数据的空标识，不改变业务内容或时间戳；导入及迁移覆盖生成目标的新标识，跳过项不变。发布快照、版本摘要及版本运行态不包含此字段。不支持旧二进制与新二进制混合写同一数据库。

## 3. 锁版和解锁

| 方法与路径 | 参数 | 结果 |
| --- | --- | --- |
| GET `/api/v1/freeze/status` | 查询 environment_key、pipeline_key | data 包含 state、revision、最后操作者/原因和时间、admin_configured |
| POST `/api/v1/freeze/lock` | JSON：范围、expected_revision、选填 reason | locked，revision 递增 |
| POST `/api/v1/freeze/unlock` | 同上 | unlocked，revision 递增 |
| GET `/api/v1/freeze/history` | 范围、limit、before_event_id | data.items、has_more、next_before_event_id |

revision 和事件 ID 使用十进制字符串；初始 revision 为 `"0"`。admin_configured 当前表示存在可用管理员账号，不再表示设置了旧密钥。历史 actor 为操作账号名，旧记录不回填。

先读状态，再提交读到的 revision：

```json
{
  "environment_key":"default",
  "pipeline_key":"default",
  "expected_revision":"0",
  "reason":""
}
```

reason 可省略、空或全空格，服务端 trim 后保存空字符串；最多 500 个 Unicode 字符。锁定前检查引用资源的归属和存在性；失败返回 400 FREEZE_RESOURCE_PRECHECK_FAILED 及 resource_issues，原因包括 missing_metadata、file_missing、cross_scope_reference、unresolvable_reference。页面显示跨范围资源时，应核对或修复资源归属，不移除检查。

revision 过期返回 409 FREEZE_REVISION_CONFLICT，重复当前状态返回 409 FREEZE_STATE_UNCHANGED，响应提供 latest_state/latest_revision。不要自动拿新 revision 强行覆盖他人操作；刷新状态并由操作方确认。此接口没有 request_id 幂等机制。

范围被冻结的编辑区写入返回 409 SCOPE_FROZEN，并提供 blocked_scopes；写协调繁忙/超时返回 503 FREEZE_WRITE_GATE_BUSY / FREEZE_WRITE_TIMEOUT。

## 4. 创建、切换和删除版本

### 管理读取

| 方法与路径 | 查询参数 | 成功 data 结构 |
| --- | --- | --- |
| GET `/api/v1/release/list` | environment_key、pipeline_key、limit、before_id | items（每项 meta/is_active）、has_more、next_before_id、独立 active |
| GET `/api/v1/release/detail` | environment_key、pipeline_key、version | meta、configs、assets、active |
| GET `/api/v1/release/active` | environment_key、pipeline_key | active；未设置时 HTTP 200，active=null |
| GET `/api/v1/release/history` | environment_key、pipeline_key、limit、before_event_id | items、has_more、next_before_event_id |

limit 缺省 20、最大 100；游标是接口返回的十进制字符串。版本列表按创建倒序，不显示逻辑删除项；active 独立于当前页，不应假设当前页必然包含生效版本。详情 configs 的 content 始终是字符串；assets/external_references 等空数组按实际显式 JSON 返回 `[]`。

meta 包含 snapshot_id、version、environment_key、pipeline_key、环境/渠道名、source_freeze_revision、creation_request_id、config_count、asset_count、asset_bytes、payload_sha256、manifest_sha256、external_references、reason、actor、created_at。active 包含 snapshot_id、version、revision、changed_at、changed_by、changed_reason。

### 创建版本

`POST /api/v1/release/create`，管理会话和 CSRF：

```json
{
  "environment_key":"default",
  "pipeline_key":"default",
  "version":"v1.0",
  "expected_freeze_revision":"1",
  "request_id":"41111111-2222-4333-8444-555555555555",
  "reason":""
}
```

expected_freeze_revision 必须使用锁定后响应中的 revision，不能使用锁定前的值。版本名满足 `^[a-z0-9][a-z0-9._-]{0,63}$`，同一范围不可重名；创建不可变快照，但不会切换 active。成功 data 为 `{meta,replayed}`。

源必须已锁定。发布检查会拒绝无法固定的嵌入媒体等资源问题，返回 409 RELEASE_RESOURCE_INVALID 和 resource_issues；普通超链接与嵌入媒体的规则不同，不能把提示的外部链接都理解为已复制进快照。当前不支持从 MinIO 源创建新快照，返回 503 RELEASE_STORAGE_UNSUPPORTED，不承诺真实多副本或远程存储部署已验证。

### 切换生效（含切回）

`POST /api/v1/release/activate`：

```json
{
  "environment_key":"default",
  "pipeline_key":"default",
  "version":"v1.0",
  "expected_active_revision":"0",
  "request_id":"51111111-2222-4333-8444-555555555555",
  "reason":""
}
```

先读取 active，尚未生效时使用 `"0"`；已有 active 时使用其 revision。只移动生效指针，不修改编辑区或版本内容。成功 data 为 operation、active、version；operation 包含 request_id、replayed、applied_revision。active 表示响应时的当前状态，重放旧请求不保证它仍是当时切换的版本。

并发 revision 不符返回 409 RELEASE_ACTIVE_REVISION_CONFLICT，可能带 latest_active；目标已生效返回 409 RELEASE_ACTIVE_UNCHANGED。应刷新并确认，不在后台盲目覆盖。

### 删除非生效版本

`POST /api/v1/release/delete`：

```json
{
  "environment_key":"default",
  "pipeline_key":"default",
  "version":"v0.9",
  "expected_snapshot_id":"61111111-2222-4333-8444-555555555555",
  "request_id":"71111111-2222-4333-8444-555555555555",
  "reason":""
}
```

成功 data 包含 request_id、snapshot_id、version、deleted、deleted_at、deleted_by、reason、replayed。只允许逻辑删除非 active 版本，删除 active 返回 409 RELEASE_DELETE_ACTIVE；snapshot 不匹配返回 409 RELEASE_SNAPSHOT_MISMATCH。

删除后列表隐藏、历史保留，版本号不能再次使用，详情/运行时/静态导出/资源的新访问返回 410 RELEASE_VERSION_DELETED。文件保留，不回收空间，也没有恢复接口。已开始且固定了目标的读取/导出允许完成，已下载的副本不会被撤回。有历史版本（含删除记录）的范围仍不能删除环境/渠道，返回 409 RELEASE_SCOPE_HAS_VERSIONS。

### 原因、幂等和超时确认

创建、切换、删除的 reason 与锁版相同：选填、trim、最多 500 个 Unicode 字符。request_id 必须是 UUID；同一次操作结果不确定时保留整份首次 payload，用同一 ID 原样确认。改变范围、目标、原因或 revision 等字段就是新操作，应使用新 ID。同一操作内 ID 重用但字段不同返回 409 RELEASE_REQUEST_CONFLICT。

创建重放在版本后来被删除后返回 410；切换重放确认历史操作而不重新切回；删除相同请求可重放原删除结果。不要把“网络超时”理解成“服务端一定没执行”，也不要为了重试改掉已提交的字段。

## 5. 推荐调用顺序

1. 登录并保存会话内 CSRF；管理接口统一走 Cookie。
2. 编辑配置并核对资源范围。
3. 读取 freeze/status，带 expected_revision 锁版，取得新 revision。
4. 带锁版 revision 创建版本，保存返回的 version/snapshot_id。
5. 读取 release/active，带 expected_active_revision 切换。
6. 通过公开 runtime/release 核对生效结果；需要固定资源包时使用读取返回的版本和 snapshot_id。
7. 下一轮编辑前按最新 revision 解锁；清理版本时只删除非 active 版本。

认证失效回到登录，不恢复旧 Bearer 兜底；409 需核对最新状态，参数错误不盲目重试。只读列表可正常刷新，不应因刷新产生新的锁版或发布副作用。
