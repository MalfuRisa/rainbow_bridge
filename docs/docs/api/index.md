# API 接口文档

虹桥计划提供完整的 RESTful API 接口，用于配置管理、资源分发和版本发布。本文对应 Fork 的 main_v1 当前功能。

## 📚 接口概览

### 基础信息

**Base URL**: `http://localhost:8080/rainbow-bridge/api/v1`

示例端口按实际部署替换，例如本机演示使用 18080。下文路径以 /api/v1 开始，完整请求为“协议 + 主机 + 部署前缀 + 接口路径”，不要把 Base URL 与完整 /api/v1 路径重复拼接。部署在根路径时省略 /rainbow-bridge。

**请求头**：

```http
Content-Type: application/json
x-environment: prod
x-pipeline: main
```

Content-Type 用于 JSON 请求；资源上传使用 multipart/form-data。x-environment、x-pipeline 用于版本运行时接口选范围；管理接口通常通过查询或 JSON 字段选范围。旧静态导出的范围是查询参数。

### 响应格式

大部分管理及运行时 JSON 接口成功返回：

```json
{
  "code": 200,
  "msg": "OK",
  "data": {}
}
```

登录及会话接口成功使用 `code: 0`、`msg: "success"`。下载接口成功返回 ZIP 或资源字节。部分历史 CRUD 错误仍以 HTTP 200 携带业务错误 code，调用方应同时检查 HTTP 状态和正文 code；不能假定所有接口成功 code 都相同。

配置修改采用编辑标识校验：打开详情取得 `edit_revision`，保存时在顶层携带 `expected_edit_revision`；过期返回真实 HTTP 409，缺失或非法返回 400，目标已删除返回 404。具体请求及草稿处理见 [管理 API 的配置编辑冲突保护](management.md#配置编辑冲突保护)。

**状态码说明**：

| HTTP 状态 | 说明 |
| --- | --- |
| 200 | 请求成功；历史接口还需核对正文 code |
| 400 | 参数或锁版资源检查失败 |
| 401 / 403 | 未登录、无权限、来源或 CSRF 不合法 |
| 404 | 范围、版本、资源不存在，或尚未设置生效版本 |
| 409 | revision、请求标识、快照冲突或操作不允许 |
| 410 | 版本已逻辑删除 |
| 413 / 415 / 429 | 超出限额 / 不支持的内容类型 / 登录限流 |
| 500 / 503 | 内部故障 / 临时不可用或快照不完整 |

**权限说明**：业务运行时、程序版本和资源下载公开；管理 API 使用 Cookie 会话，写操作还需 X-CSRF-Token。具体流程见后文“认证与授权”。

## 🔌 接口详情

### 环境管理 API

管理读接口要求登录，写接口还需 CSRF。列表 data 为 total/list，单项 data 为 environment。生成模型的空字段可能省略，不依赖不存在的 created_at/updated_at。


#### 获取环境列表

```http
GET /api/v1/environment/list
```

**响应示例**：

```json
{
  "code": 200,
  "msg": "OK",
  "data": {
    "total": 1,
    "list": [
      {
        "environment_key": "prod",
        "environment_name": "生产环境",
        "description": "线上生产环境",
        "sort_order": 0,
        "is_active": true
      }
    ]
  }
}
```


#### 获取环境详情

```http
GET /api/v1/environment/detail?environment_key=prod
```

**响应 data**：`{ "environment": {...} }`。


#### 创建环境

```http
POST /api/v1/environment/create
Content-Type: application/json

{
  "environment": {
    "environment_key": "prod",
    "environment_name": "生产环境",
    "description": "线上生产环境",
    "sort_order": 0,
    "is_active": true
  }
}
```

**响应 data**：`{ "environment": {...} }`。


#### 更新环境

```http
POST /api/v1/environment/update
Content-Type: application/json

{
  "environment": {
    "environment_key": "prod",
    "environment_name": "生产环境（更新）",
    "description": "线上生产环境",
    "sort_order": 0,
    "is_active": true
  }
}
```

使用 environment 包装对象，不是扁平请求。


#### 删除环境

```http
POST /api/v1/environment/delete
Content-Type: application/json

{
  "environment_key": "prod"
}
```

有历史发布版本（含逻辑删除记录）的范围拒绝删除，返回 409 RELEASE_SCOPE_HAS_VERSIONS；冻结保护也可能阻止删除。


### 渠道管理 API

列表 data 为 total/list，单项 data 为 pipeline。


#### 获取渠道列表

```http
GET /api/v1/pipeline/list?environment_key=prod
```

**响应示例**：

```json
{
  "code": 200,
  "msg": "OK",
  "data": {
    "total": 1,
    "list": [
      {
        "environment_key": "prod",
        "pipeline_key": "main",
        "pipeline_name": "主渠道",
        "description": "生产发布渠道",
        "sort_order": 0,
        "is_active": true
      }
    ]
  }
}
```


#### 获取渠道详情

```http
GET /api/v1/pipeline/detail?environment_key=prod&pipeline_key=main
```

**响应 data**：`{ "pipeline": {...} }`。


#### 创建渠道

```http
POST /api/v1/pipeline/create
Content-Type: application/json

{
  "environment_key": "prod",
  "pipeline": {
    "environment_key": "prod",
    "pipeline_key": "main",
    "pipeline_name": "主渠道",
    "description": "生产发布渠道",
    "sort_order": 0,
    "is_active": true
  }
}
```


#### 更新渠道

```http
POST /api/v1/pipeline/update
Content-Type: application/json

{
  "environment_key": "prod",
  "pipeline": {
    "environment_key": "prod",
    "pipeline_key": "main",
    "pipeline_name": "主渠道（更新）",
    "description": "生产发布渠道",
    "sort_order": 0,
    "is_active": true
  }
}
```


#### 删除渠道

```http
POST /api/v1/pipeline/delete
Content-Type: application/json

{
  "environment_key": "prod",
  "pipeline_key": "main"
}
```

有历史发布版本时返回 409 RELEASE_SCOPE_HAS_VERSIONS；冻结范围也受写保护。


### 配置管理 API

这些接口操作编辑区。修改不会直接改变已发布快照；需要创建版本并切换生效。alias 创建后不可改，name 可改。content 在管理请求中为字符串，object/keyvalue 使用编码后的 JSON 字符串。


#### 获取配置列表

```http
GET /api/v1/config/list?environment_key=prod&pipeline_key=main
```

**响应示例**：

```json
{
  "code": 200,
  "msg": "OK",
  "data": {
    "total": 1,
    "list": [
      {
        "resource_key": "demo-key",
        "environment_key": "prod",
        "pipeline_key": "main",
        "name": "API 地址",
        "alias": "api_base_url",
        "content": "https://api.example.com",
        "type": "text",
        "remark": "后端 API 基础地址",
        "is_perm": false,
        "description": ""
      }
    ]
  }
}
```


#### 获取配置详情

```http
GET /api/v1/config/detail?environment_key=prod&pipeline_key=main&resource_key=demo-key
```

**响应 data**：`{ "config": {...} }`。


#### 创建配置

```http
POST /api/v1/config/create
Content-Type: application/json

{
  "config": {
    "environment_key": "prod",
    "pipeline_key": "main",
    "name": "API 地址",
    "alias": "api_base_url",
    "content": "https://api.example.com",
    "type": "text",
    "remark": "后端 API 基础地址",
    "is_perm": false,
    "description": ""
  }
}
```

**响应 data**：`{ "config": {...} }`，包含实际 resource_key。


#### 更新配置

```http
POST /api/v1/config/update
Content-Type: application/json

{
  "config": {
    "resource_key": "demo-key",
    "environment_key": "prod",
    "pipeline_key": "main",
    "name": "API 地址",
    "alias": "api_base_url",
    "content": "https://new.example.com",
    "type": "text",
    "remark": "后端 API 基础地址",
    "is_perm": false,
    "description": ""
  }
}
```

保留 config 包装及目标 resource_key；不要通过更新修改 alias。


#### 删除配置

```http
POST /api/v1/config/delete
Content-Type: application/json

{
  "environment_key": "prod",
  "pipeline_key": "main",
  "resource_key": "demo-key"
}
```

删除参数直接放顶层，不使用 config 包装。


### 静态资源 API


#### 获取资源列表

```http
GET /api/v1/asset/list?environment_key=prod&pipeline_key=main
```

**响应示例**：

```json
{
  "code": 200,
  "msg": "OK",
  "data": {
    "total": 1,
    "list": [
      {
        "file_id": "31111111-2222-4333-8444-555555555555",
        "environment_key": "prod",
        "pipeline_key": "main",
        "file_name": "banner.png",
        "content_type": "image/png",
        "file_size": 102400,
        "url": "/rainbow-bridge/api/v1/asset/file/31111111-2222-4333-8444-555555555555/banner.png",
        "remark": ""
      }
    ]
  }
}
```


#### 上传资源

```http
POST /api/v1/asset/upload
Content-Type: multipart/form-data
X-CSRF-Token: <会话令牌>

FormData:
- file: <binary>
- environment_key: prod
- pipeline_key: main
```

浏览器提交 FormData 时自动生成 boundary，不要手写不完整的 Content-Type。

**响应示例**：

```json
{
  "code": 200,
  "msg": "OK",
  "data": {
    "asset": {
      "file_id": "31111111-2222-4333-8444-555555555555",
      "environment_key": "prod",
      "pipeline_key": "main",
      "file_name": "banner.png",
      "content_type": "image/png",
      "file_size": 102400,
      "url": "/rainbow-bridge/api/v1/asset/file/31111111-2222-4333-8444-555555555555/banner.png",
      "remark": ""
    },
    "reference": "asset://31111111-2222-4333-8444-555555555555"
  }
}
```

配置使用返回的 reference。不要把别的范围的资源 URL 直接复制到当前范围，锁版检查会拒绝跨范围引用。


#### 下载原始资源

```http
GET /api/v1/asset/file/{file_id}/{file_name}
```

成功返回资源字节和对应 Content-Type。使用上传响应的实际 url；其中文件名及部署前缀不能随意省略。


### 运行时配置 API

业务应用可读取当前生效配置、固定读取指定版本、导出快照资源包。以下内容在本页直接说明；[运行时专题](runtime.md)提供单独阅读入口。

#### 读取当前生效版本

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

#### 固定读取指定版本

```http
GET /api/v1/runtime/release?version=v1.0
x-environment: default
x-pipeline: default
```

参数需 URL 编码。此时 `selection_mode` 为 fixed，不返回 `active_revision`、`activated_at`；其余结构同上。可读取已创建但当前未生效的版本；不依赖该范围是否已经设置 active。后续切换不会修改此版本的内容，但逻辑删除会使新请求返回 410。

不传 version 和 `?version=` 不同：后者明确提供了空版本号，返回 400。版本号仅在环境/渠道范围内解释，不是全局的虹桥程序版本。

#### 原文模式与大整数

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

#### 固定导出版本资源包

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

#### 快照资源下载

```http
GET /api/v1/release/asset/{snapshot_id}/{asset_id}
```

使用配置内容或版本详情实际返回的地址，不拼接原始编辑区路径。成功是原始文件字节和资源 Content-Type，服务端校验资源大小与 SHA-256。版本删除后新下载返回 410；资源缺失或完整性不符返回 503。业务读取本身检查引用文件存在和长度，资源下载时再核验完整 SHA-256。

#### 旧接口迁移

旧接口已经统一使用发布快照，**不再读取编辑区**。

| 接口 | 范围 | 行为 |
| --- | --- | --- |
| GET `/api/v1/runtime/config` | x-environment / x-pipeline 请求头 | 当前生效快照；保留旧 config 字段并增加版本元数据 |
| GET `/api/v1/runtime/static?environment_key=default&pipeline_key=default` | 查询参数 | 本次解析到的 active 资源包，不是固定某次之前展示的快照 |
| GET `/api/v1/runtime/overview` | 无 | 环境/渠道列表，不能当成生效版本配置 |

旧 config 的 data 为 snapshot_id、version、source_freeze_revision、active_revision、payload_sha256、selection_mode、environment、configs。每项 config 额外保留 environment_key、pipeline_key；object/keyvalue 返回 JSON，其余 content 为字符串。旧接口不提供 raw，也不承诺 activated_at。

旧 config/static 明确传 `version`、`expected_snapshot_id` 或 `content_format` 均返回 400，不能以为它们已支持新接口的选择参数。需要固定读取、raw 或固定导出时使用新接口。静态包格式也已改变，旧消费方应按 manifest 和相对资源路径适配。


### 配置迁移 API

这些管理接口操作编辑区，不会发布或切换生效版本。归档导入与发布静态 ZIP 不是相同格式，不能互换。


#### 获取导出树

```http
GET /api/v1/transfer/export-tree
```

**响应 data**：environments 数组；每项包含环境和 pipelines，每条渠道包含 configs、config_count 等。


#### 选择性导出

```http
POST /api/v1/transfer/export
Content-Type: application/json

{
  "format": "zip",
  "selections": [
    {
      "environment_key": "prod",
      "pipeline_key": "main",
      "resource_keys": [
        "demo-key"
      ]
    }
  ]
}
```

返回归档字节，format 可为 zip 或 tar.gz。selections 中 pipeline_key 为空表示该环境全部渠道，resource_keys 为空表示该渠道全部配置。入口是 POST /transfer/export。


#### 导入配置或归档

```http
POST /api/v1/transfer/import
Content-Type: application/json
```

```json
{
  "configs": [
    {
      "resource_key": "demo-key",
      "environment_key": "prod",
      "pipeline_key": "main",
      "name": "API 地址",
      "alias": "api_base_url",
      "content": "https://api.example.com",
      "type": "text",
      "remark": "后端 API 基础地址",
      "is_perm": false,
      "description": ""
    }
  ],
  "overwrite": false
}
```

也支持 multipart/form-data 的 file、overwrite。归档按自身范围信息导入，不把当前页面范围当成任意重定向参数。成功 data 包含 total、environment_keys、pipeline_keys、items，归档资源结果另见 resource_import。


#### 导入预览

```http
POST /api/v1/transfer/import-preview
Content-Type: multipart/form-data

FormData:
- file: <export.zip>
```

只读预览，不写配置。成功 data 包含 format、environments、summary；summary 含 total_environments、total_pipelines、total_configs、total_assets、new_count、existing_count、conflict_count。


#### 选择性导入

```http
POST /api/v1/transfer/import-selective
Content-Type: multipart/form-data

FormData:
- file: <export.zip>
- overwrite: false
- selections: [{"environment_key":"prod","pipeline_key":"main","resource_keys":["demo-key"]}]
```

selections 是 JSON 字符串。成功后检查 data.resource_import 的 status、expected、restored、reused、failed、issues，不能只凭 HTTP 200 判断所有资源恢复。


#### 配置迁移

```http
POST /api/v1/transfer/migrate
Content-Type: application/json

{
  "source_environment_key": "dev",
  "source_pipeline_key": "main",
  "target_environment_key": "prod",
  "target_pipeline_key": "main",
  "resource_keys": [
    "demo-key"
  ],
  "overwrite": false
}
```

**响应 data**：total、succeeded、skipped、failed、items。items 每项包含 resource_key、name、alias、status、message。目标被冻结时拒绝写入；迁移后的资源引用应归属目标范围。


### 版本管理 API

本节是虹桥程序的构建版本，不是配置快照版本。


#### 获取版本信息

```http
GET /api/v1/version
```

**响应示例**：

```json
{
  "code": 200,
  "msg": "OK",
  "data": {
    "version_info": {
      "version": "dev",
      "git_commit": "<构建提交>",
      "build_time": "<构建时间>"
    }
  }
}
```


#### 获取最新程序发行信息

```http
GET /api/v1/version/latest
```

成功 data.release_info 包含 tag_name、name、published_at、html_url、prerelease、body。当前实现默认查询 yi-nology/rainbow_bridge 的上游发行信息，不能据此判断 Fork 的 main_v1 已更新；内网模式返回对应提示。


#### 健康检查

```http
GET /ping
```

路径位于部署前缀下，不在 /api/v1 中。


### 锁版管理 API

以下管理操作需要 Cookie 会话；写操作附 CSRF。


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


### 配置版本发布 API

版本管理在当前环境/渠道内创建不可变快照，与程序构建版本不同。


#### 管理读取

| 方法与路径 | 查询参数 | 成功 data 结构 |
| --- | --- | --- |
| GET `/api/v1/release/list` | environment_key、pipeline_key、limit、before_id | items（每项 meta/is_active）、has_more、next_before_id、独立 active |
| GET `/api/v1/release/detail` | environment_key、pipeline_key、version | meta、configs、assets、active |
| GET `/api/v1/release/active` | environment_key、pipeline_key | active；未设置时 HTTP 200，active=null |
| GET `/api/v1/release/history` | environment_key、pipeline_key、limit、before_event_id | items、has_more、next_before_event_id |

limit 缺省 20、最大 100；游标是接口返回的十进制字符串。版本列表按创建倒序，不显示逻辑删除项；active 独立于当前页，不应假设当前页必然包含生效版本。详情 configs 的 content 始终是字符串；assets/external_references 等空数组按实际显式 JSON 返回 `[]`。

meta 包含 snapshot_id、version、environment_key、pipeline_key、环境/渠道名、source_freeze_revision、creation_request_id、config_count、asset_count、asset_bytes、payload_sha256、manifest_sha256、external_references、reason、actor、created_at。active 包含 snapshot_id、version、revision、changed_at、changed_by、changed_reason。

#### 创建版本

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

#### 切换生效（含切回）

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

#### 删除非生效版本

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

#### 原因、幂等和超时确认

创建、切换、删除的 reason 与锁版相同：选填、trim、最多 500 个 Unicode 字符。request_id 必须是 UUID；同一次操作结果不确定时保留整份首次 payload，用同一 ID 原样确认。改变范围、目标、原因或 revision 等字段就是新操作，应使用新 ID。同一操作内 ID 重用但字段不同返回 409 RELEASE_REQUEST_CONFLICT。

创建重放在版本后来被删除后返回 410；切换重放确认历史操作而不重新切回；删除相同请求可重放原删除结果。不要把“网络超时”理解成“服务端一定没执行”，也不要为了重试改掉已提交的字段。


## 🔐 认证与授权


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


## 📊 限流与配额

| 项目 | 当前限制 |
| --- | --- |
| 登录尝试 | 每进程每来源 IP 每分钟 5 次，超限 429 并带 Retry-After |
| 密码计算 | 每进程并发 2，繁忙返回 503 AUTH_BUSY |
| 登录/改密 JSON | 最多 4 KiB，拒绝未知字段 |
| 发布列表/历史分页 | 默认 20，最大 100 |
| 静态资源包 | 未压缩输出最多 192 MiB，构包总期限 60 秒 |

登录限流不是集群全局配额，也不解析自报 X-Forwarded-For 来放宽来源限制。部署网关可另设限流，不应将网关配置描述成虹桥已经实现的能力。创建快照还可能触发配置、资源等发布限额，413 响应按实际 limits 字段判断。


## 🐛 错误处理

### 错误响应示例

```json
{
  "code": 404,
  "msg": "error",
  "error": "scope prod/main has no active release",
  "error_type": "RELEASE_NOT_ACTIVE"
}
```

认证错误通常使用 msg 说明，运行时/发布还可包含 error。冻结和发布返回真实 HTTP 状态；历史 CRUD 还要检查正文 code。

### 常见错误码

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

管理操作另有 SCOPE_FROZEN、FREEZE_RESOURCE_PRECHECK_FAILED、FREEZE_REVISION_CONFLICT、RELEASE_SOURCE_NOT_FROZEN、RELEASE_SOURCE_REVISION_CONFLICT、RELEASE_ACTIVE_REVISION_CONFLICT、RELEASE_REQUEST_CONFLICT、RELEASE_DELETE_ACTIVE、RELEASE_SCOPE_HAS_VERSIONS 等；具体触发和最新状态字段见前文对应接口。


## 💡 最佳实践

### 1. 批量读取

业务应用使用 runtime/release 一次读取该范围的 configs，不遍历管理接口拼装生效配置。需要固定版本时显式传 version；含大整数的结构化配置优先考虑 raw 并进行无损处理。

### 2. 缓存策略

这些快照读取与导出使用 `Cache-Control: no-store`。跟随 active 的接入方不能无条件缓存五分钟后再称“实时切换”；如果自行设计缓存，应明确刷新周期及可接受的生效延迟。固定版本内容不可修改，但逻辑删除会阻止新的服务端读取；客户端已下载的副本无法被远程撤回。

### 3. 错误重试

重试仅用于网络故障和适当的临时服务错误，不能盲目重试 400/404/409/410。失败不应被客户端悄悄替换为编辑区配置。

创建、切换、删除结果不确定时，用同一个 request_id 原样重发整份 payload；不要改了原因或 revision 又沿用同一 ID。409 刷新并核对最新状态，401 重新登录，不自动恢复旧 Bearer。

### 4. 管理操作顺序

编辑配置 → 读取锁版状态并锁版 → 用锁定后的 revision 创建版本 → 读取 active revision 并切换 → 用公开运行时接口确认。创建版本本身不自动生效；下一轮编辑前解锁，版本清理只删除非生效项。

## 🔗 相关链接

- [运行时接口详细说明](runtime.md)
- [管理接口详细说明](management.md)
- [IDL 请求与路由定义](../../../idl/biz)（不是已生成的 OpenAPI）
- [HTTP 契约测试](../../../biz/handler/release/release_http_test.go)
- [Fork GitHub Issues](https://github.com/MalfuRisa/rainbow_bridge/issues)

本文按当前 main_v1 后端实现维护。GitHub Markdown 更新与文档站部署分别生效；首页“查看完整 API”指向本页即可阅读全部主要接口，不需要先跳到另一篇才能了解运行时和发布功能。
