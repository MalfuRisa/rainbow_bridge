# 业务运行时 API

[返回 API 入口](index.md) · [管理员操作](management.md)

本文路径都相对于部署地址，例如 `http://127.0.0.1:18080/rainbow-bridge`。公开业务读取无需管理员登录。每次读取或导出都固定本次解析到的快照，不会把两个版本的配置混在一起。

## 1. 读取当前生效版本

```http
GET /api/v1/runtime/release
x-environment: default
x-pipeline: default
```

未传 `version` 时读取该范围的生效指针。管理员切换后，下一次请求读取新版本；编辑区的增删改不会直接改变已发布版本。没有生效版本返回 404，不会自动读编辑区或最新创建的版本。

命令示例（Bash/curl；Windows 使用 curl.exe，按 shell 调整换行）：

```bash
curl --fail-with-body 'http://127.0.0.1:18080/rainbow-bridge/api/v1/runtime/release' \
  -H 'x-environment: default' \
  -H 'x-pipeline: default'
```

成功 HTTP 200，示例仅展示一个配置，ID、时间与摘要需使用实际响应：

```json
{
  "code": 200,
  "msg": "OK",
  "data": {
    "snapshot_id": "31111111-2222-4333-8444-555555555555",
    "version": "v1.0",
    "source_freeze_revision": "3",
    "payload_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    "selection_mode": "active",
    "active_revision": "2",
    "activated_at": "2026-10-21T02:00:00Z",
    "environment": {
      "environment_key": "default",
      "environment_name": "默认环境",
      "pipeline_key": "default",
      "pipeline_name": "默认渠道"
    },
    "configs": [
      {
        "resource_key": "demo-key",
        "alias": "welcome",
        "name": "欢迎语",
        "type": "text",
        "content": "你好",
        "remark": "",
        "is_perm": false,
        "description": ""
      }
    ]
  }
}
```

| 字段 | 说明 |
| --- | --- |
| `snapshot_id` | 不可变快照 ID，可用于后续固定导出 |
| `version` | 当前环境/渠道内的发布版本名，字符串 |
| `source_freeze_revision` | 创建时的锁版 revision，十进制字符串 |
| `payload_sha256` | 快照 payload 摘要，不是整个 HTTP 响应的摘要 |
| `selection_mode` | 默认读取为 active，指定版本为 fixed |
| `active_revision` | 生效指针 revision，十进制字符串；仅 active 模式出现 |
| `activated_at` | 本次读取指针对应的生效时间，RFC3339 UTC；fixed 模式省略，不是页面加载时间 |
| `configs` | 配置数组，无配置时为 `[]` |

默认 `content_format=json`：object/keyvalue 的 content 返回原生 JSON 值；其他类型仍返回字符串，包括 number、boolean 类型。调用方按 `type` 使用，不应假定全部已自动转换成数字或布尔值。

## 2. 固定读取指定版本

```http
GET /api/v1/runtime/release?version=v1.0
x-environment: default
x-pipeline: default
```

参数需 URL 编码。此时 `selection_mode` 为 fixed，不返回 `active_revision`、`activated_at`；其余结构同上。可读取已创建但当前未生效的版本；不依赖该范围是否已经设置 active。后续切换不会修改此版本的内容，但逻辑删除会使新请求返回 410。

不传 version 和 `?version=` 不同：后者明确提供了空版本号，返回 400。版本号仅在环境/渠道范围内解释，不是全局的虹桥程序版本。

## 3. 原文模式与大整数

```http
GET /api/v1/runtime/release?version=v1.0&content_format=raw
x-environment: default
x-pipeline: default
```

`content_format` 可为 `json`（默认）或 `raw`；空值及其他值返回 400。raw 模式每个配置的 content 都是字符串，例如：

```json
{
  "resource_key": "big-number-key",
  "alias": "limits",
  "name": "额度",
  "type": "object",
  "content": "{\"big\":9007199254740993}",
  "remark": "",
  "is_perm": false,
  "description": ""
}
```

raw 保留配置内容中的数字文本；其中受管资源地址仍会按快照规则渲染，不意味着返回数据库里的逐字原始 payload。JavaScript 使用 `response.json()` 解析外层时，content 内的数字仍是字符串中的文本；之后若对 content 再用普通 `JSON.parse()`，超出安全整数范围的数字仍可能丢失精度。需要保持原文或自行使用无损解析方案。

## 4. 固定导出版本资源包

```http
GET /api/v1/runtime/release/static?version=v1.0&expected_snapshot_id=31111111-2222-4333-8444-555555555555
x-environment: default
x-pipeline: default
```

先读取版本，取得同一响应的 `data.version`、`data.snapshot_id`，再把它们传入本接口。`expected_snapshot_id` 是防止导出目标与已展示目标不一致的校验值，不是额外的访问密钥。两个参数及两个范围请求头均必填；ID 必须是 UUID，与定位的版本不一致返回 409。

成功返回 ZIP，错误返回 JSON。成功头包含：

```http
Content-Type: application/zip
Content-Disposition: attachment; filename=...
Cache-Control: no-store
X-Rainbow-Snapshot-Id: 31111111-2222-4333-8444-555555555555
X-Rainbow-Version: v1.0
```

ZIP 内容：

```text
config.json
manifest.json
assets/
  <asset_id>.bin
```

config.json 包含版本元数据和 configs，资源地址替换为包内相对路径；manifest.json 包含格式标识、快照元数据和资源清单。每份资源包含大小、摘要及原始文件名等信息；`.bin` 是包内命名规则，不代表原始内容都是某种二进制格式。

这是配置与资源数据包，不含 index.html，不是可直接运行的网页，也不是 transfer 导入包或数据库备份。导出过程中切换 active 不影响已固定的导出目标。构包期限为 60 秒（调用方可更早取消），未压缩输出限额为 192 MiB；限额返回 413，超时返回 503，不返回伪装成功的错误 ZIP。

## 5. 快照资源下载

```http
GET /api/v1/release/asset/{snapshot_id}/{asset_id}
```

使用配置内容或版本详情实际返回的地址，不拼接原始编辑区路径。成功是原始文件字节和资源 Content-Type，服务端校验资源大小与 SHA-256。版本删除后新下载返回 410；资源缺失或完整性不符返回 503。业务读取本身检查引用文件存在和长度，资源下载时再核验完整 SHA-256。

## 6. 旧接口迁移

旧接口已经统一使用发布快照，**不再读取编辑区**。

| 接口 | 范围 | 行为 |
| --- | --- | --- |
| GET `/api/v1/runtime/config` | x-environment / x-pipeline 请求头 | 当前生效快照；保留旧 config 字段并增加版本元数据 |
| GET `/api/v1/runtime/static?environment_key=default&pipeline_key=default` | 查询参数 | 本次解析到的 active 资源包，不是固定某次之前展示的快照 |
| GET `/api/v1/runtime/overview` | 无 | 环境/渠道列表，不能当成生效版本配置 |

旧 config 的 data 为 snapshot_id、version、source_freeze_revision、active_revision、payload_sha256、selection_mode、environment、configs。每项 config 额外保留 environment_key、pipeline_key；object/keyvalue 返回 JSON，其余 content 为字符串。旧接口不提供 raw，也不承诺 activated_at。

旧 config/static 明确传 `version`、`expected_snapshot_id` 或 `content_format` 均返回 400，不能以为它们已支持新接口的选择参数。需要固定读取、raw 或固定导出时使用新接口。静态包格式也已改变，旧消费方应按 manifest 和相对资源路径适配。

## 7. 错误与缓存

运行时错误使用实际 HTTP 状态，示例：

```json
{
  "code": 404,
  "msg": "error",
  "error": "scope default/default has no active release",
  "error_type": "RELEASE_NOT_ACTIVE"
}
```

| HTTP | error_type | 调用方处理 |
| --- | --- | --- |
| 400 | RELEASE_INVALID_REQUEST | 补范围、合法 version/content_format 或有效 UUID |
| 404 | RELEASE_SCOPE_NOT_FOUND | 核对环境和渠道 |
| 404 | RELEASE_NOT_ACTIVE | 管理员尚未切换生效版本 |
| 404 | RELEASE_VERSION_NOT_FOUND / RELEASE_ASSET_NOT_FOUND | 核对版本或资源标识 |
| 409 | RELEASE_SNAPSHOT_MISMATCH | 固定导出目标与先前读取结果不符，核对范围和 ID |
| 410 | RELEASE_VERSION_DELETED | 版本已删除，不能通过重试恢复 |
| 413 | RELEASE_LIMIT_EXCEEDED | 查看 limits，缩减导出内容或调整发布方案 |
| 503 | RELEASE_INCOMPLETE | 快照/清单/文件不完整，由管理员恢复，服务端不回退编辑区 |
| 503 | RELEASE_EXPORT_TIMEOUT | 导出超时；可在合适退避后重试同一固定目标 |
| 500 | RELEASE_INTERNAL_ERROR / RELEASE_STORAGE_ERROR | 检查服务端日志 |

这些快照读取与导出使用 `Cache-Control: no-store`。跟随 active 的接入方不能无条件缓存五分钟后再称“实时切换”；如果自行设计缓存，应明确刷新周期及可接受的生效延迟。固定版本内容不可修改，但逻辑删除会阻止新的服务端读取；客户端已下载的副本无法被远程撤回。

重试仅用于网络故障和适当的临时服务错误，不能盲目重试 400/404/409/410。失败不应被客户端悄悄替换为编辑区配置。
