# 交付说明:配置版本快照与发布切换(config-release-v1)

对应 PRD:[config-release-v1.md](./config-release-v1.md)。本文件只记录**已实现的行为、真实执行的验证与仍存的边界**,不重复 PRD 的动机说明。

提交整理（2026-10-19）：最终实现及回归已提交为 `adc6df19ca52609e1344cfbfb5acf7045d3eab48`，位于 `codex/config-freeze`；文档随本次单独提交归档。推送目标为用户Fork `https://github.com/MalfuRisa/rainbow_bridge`，不向原上游提PR。提交前新增的验证记录见[复核文档第10节](./config-release-review.md)。

## 1. 交付概览

| 阶段 | 内容 | 位置 |
| --- | --- | --- |
| M1 | 表结构、DAO、错误契约、独立快照存储、启动迁移 | `biz/dal/model/release.go`、`biz/dal/db/release_dao.go`、`biz/service/release_types.go`、`biz/service/release_errors.go`、`biz/service/release_storage.go`、`initialize.go` |
| M2 | 快照捕获(排序/富文本媒体边界解析与引用重写/摘要)、创建编排(幂等、限额、回滚)、固定版本读取与资源下载 | `biz/service/release_snapshot.go`、`biz/service/release_html.go`、`biz/service/release_service.go`、`biz/service/release_runtime.go` |
| M3 | 生效指针 CAS 切换、active 查询、历史审计、父级删除保护 | `biz/service/release_service.go`(ActivateRelease/ReleaseActive/ReleaseHistoryPage)、`biz/service/pipeline_service.go`、`biz/service/environment_service.go` |
| M4 | IDL 与生成代码、handler 绑定与错误映射、版本发布页与四个弹窗、侧边栏入口 | `idl/biz/release.proto`、`biz/model/release/`、`biz/router/release/`、`biz/handler/release/release_service.go`、`vue/src/views/ReleasesPage.vue`、`vue/src/components/release/`、`vue/src/stores/release.ts`、`vue/src/lib/api/release.ts` |
| M5 | Go 单测(含复核 F1/F2/F5 与 R09/R13/R17 回归)、Playwright `@release` 规格(含韧性规格)、双进程/崩溃/恢复脚本、交付说明 | `biz/service/release_service_test.go`、`biz/service/release_review_test.go`、`tests/e2e/local/release/`、`script/release-two-process.ps1`、`script/release-crash.ps1`、`script/release-recovery.ps1` |

原锁版能力保持不变:冻结表、写拦截、`/api/v1/freeze/*` 与页面入口都未改动语义,只增加了“发布版本存在则拒绝删除环境/渠道”的一条保护。

## 2. 实际对外契约

### 2.1 接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/v1/release/create` | 从**已冻结**范围创建不可变版本;需 `Authorization: Bearer <管理令牌>` |
| POST | `/api/v1/release/activate` | 切换生效版本(可前可后);需管理令牌 |
| GET | `/api/v1/release/list` | 版本列表(创建倒序),`limit`+`before_id` 游标分页,同时返回 `active` |
| GET | `/api/v1/release/detail` | 单版本元数据、配置内容与资源清单(只读) |
| GET | `/api/v1/release/active` | 当前生效指针(无则 `null`) |
| GET | `/api/v1/release/history` | 创建/切换审计(`before_event_id` 游标) |
| GET | `/api/v1/runtime/release` | 业务读取:带范围头,无 `version` 跟随生效版本,带 `version` 读固定版本 |
| GET | `/api/v1/release/asset/{snapshot_id}/{asset_id}` | 快照资源下载(完整 SHA-256 校验后才返回字节) |

旧接口 `GET /api/v1/runtime/config` 语义不变,仍读编辑区。

### 2.2 成功响应形状(显式契约)

生成模型带 `omitempty`,空数组与零值计数会被省略,前端按字段必然存在读取会崩溃;因此**成功响应统一由 handler 侧显式结构序列化**:

- 数组字段始终存在,空值为 `[]`(`items`/`configs`/`assets`/`external_references`/`resource_issues`);
- 计数字段始终存在,空值为 `0`(`config_count`/`asset_count`/`asset_bytes`);
- 字符串字段始终存在(`next_before_id`、`payload_sha256` 等),缺失时为空串;
- `active` 无生效版本时为 `null`。

响应示例(固定版本读取):

```json
{"code":200,"msg":"OK","data":{"snapshot_id":"...","version":"v1.0","source_freeze_revision":"3",
 "payload_sha256":"...","selection_mode":"fixed",
 "environment":{"environment_key":"qa_prod","environment_name":"QA Prod","pipeline_key":"main","pipeline_name":"QA main"},
 "configs":[{"resource_key":"...","alias":"A","name":"a","type":"text","content":"v","remark":"","is_perm":false,"description":""}]}}
```

`object`/`keyvalue` 类型在 `runtime/release` 中 `content` 为 JSON 对象且保留数字原文(`json.Decoder.UseNumber()`,不做浮点转换);`release/detail` 中 `content` 为字符串,同样保精度。

### 2.3 错误契约

HTTP 状态 + 业务码 + `error_type` 三段一致,`error_type` 取值:

| error_type | HTTP | 触发 |
| --- | --- | --- |
| `RELEASE_INVALID_REQUEST` | 400 | 版本号/原因/request_id/revision/游标非法 |
| `RELEASE_SCOPE_NOT_FOUND` | 404 | 环境或渠道不存在 |
| `RELEASE_VERSION_NOT_FOUND` | 404 | 范围内无该版本(含跨范围读取) |
| `RELEASE_NOT_ACTIVE` | 404 | 未设置生效版本时读 `runtime/release` |
| `RELEASE_ASSET_NOT_FOUND` | 404 | 快照资源不存在 |
| `RELEASE_SOURCE_NOT_FROZEN` | 409 | 编辑区未冻结就创建 |
| `RELEASE_SOURCE_REVISION_CONFLICT` | 409 | `expected_freeze_revision` 与当前冻结 revision 不一致 |
| `RELEASE_VERSION_EXISTS` | 409 | 同范围同版本号已存在 |
| `RELEASE_REQUEST_CONFLICT` | 409 | 同 `request_id` 携带不同字段 |
| `RELEASE_ACTIVE_REVISION_CONFLICT` | 409 | `expected_active_revision` 落后(响应含 `latest_active`) |
| `RELEASE_ACTIVE_UNCHANGED` | 409 | 目标已是当前生效版本 |
| `RELEASE_RESOURCE_INVALID` | 409 | 预检失败(响应含 `resource_issues` 清单) |
| `RELEASE_SCOPE_HAS_VERSIONS` | 409 | 删除仍有版本的环境/渠道 |
| `RELEASE_LIMIT_EXCEEDED` | 413 | 超限额(响应含 `limits.item/limit/actual`) |
| `RELEASE_STORAGE_UNSUPPORTED` | 503 | 当前源存储为 MinIO 等不受支持类型(仅新建路径;已提交结果的幂等确认不受影响) |
| `RELEASE_INCOMPLETE` | 503 | 已提交版本的文件缺失或摘要不符 |
| `RELEASE_STORAGE_ERROR` | 500 | 快照目录不可用(如相对路径、越界路径) |
| `RELEASE_INTERNAL_ERROR` | 500 | 其他内部错误 |

管理令牌相关沿用冻结侧契约:`FREEZE_ADMIN_NOT_CONFIGURED` / `FREEZE_UNAUTHORIZED`,写协调繁忙见 `FREEZE_WRITE_GATE_BUSY`(503)、`FREEZE_WRITE_TIMEOUT`(503)。

### 2.4 幂等与并发

- 创建与切换都以 `request_id`(UUID)幂等:同请求重放返回**原结果**并置 `replayed=true`,不重复复制资源、不重复写审计;同 `request_id` 不同字段 → 409 `RELEASE_REQUEST_CONFLICT`。
- 请求指纹为 `rainbow-release-v2|<op 长度>:<op>|<字段长度>:<字段>...` 的 SHA-256(长度前缀编码,字段边界明确,环境 `a|b`+渠道 `c` 与 `a`+`b|c` 不会碰撞);**不含令牌**。
- 幂等确认先于源存储能力、快照目录与冻结状态的校验:已提交结果在任何后续配置变化下都能被确认(源存储改 MinIO、快照根目录不可用、范围已解锁都不影响确认)。
- 唯一索引:`(event_type, request_id)`、`(environment_key, pipeline_key, version)`、`(snapshot_id, asset_id)`、`(snapshot_id, source_file_id)`。
- 生效指针用 CAS:`UPDATE ... WHERE revision = expected`;失败返回 409 并回传最新 `latest_active`。
- 所有发布写操作在**冻结写协调闸门**内串行:先有界取连接(10s,超时 `FREEZE_WRITE_GATE_BUSY`),整事务上限 60s(超时 `FREEZE_WRITE_TIMEOUT`)。

### 2.5 限制与存储布局

| 项 | 值 |
| --- | --- |
| 单范围最大配置数 | 5000 |
| 快照 payload | ≤ 64 MiB |
| 去重资源数 | ≤ 500 |
| 单份资源 | ≤ 32 MiB |
| 资源合计 | ≤ 100 MiB |
| 列表默认/最大页 | 20 / 100 |

- 快照根目录:环境变量 `RAINBOW_RELEASE_DIR`(必须是绝对路径),缺省 `<工作目录>/data/release-snapshots`。
- 布局:`<root>/<snapshot_id>/assets/<asset_id>.bin`;准备目录 `<root>/.staging/<snapshot_id>` 仅在未提交时存在。
- 快照内容把受管引用改写为 `release-asset://<asset_id>`,读取时渲染成 `${basePath}/api/v1/release/asset/<snapshot>/<asset_id>`,保留原 query/fragment。
- 富文本按标签/属性边界解析引用(见 §2.7),受管引用的捕获、重写与渲染共用同一套扫描规则。
- 版本不可修改、不可覆盖、本版不提供删除接口。
- **多实例部署要求**:共享同一快照根目录,否则切换校验会在其他实例上失败。

### 2.6 测试专用钩子

`RAINBOW_RELEASE_CRASH_POINT` ∈ {`during_copy`,`before_commit`,`after_commit`} 会在对应位置 `os.Exit(97)`,**仅用于崩溃点验证**,行李目录残留因此可复现。生产环境不要设置该变量。

另有三个仅在测试内赋值的包级暂停钩子(默认 nil,生产不启用):`releaseReadPauseHookForTest`(读取生效版本时,拿到版本行之后、读快照之前)、`releaseCopyPauseHookForTest`(复制资源期间)、`resourceRestoreHookForTest`(冻结批次的资源恢复),用于构造确定性并发时序(R13/R17 回归)。

### 2.7 富文本引用规则

富文本(`richtext`)按标签与属性边界扫描,捕获(改写为标记)与读取(渲染为版本 URL)共用同一套规则,覆盖带/不带引号的属性值、`srcset` 候选列表、`poster`、`style` 属性与 `<style>` 块中的 `url(...)` 及 `@import "..."`:

| 引用位置 | 受管引用(`asset://` / 受管路由) | 绝对或相对协议外部地址 | 相对路径等无法定位 | 内联 `data:` / 纯 fragment |
| --- | --- | --- | --- | --- |
| 嵌入媒体:`src`/`srcset`/`poster`/`background`/样式 `url()`/`@import` | 改写为快照标记 | **拒绝创建** 409(`external_reference`) | **拒绝创建** 409(`unresolvable_reference`) | 原值保留 |
| 普通业务超链接:`<a>`/`<area>` 的 `href` | 改写为快照标记 | 原值保留并计入 `external_references` 提示 | 原值保留 | 原值保留 |

其余 `href`(如 `<link>`、`<base>`)按嵌入资源处理。拒绝时创建失败并返回问题清单(含配置别名与原因),不产生任何版本。

当前不支持先将HTML实体解码后再解析CSS，例如 `style="background-image:url(&quot;asset://X&quot;)"`。此类形式按无法安全定位的资源拒绝，不作为已支持的受管媒体往返格式；复核在快照捕获预检中确认拒绝。使用普通匹配引号形式可正常捕获与渲染。

## 3. 验收场景逐项结果

图例:✅ 本轮真实执行通过;◐ 有替代证据但未按 PRD 原文逐步执行;⛔ 本轮未执行。

| 场景 | 结果 | 证据 |
| --- | --- | --- |
| R01 旧库迁移幂等、head 视为 0 | ✅ | 崩溃/恢复脚本对同一数据库反复重启;`T01` 重启检查;`initialize.go` 对 5 张表 AutoMigrate |
| R02 未锁版/过期 revision/令牌错误 | ✅ | `TestReleaseCreateRequiresFrozenScopeAndRevision`;`@release` API 规格校验段;冻结套件令牌用例 |
| R03 锁定后创建、空渠道快照 | ✅ | `TestReleaseEmptyAndAssetlessVersions`(空渠道 `configs=[]` 可创建)、`TestReleaseSnapshotPayloadAndFixedRuntime`(含资源与外部 URL)、`-Suite Release` 端到端创建 |
| R04 解锁修改后快照内容/资源字节保持 | ✅ | `TestReleaseIndependentFromEditorAndSourceFiles` |
| R05 再建 v1.1 并 1.0→1.1→1.0 切换 | ✅ | `@release` 页面规格;`TestReleaseActivateLifecycleCASAndAudit` |
| R06 双进程并发创建同版本 | ✅ | `script/release-two-process.ps1`:不同 `request_id` 一胜一 409 `RELEASE_VERSION_EXISTS`;相同 `request_id` 并发两处 200 且同一 `snapshot_id`、只新增一份快照 |
| R07 双进程并发切换 | ✅ | 同上:一胜一 409,head revision=1,切换事件恰 1 条,P2 立即读到新指针 |
| R08 创建重放与字段冲突 | ✅ | `TestReleaseCreateIdempotencyAndConflicts`;`@release` API 规格 |
| R09 切换响应丢失后重放 | ✅ | `TestReleaseActivateReplayAfterFurtherSwitches`(激活 v1.0 后切到 v1.1,再重放原请求:返回原 applied_revision=1、最新 active=v1.1/rev2,且不切回);`release-resilience` 页面规格的切换用例同向验证 |
| R10 冲突/校验/分页/跨范围 | ✅ | `@release` API 规格(含 `before_id=abc`→400、`limit=1` 游标、跨范围 404、`active=null`、空数组字段齐全) |
| R11 资源预检与嵌入媒体 | ✅ | 受管引用的 6 类问题原因由 `TestReleaseReferenceRewriteAndRejections` 覆盖;富文本嵌入媒体的外部地址与相对路径在 `TestReleaseRichTextMediaRejections` 中逐项拒绝(复核 F1 修复后),普通业务超链接仍原值保留并提示 |
| R12 大整数/精度 | ✅ | `TestReleaseSnapshotPayloadAndFixedRuntime`;恢复脚本按原始响应文本校验 `9007199254740993` |
| R13 复制中并发写(解锁/改配置/锁版) | ✅ | `TestReleaseCreateDuringConcurrentWrites`:在复制资源的暂停点并发发起解锁/改配置/重新锁版三种写,三者被写闸门串行化(全部成功或有界 503/409),快照 payload 仍是创建时那个 revision 的内容、不含并发修改,`source_freeze_revision` 保持 1,编辑区与冻结 revision 各自正确前进 |
| R14 复制/校验/提交失败后的清理 | ◐ | `TestReleaseLimitsAndStorageRules`(超限不留完整版本)与崩溃脚本 `before_commit`(残留目录可定位);**磁盘无权限/校验失败的错误注入未单独构造**(不接受“按代码保证”替代运行) |
| R15 三个崩溃点 | ✅ | `script/release-crash.ps1`:复制中/提交前退出后**无任何可读版本且固定读取 404**;提交后退出可用同 `request_id` 确认结果,资源文件未被误删 |
| R16 限额与写协调持有 | ◐ | 超限 413、稀疏文件预检、相对目录 500、复制中并发写的有界排队已测;恰在边界值的等值用例未逐项执行 |
| R17 active 读取中并发切换 | ✅ | `TestReleaseActiveReadDuringSwitchKeepsOldSnapshot`:在“已读 head、未读快照”窗口内完成切换,在途响应仍是旧版本的版本号/摘要/URL,旧资源字节不变,随后新请求读到 v1.1 |
| R18 缺文件/同长度篡改/payload 篡改 | ✅ | `TestReleaseRuntimeIntegrityFailures`;恢复脚本缺文件 → 503 且不回退编辑区 |
| R19 前缀、越界路径、query/fragment | ◐ | `@release` 套件在 `/rainbow-bridge` 前缀实例下运行;单测覆盖重写后的 URL 仅带一个前缀、越界路径拒绝;`..`/符号链接用例未逐项枚举 |
| R20 旧快照切换与父级删除保护 | ◐ | 页面规格验证解锁后切换旧版仍可用;API 规格验证有版本时 409;普通导入不覆盖版本表未单独验证(导入只操作配置/资源表) |
| R21 页面完整流程 | ✅ | `@release` 页面规格:创建不自动生效、详情弹窗、首次生效、切新版本、切回旧版、两类地址文案、无修改/删除按钮 |
| R22 范围隔离与令牌不落地 | ◐ | 页面规格:切换渠道后列表清空不串状态、`page.content()` 不含令牌;新增韧性规格覆盖 A→B→A 迟到响应与响应丢失后的状态漂移。**仍未单独构造**:弹窗内切范围、另一管理员先切换、令牌不入日志的端到端断言 |
| R23 旧→新→固定三态读取 | ✅ | 页面规格 + 恢复脚本:旧接口读编辑区、新接口跟随切换、固定版本不跟随 |
| R24 备份恢复 | ✅ | `script/release-recovery.ps1`:停写后复制 DB(含 WAL/SHM)与快照目录,恢复实例元数据/摘要/资源字节/active/事件数逐项一致;缺文件明确 503,补回文件免重启恢复 |
| R25 Redis 关闭/MinIO | ◐ | 所有用例均在 `redis.enabled=false` 下运行;**未起真实 MinIO 实例**;源存储为 MinIO 时新建返回 503、已提交结果仍可确认,这两条由 `TestReleaseReplayIndependentOfSourceChanges` 通过切换服务配置覆盖,不代表真实 MinIO 客户端路径已运行 |
| R26 既有 F01–F24 与 Full/Freeze 回归 | 见第 4 节 | 本轮重新执行,不引用旧报告 |

## 4. 本轮回归证据

各项证据的运行批次见下表，复用时点见第5节；Freeze与Full保留上一轮结果，不计为本轮新执行。证据目录为 `C:\Users\MalfuRisa\Desktop\产出\05-虹桥\附件\本机测试\<运行 id>\`。

| 验证 | 命令 | 结果 |
| --- | --- | --- |
| Go 单测 | `go test -mod=readonly -tags=dev -count=1 ./biz/...` | ok(含 12 个 `TestRelease*` 与复核回归 `TestReleaseRichText*`/`TestReleaseFingerprint*`/`TestReleaseCrossScope*`/`TestReleaseReplayIndependent*`/R09/R17) |
| 静态检查 | `go vet -tags=dev ./biz/... ./pkg/...` | 无输出 |
| 前端类型检查 | `cd vue && npx vue-tsc -b --pretty false` | 通过(仓库没有名为 `type-check` 的 npm 脚本,以 `vue-tsc -b` 为准) |
| Release 专项 | `script/test-local.ps1 -Suite Release` | **6/6 通过**(`20261019-170551-fywl`):既有 API/页面规格 + 韧性规格四条(创建确认、切换确认、迟到数据、旧批次加载态) |
| 冻结回归 | `script/test-local.ps1 -Suite Freeze` | 4/4 通过(`20261019-154833-truh`) |
| 主回归 Full | `script/test-local.ps1 -Suite Full` | **实际 35 次执行通过**(`20261019-154916-zcxa`):主套件 26 + 三次重启各 1 + 恢复 1 + 根路径 5,全部 `pw-*.json` 报告汇总 passed=35 failed=0,各阶段退出码均为 0(此前只写 26 是漏统计补充阶段) |
| 双进程 | `script/release-two-process.ps1` | 通过(`rainbow-release-2p-20261019-170723-cjyu`) |
| 崩溃点 | `script/release-crash.ps1` | 通过(`rainbow-release-crash-20261019-170735-joax`) |
| 备份恢复 | `script/release-recovery.ps1` | 通过(`rainbow-release-recovery-20261019-170750-nvix`) |

套件分工:`-Suite Full` 跑 T01–T25 与 opt02(排除 `@freeze`/`@release`),另外还有重启/恢复/根路径三个补充阶段;`-Suite Freeze` 跑冻结 4 条;`-Suite Release` 跑发布 6 条。三者合起来才覆盖“原行为 + 新功能”。

### 4.1 研发轮实际发现并修复的缺陷

| # | 现象 | 根因 | 修复 |
| --- | --- | --- | --- |
| 1 | 空范围(无资源)创建版本失败:`rename ...\.staging\<id> ... The system cannot find the path specified` | 暂存资源目录只在复制资源时才创建 | `materializeRelease` 在复制前 `MkdirAll(.staging/<snapshot>/assets)`;新增 `TestReleaseEmptyAndAssetlessVersions` 回归 |
| 2 | 详情弹窗整块消失,控制台报 `Cannot read properties of undefined (reading 'length')` | 生成模型 `omitempty` 省略空数组与零值计数,前端按字段必然存在读取 | 成功响应改显式 JSON 结构(§2.2)+ 前端 `?? []`/`?? 0` 兜底;新增空值断言 |
| 3 | 版本列表“内容”列显示 `2 配置 ·`(缺资源数) | 同上(`asset_count=0` 被省略) | 同上 |
| 4 | 切换到**更新**版本时按钮与弹窗都写“切回此版本”,页面流程卡在找不到确认按钮 | 文案只看“是否已有生效版本”,未比较版本新旧 | 新增 `isOlderThanActive`(按列表创建倒序判断),列表按钮与弹窗共用 |

### 4.2 复核轮发现并修复的 5 项(F1–F5)

| # | 现象(复核复现) | 根因 | 修复与回归 |
| --- | --- | --- | --- |
| F1/P1 | `srcset`、无引号 `src`、`poster`、协议相对地址、`style` 内 `url()` 五类嵌入媒体被当成普通文字放过,资源数 0、外部提示为空 | 富文本只用一条正则匹配带引号的 `src`/`href` | 新增 `release_html.go` 按标签/属性边界扫描(覆盖单/双引号与无引号值、`srcset` 候选、`poster`、`style` 属性与 `<style>` 块的 `url()` 与 `@import "..."`);**嵌入媒体的外部地址与相对路径一律拒绝创建**,普通 `<a>`/`<area>` 超链接保留原值并提示;捕获与渲染共用同一扫描器。回归:`TestReleaseRichTextMediaRejections`(18 例)、`TestReleaseRichTextManagedMediaRoundTrip`(13 处受管媒体往返)、`TestReleaseRichTextBusinessHyperlinkStaysLiteral`、`TestReleaseRichTextScanBoundaries`。**第一轮修复后复核又发现三个解析缺口(F1-a/F1-b/F1-c,见 §4.3),均已修好** |
| F2/P2 | 环境 `a\|b`+渠道 `c` 与环境 `a`+渠道 `b\|c` 指纹相同,第二个范围拿到第一个范围的发布结果(replayed=true) | 指纹用 `\|` 直接拼接字段,边界可被字段内容制造 | 改长度前缀编码 `rainbow-release-v2\|<len>:<op>\|<len>:<field>...`,创建与切换共用。回归:`TestReleaseFingerprintFieldBoundariesDoNotCollide`(含操作区分)、`TestReleaseCrossScopeRequestIDIsConflict`(端到端 409 + 真重放仍可用) |
| F3/P2 | “确认结果”只保留 `request_id`,revision 实时读取;后台状态前进后同一请求内容变化,后端按冲突拒绝确认 | 弹窗重试时重新读取可编辑表单与实时 revision | 两个弹窗在首次提交时固定**整份 payload**,重试原样重发(只允许重新输入凭据);修改目标/原因/范围后重试按钮消失,按新操作提交;创建弹窗的确认不受当前冻结状态限制。回归:`release-resilience.spec.ts` 前两条(断言两次请求体逐字段相等) |
| F4/P2 | A→B→A 后第一次 A 的迟到响应覆盖第二次 A 的新列表与生效状态 | store 只校验范围键,没有请求批次概念 | store 增加批次号:范围变化、显式刷新与创建/切换后的刷新都会开启新批次,列表/分页/active/error 只能由当前批次写入;`loading` 用当前批次的进行中计数,新批次会作废旧批次的加载态。回归:`release-resilience.spec.ts` 第三条(扣住第一次 A 的响应,放回后页面仍是新状态)。**第一轮修复后复核发现旧批次的 finally 仍会扣减新批次计数(见 §4.4),已在本轮修好** |
| F5/P2 | 已创建成功的版本在源存储改为 MinIO 后用原请求确认,被提前拒绝 `RELEASE_STORAGE_UNSUPPORTED` | 源存储能力与快照目录解析安排在幂等查询之前 | 创建与切换都改成**先查已提交结果**再校验源存储能力/快照目录/冻结状态;清理逻辑改为 storage 为 nil 时跳过。回归:`TestReleaseReplayIndependentOfSourceChanges`(MinIO 与不可用快照目录下的确认、新请求仍 503/500)、`release-resilience` 创建用例(解锁后确认成功) |

另:存储不支持的契约是 **503**(代码原本即 503,交付说明此前误写 409,已纠正);`RELEASE_STORAGE_ERROR`(快照根目录不可用)为 500。

### 4.3 复核第二、三轮修正(F1 剩余解析缺口与 F4 计数)

第一轮修复后复核继续检查,确认 F2/F3/F5 关闭,但 F1、F4 仍留缺口;修完后复核又发现 F1 的 CSS 带引号分支仍未修(F1-c)。下列四项均已修并补回归(F1-c 为追加):

| # | 现象(复核复现) | 根因 | 修复与回归 |
| --- | --- | --- | --- |
| F1-a/P1 | `<img alt=">" src="https://cdn.example.com/a.png">` 这类合法 HTML 里,引号内的 `>` 让属性提前结束,后面的外部图片被放过;Chromium 实际会加载该图片 | 扫描器在引号内也把 `>` 当作终止符(为容错未闭合引号而加) | 引号内只以匹配引号结束;未闭合引号则把余下内容作为值交给整段判定(受管地址→`unresolvable_reference` 拒绝,外部地址→`external_reference` 拒绝)。回归:`quoted_gt_before_external_src`、`quoted_gt_text_before_external_src`、`quoted_gt_in_style_media` 三个拒绝用例,以及往返用例中的 `<img alt="a > b" src="asset://…">`(捕获+渲染都正确) |
| F1-b/P1 | `<img srcset="#local, https://cdn.example.com/a.png 2x">` 中第二个外部候选被吞掉,创建成功且提示为空 | 读完第一个候选、裁掉结尾逗号后,代码继续"跳过描述符直到下一逗号",把后面的候选一并跳过 | 区分两种状态:URL 词元以逗号结尾即"该候选已结束",直接继续读下一候选;否则才跳过描述符。回归:`srcset_fragment_first_then_external`、`srcset_two_candidates_no_descriptor_then_external` 两个拒绝用例,以及往返用例中的 `<img srcset="asset://A, asset://B 2x">`(首项不带描述符时两个受管候选都被重写) |
| F4/P2 | 旧批次请求结束时扣减了新批次的 `pending`:切到 B 且 B 未返回时只放回旧 A,loading 立即变 false,页面可能提前显示"暂无版本" | `loadFirstPage`/`loadMore` 的 finally 无条件扣减计数 | 只有请求仍属于当前批次(范围+批次都匹配)才扣减;旧批次已被 `beginBatch` 归零,不再影响当前计数。回归:`release-resilience.spec.ts` 第四条(扣住 A 与 B 两个请求,只放回 A 时断言空态仍未出现,放回 B 后才出现) |
| F1-c/P1 | 合法 CSS `url(")banner.png")` 里的 `)` 被当成值终止符,该引用被当作空值跳过,相对图片既不拒绝也不提示 | 上一轮只修了 HTML 属性侧的引号规则,`scanReleaseCSSURLValue` 的带引号分支仍在引号内以 `)` 结束(交付说明当时写成 "已顺带修复" 属于过早结论,复核已指出) | CSS 字符串内的 `'`/`"` 只在匹配引号处结束,闭合括号在引号之后再找;未闭合引号则值延伸到片段末尾交由整段判定拒绝。回归:`css_url_quoted_relative_with_paren`、`css_url_quoted_external_with_paren`、`css_url_quoted_protocol_relative_with_paren`、`style_block_quoted_relative_with_paren` 四个拒绝用例,以及往返用例中的 `url('asset://A?v=1#top')`、`<style>.c{background:url("asset://B?x=a)b")}</style>`(后缀里的 `)` 原样保留) |

### 4.4 测试工具链修正(非产品缺陷)


- `@release` 规格原先把 `${target.prefix}` 又传给自带前缀拼接的 `request()`,请求打到双前缀路径,被静态兜底当 200 返回;已改为无前缀路径。
- 两个 `@release` 规格原先共用 `qa_dev/main`,API 规格激活 v9.0 后页面规格的“未设置”断言必然失败;API 规格改用 `qa_prod/main`,跨范围探针保留 `qa_dev/main`。
- 新增韧性规格用独立范围 `qa_resilience/resilience`,并用 `page.route` 制造"响应丢失"与"迟到响应";注意模态弹窗打开时页面按钮会被遮罩挡住(点击顺带关闭弹窗),因此状态漂移通过 HTTP 制造。
- PowerShell 5.1 三个脚本:补 UTF-8 BOM(否则 GBK 误解码吞引号)、`Invoke-WebRequest` 失败时改用 `$_.ErrorDetails.Message` 读错误体、函数返回空数组需 `,@()` 且调用点包 `@()`、`Join-Path` 不支持三参数、嵌套 JSON 的收尾引号需转义。
- 复核环境记录:在 C 盘默认临时目录下运行会出现目录重命名"句柄无效"(原生 PowerShell `Move-Item` 同样失败);把 `TEMP/TMP` 指向 D 盘目录后同一代码通过。该现象与业务代码无关,但不是已知缺陷,后续复测需记录临时目录位置。

## 5. 未验证与已知边界

> 证据时点:Release 专项与后端/前端检查在 F1/F4 二次修复后重跑(§4.4);Freeze 4/4 与 Full 35 次通过的记录取自上一轮,两次修复只触及富文本扫描器与发布页 store(Full 套件排除 `@freeze`/`@release`,这些路径不在其中),因此保留该证据并在此说明时点。


1. **数据库**:全部验证基于 SQLite。MySQL/PostgreSQL 未执行(建表、唯一索引与 CAS 语义未在真实库上跑过)。
2. **网络存储/MinIO**:源存储为 MinIO 时创建直接拒绝(返回 `RELEASE_STORAGE_UNSUPPORTED`),未起真实 MinIO 验证;快照只落在本地目录。
3. **部署形态**:Kubernetes/多副本下必须共享同一快照卷;`production` 构建(embed 前端)未单独跑,验证均在 `-tags=dev` 与 `--no-frontend`/静态目录形态。
4. **崩溃残留**:`before_commit` 崩溃会留下一个快照最终目录(数据库无对应版本行),它**不可读、不影响功能**,但会占用磁盘,需要人工清理;`during_copy` 崩溃可能留下 `.staging/<id>`。本版不提供自动清理。
5. **时间语义**:切换生效是“随后开始的新请求读新版”,不做推送;业务侧需自行按大约 30 秒重读。
6. **未执行的验收条目**:R13(复制阶段并发写入的全部时序);以及 R14(磁盘无权限等错误注入)、R16(限额恰在边界值的等值用例)、R19(`..`/编码斜杠/符号链接逐项枚举)、R20(普通导入不覆盖版本表的断言)、R22(弹窗内切范围、他人先切换、令牌不入日志的端到端断言)、R25(真实 MinIO 实例)中标为 ◐ 的部分。R9/R17 已由正式回归覆盖(§4.2)。
7. **前端类型检查**:以 `cd vue && npx vue-tsc -b` 执行,本轮通过;仓库确实没有名为 `type-check` 的 npm 脚本,但这不影响该检查可运行。
8. **提交范围**:已排除并还原 `.hz` 目录配置与 `biz/model/api/api.pb.go` 版本注释漂移；`go.mod` 保留3个直接引用依赖的direct/indirect归位，无版本升级。本机配置、启动脚本、上手文档、临时文件与构建缓存未纳入提交。

## 6. 业务接入简版说明

三个接口按需选一个,以下 `<basePath>` 即部署前缀(如 `/rainbow-bridge`)。

1. **暂时不改**:继续用 `GET <basePath>/api/v1/runtime/config`(头 `x-environment`、`x-pipeline`),它读的是编辑区当前值,天然“立即生效但不留版本”。
2. **推荐接入(跟随切换)**:

```bash
curl -H 'x-environment: prod' -H 'x-pipeline: main' \
     https://rainbow-bridge.example.com/rainbow-bridge/api/v1/runtime/release
```

   返回 `data.selection_mode="active"`、`data.version` 为当前生效版本;管理员切换后,新请求读新版本,已在途请求与业务本地缓存不会被撤回。

3. **需要锁死版本**:`GET <basePath>/api/v1/runtime/release?version=v1.0`,返回 `selection_mode="fixed"`,不受切换影响。

注意事项:

- 每次响应都带 `payload_sha256`;需要一致性校验的业务可在本地比对本轮与上轮摘要,变化时整体替换配置值,**不要只合并新增字段**。
- 数字精度:响应保留 JSON 数字原文;JavaScript `JSON.parse` 仍会损失超大整数,涉及大整数(如雪花 ID)请用无损解析库(如 `json-bigint`)。
- 失败处理:读取失败(404/503)时是否沿用上次成功值由业务决定;`503 RELEASE_INCOMPLETE` 表示该版本文件缺失,**不要回退去读旧接口**当作等价数据。
- 资源引用:配置内容里的受管资源会自动渲染成当前版本的下载地址,业务只需按字符串使用;外部 URL 不受版本控制(`detail` 的 `meta.external_references` 会列出)。

## 7. 后续建议(不含在本版范围)

1. 增加孤儿快照目录与 `.staging` 残留的巡检/清理命令。
2. 为 MySQL/PostgreSQL 补一套后端驱动的集成测试(唯一索引冲突与 CAS 语义)。
3. 给版本增加“删除/保留策略”需要新 PRD:当前明确不可删,磁盘随版本增长。
