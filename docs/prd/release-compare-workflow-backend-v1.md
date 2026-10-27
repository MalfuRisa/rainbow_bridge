# 发布差异与集中发布流程：后端 PRD

日期：2026-10-26。状态：设计完成，待研发实施。本文件包含两个任务，不代表功能已完成。

## 1. 本轮要做什么

任务一：在切换生效前，比较“准备发布的版本”和“当前生效版本”，告诉管理员新增、修改、删除了哪些配置。

任务二：为前端在一个入口完成“检查、锁版、创建版本、查看差异、确认生效”明确后端接口与续作合同。复用现有接口，不新增整套发布任务、审批或自动回滚系统。

这里的“发布前”指业务应用实际读取内容改变之前。创建不可变版本本身不会生效，因此第一版先创建未生效版本，再比较、确认、切换。此次不做可变编辑区与生效版本的实时差异，也不修改错误反馈体系、字段清空/false 更新语义。

完成后的使用示例：编辑区将标题由“虹桥”改为“虹桥管理平台”，新增图片，删除旧开关。管理员在同一操作流程里创建 v2，看到新增 1、修改 1、删除 1，确认后才让业务读取 v2。取消确认时 v2 保持未生效，原生效版本不变。

## 2. 工作位置与现状依据

仓库：`D:/kylin/rainbow_bridge`。编写时分支 `main_v1`，已提交 HEAD 为 `c9aefe5`，工作区另有范围身份、导出来源、备份及前端尚未提交成果。实施以这些已完成成果整合后的实际代码为基线，不能仅从旧 HEAD 开发后覆盖回来。

实施前登记基线和工作区状态。需要隔离时使用独立工作树，建议分支 `codex/release-compare-workflow`（尚未创建）；未提交依赖由负责整合者明确带入，不能假定 Git 工作树会自动复制。禁止在共享目录切换分支、全仓暂存或还原他人文件。

| 当前能力 | 已核对的入口 | 本轮处理 |
| --- | --- | --- |
| 锁版/解锁，状态 revision | `idl/biz/freeze.proto`、`biz/service/freeze_service.go` | 保持接口与冻结语义 |
| 创建不可变版本，创建不生效 | `CreateRelease`，`biz/service/release_service.go` | 复用，继续校验 scope、冻结 revision 与 request_id |
| 切换生效及并发保护 | 同文件 `ActivateRelease` | 复用 expected_active_revision 的事务 CAS |
| 幂等确认 | 创建记录和切换事件中的 request_id、请求指纹 | 保持“确认原请求”能力 |
| 版本详情、active、列表、历史 | `idl/biz/release.proto`、release handler | 复用作流程恢复及完整内容读取 |
| payload、资源清单、富文本标记扫描 | `release_types.go`、`release_snapshot.go`、`release_html.go` | 比较使用原始快照和共享解析规则 |
| 同名重建隔离 | scope_id 与 expected_scope_id | 全流程固定同一代身份 |

本轮不改 Vue；前端后续自行安排页面与交互。保留业务 runtime、导入导出、备份、逻辑删除版本及独立锁版入口。不新增数据库表，不调整备份 schema，不给现有 activate 客户端增加必填字段。

## 3. 两个任务的边界

### 任务一：新增版本差异接口

新增管理接口 `POST /api/v1/release/compare`。只比较同一 scope 的两个不可变快照：目标版本与请求固定的当前生效版本。未生效版本、历史旧版都可作为目标；已删版本不能比较。目标已生效时允许比较，返回零差异及 `target_is_active:true`。

### 任务二：复用现有接口完成集中发布

后端交付流程合同、必要的共用适配和真实 HTTP 连续场景测试。前端按合同串联现有接口。本轮不新增“一键发布”聚合接口，不在单个长事务里等待用户确认，不替前端决定是否生效。

这样做的原因是创建和生效之间需要用户看差异并确认，不能伪装成一次全成功或全失败的原子操作。

## 4. 差异查询请求

所有路由按当前 BasePath 挂载，以下路径不包含部署前缀。

```http
POST /api/v1/release/compare
Content-Type: application/json
```

```json
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

- scope、version、目标 expected_snapshot_id、expected_active_revision 必填，复用现有格式校验；revision 是非负十进制字符串，不能转为 JS number。
- expected_active_snapshot_id 固定比较基准；无生效版本时为空字符串或省略，expected_active_revision 仍必须传实际 head revision，首次通常为 `"0"`。服务端判定有 active 但该字段为空时按基准不符处理，不能自动补当前 active。
- offset 默认 0，必须为非负整数；limit 缺省或 0 采用 20，负值或大于 100 返回 400。offset 超出列表返回空页，不能省略数组或 summary。
- 翻页沿用全部范围、目标与基准字段；active 发生变化即返回冲突，不把多次不同基准的结果拼在一起。
- 不接受 editor、跨 scope、自定义任意两个历史版本或自动选择最新目标等模式。
- 有效管理会话和现有 CSRF/Origin 合同适用。虽然使用 POST，本接口只读，不创建版本、head、审计事件或比较记录；不得加入公开白名单。返回 `Cache-Control: no-store`。

## 5. 差异响应和显示边界

成功 HTTP 200，沿用 release 的 `code:200`、`msg:"OK"` 包装。下面只展示一个修改项，实际 summary 按整组计算。

```json
{
  "code": 200,
  "msg": "OK",
  "data": {
    "scope_id": "11111111-1111-4111-8111-111111111111",
    "target": {
      "version": "v2",
      "snapshot_id": "22222222-2222-4222-8222-222222222222"
    },
    "baseline": {
      "version": "v1",
      "snapshot_id": "33333333-3333-4333-8333-333333333333"
    },
    "active_revision": "3",
    "target_is_active": false,
    "has_changes": true,
    "summary": {"added": 0, "modified": 1, "removed": 0, "unchanged": 2},
    "items": [
      {
        "resource_key": "site_title",
        "change_type": "modified",
        "changed_fields": ["content"],
        "before": {"alias": "site_title", "name": "网站标题", "type": "text"},
        "after": {"alias": "site_title", "name": "网站标题", "type": "text"}
      }
    ],
    "total_changes": 1,
    "offset": 0,
    "limit": 20,
    "has_more": false,
    "next_offset": null
  }
}
```

- baseline 无生效版本时显式 `null`，全部目标配置算 added；空目标对非空基准全部算 removed；双方空时全部计数为 0。
- items 只含 added/modified/removed，不逐条输出 unchanged。按 resource_key 的 UTF-8 字节序升序，页间顺序固定。
- added 的 before 为 null；removed 的 after 为 null；对应 changed_fields 为空数组。modified 的 changed_fields 顺序固定为 alias、name、type、content、remark、is_perm、description 中实际变化项。
- 比较键是 `(scope_id, resource_key)`，不能按名称、数组位置或跨范围 alias 配对。
- before/after 只提供表格摘要，不复制整份 content/remark/description 到每条分页响应。完整前后内容复用 `release/detail`，分别使用响应里固定的版本和 scope，核对返回 snapshot_id；content 按既有字符串合同处理，不经 JS 数字解析。首次发布没有“之前内容”。
- 不提供逐行文本 diff、JSON 字段树 diff 或图片像素差异。前端可用已有版本详情做内容对照；本轮准确交付“哪些配置、哪些字段变了”。
- 比较范围包含全部快照配置字段；环境/渠道展示名称、版本说明、操作者、创建时间、快照 UUID 和冻结 revision 不作为配置变化。
- `has_changes:false` 只表示配置与受管资源语义相同，不等于发布成功、资源健康或不允许切版。第一版允许用户显式切换到内容相同的另一个版本；若目标就是当前 active，仍遵循原 activate 的 unchanged 错误。

## 6. 比较规则：资源不因快照换 ID 就假报变化

不能直接比较 runtime URL、payload 总 SHA 或 release-asset 的随机 ID：同一图片再次创建快照会得到新快照地址，但不一定是内容变化。

1. 读取两侧不可变 payload 和资源清单，校验 payload SHA、清单 SHA、可解析性、配置键唯一性和所有受管引用存在；任何异常整体失败，不能略过问题项后给出“无变化”。
2. 复用 `renderReleaseContentWith` 和既有富文本/CSS/srcset/JSON 扫描规则，将受管引用转换成内部比较形式。资源身份按 `(sha256,size,content_type,file_name)` 形成有边界的确定性编码，引用的 query/fragment 原样纳入比较。不得自行再写一套正则。
3. 此内部形式采用带长度的字段编码或等价无歧义表示，并对普通文本/字面量转义或分类型编码，避免用户文本恰好等于资源标记导致碰撞。它只供比较，不能回写快照或对外返回成业务地址。
4. 图片等资源 ID 不同但上述信息与引用后缀相同视为未变；资源字节不同、文件名/类型不同、引用位置/数量/后缀不同均属于 content 变化。资源文件本身不单独增加配置计数，一个配置多个引用改变仍只算一个 modified。
5. object/keyvalue 使用已有无损 JSON 解码，规范化对象键顺序、保留数组顺序与数字词元；不能经 float64。对象键顺序或 JSON 空白变化不算修改；`1` 与 `1.0` 第一版按不同数字词元处理。字符串、富文本保留原文本语义，不尝试浏览器渲染等价判断。
6. 其他类型按已保存内容比较，除受管引用解析外不做额外 trim、大小写折叠或类型猜测。类型变化明确列入 type；content 只有其比较形式也不同时才列入，不强行把所有类型变化记为内容变化。

比较依据是受摘要保护的清单，不为每次翻页重复读取并 SHA 全部资源文件。因此本接口不是完整资源健康检查，最终 activate 仍必须执行原有完整性与实际文件校验；页面不得宣称“差异查询成功代表资源健康”。

## 7. 并发与错误合同

- 在一致的只读 DB 事务/快照内校验当前 scope、目标版本、目标 snapshot、active/head、基准版本及清单，复制本次比较所需数据后结束事务；不能分多次无关联查询拼成混合基准。
- 开始时 head revision 和 active snapshot 均须与请求一致；同一快照被切走又切回也因 revision 增长而冲突。
- 捕获完成后基于固定数据计算；期间切版或删除允许在途只读比较完成，但后续翻页/切换必须重新校验。响应不是当前状态永久有效的保证。
- 不占用全局业务写闸门做排序/JSON 比较，不锁住用户直到确认。备份维护期间遵循现有只读请求策略，不能因 POST 机械视为业务写入；检查并适配维护中间件分类，但不得扩大公开鉴权范围。
- 单次请求总期限不超过 60 秒，也不延长调用方更短期限；使用现有每快照 5000 配置、64 MiB payload 的上限。规范化时检查 ctx，不复制资源字节，不使用无界全局比较缓存。比较上限为两侧既有 payload 上限之和，不另设任意低阈值拒绝已合法创建的版本。

| HTTP | error_type | 含义与客户端处理 |
| --- | --- | --- |
| 400 | `RELEASE_INVALID_REQUEST` / `SCOPE_INVALID_REQUEST` | 非法字段、分页、revision 或身份，禁止缺身份降级 |
| 401/403 | 既有 auth 错误码 | 登录/CSRF/Origin 不满足，复用统一处理 |
| 404 | `RELEASE_VERSION_NOT_FOUND` / 既有范围 not found | 目标或范围不存在 |
| 409/410 | `SCOPE_IDENTITY_CONFLICT` / `SCOPE_DELETED` | 同名重建或已删除，作废旧流程 |
| 410 | `RELEASE_VERSION_DELETED` | 目标或被固定的基准版本已删除 |
| 409 | `RELEASE_SNAPSHOT_MISMATCH` | version 与目标 expected_snapshot_id 不对应 |
| 409 | `RELEASE_ACTIVE_REVISION_CONFLICT` | head revision 不符，返回现有 latest_active 结构（无 active 则 null） |
| 409 | `RELEASE_COMPARE_BASELINE_MISMATCH`（新增） | revision 相同但基准 snapshot 请求不符，返回 latest_active，重新读取并确认 |
| 503 | `RELEASE_INCOMPLETE` | payload/清单损坏、引用缺清单，禁止返回部分成功 |
| 503 | `RELEASE_COMPARE_TIMEOUT`（新增） | 计算超时，可按原参数重试，不改任何状态 |

底层 DB/服务异常沿用现有内部错误包装，不伪装为零差异。不要借本轮重构全局错误体系。

## 8. 集中发布流程的接口合同

| 步骤 | 后端接口 | 客户端必须固定什么 | 成功后的实际状态 |
| --- | --- | --- | --- |
| 读取当前范围 | 现有环境/渠道、freeze/status、release/active | 当前 scope_id，冻结 revision，active/head 信息 | 只读，没有开始生效 |
| 锁版 | POST freeze/lock（已锁可跳过） | expected_scope_id、expected_revision；reason 继续选填 | 编辑区冻结，业务仍读原 active |
| 创建候选版本 | POST release/create | expected_scope_id、锁版后 expected_freeze_revision、版本名、独立 create request_id | 不可变版本创建，仍未生效 |
| 查看差异 | 新 POST release/compare | 目标 version/snapshot、scope、比较时 active snapshot/revision | 只读，展示准备切换的差异 |
| 确认生效 | POST release/activate | 同一 scope、目标 version、比较响应 active_revision、独立 activate request_id | CAS 成功才改变业务读取内容 |
| 核对结果 | 既有响应 operation/active，必要时 active/detail/history | 原请求与目标身份 | 区分操作曾成功和此刻仍在生效 |

锁版前的“检查”第一版复用状态读取与 lock 自带资源预检，不新增一个假定检查后永远有效的接口。若资源检查不通过，沿用已有 resource_issues，并停在该步骤。本轮不新增资源归属诊断字段。

具体要求：

- 初次无 active 可发布，compare 与空基准比较后 activate 使用返回的 revision。
- 已锁范围直接使用当前冻结 revision 创建；未锁范围先锁。已经有人完成锁定造成 revision 冲突时重新读取状态，不能假造锁版成功。
- 用户选择已有未生效版本时，可直接 compare → activate，不要求重新锁版/创建，也不要求为切回历史版本冻结当前编辑区。已经创建的快照不受后续解锁编辑影响。
- 创建与切换使用两个独立 UUID request_id。首次提交后固定各自整份 payload；结果不明时原样重放，不能刷新 revision 后继续用旧 request_id。换目标/范围/原因/revision 是新操作，应重新确认并分配新 request_id。
- compare 后 active 改变：activate 保持原比较 revision，请求返回冲突后重新比较，让用户重新确认。禁止客户端先刷新 head 再静默切换，否则绕过差异确认。
- activate 无需新比较令牌：同 scope 的版本名不可覆盖且不复用，目标固定由 `(scope_id,version)` 唯一决定；compare 校验 snapshot，activate 校验 scope、目标未删及基准 revision 足以闭合。保留现有直接切版客户端，不新增强制“先调用 compare”的服务端门禁。
- 原有单独的锁版、创建、切换及版本删除继续可用，不由流程功能取代。

## 9. 取消、中途失败与刷新续作

| 情况 | 明确行为 |
| --- | --- |
| 锁版失败 | 不调用 create，不改变 active |
| 已锁版，创建失败 | 保留实际冻结状态，提示尚未创建；不自动解锁 |
| 创建响应丢失 | 同 create request_id、同 payload 确认；不得重复创建候选版本 |
| 创建成功，比较失败/用户取消 | 候选版本保留未生效，active 不变；用户可继续或用现有删除版本能力清理 |
| 比较成功，切换失败 | 不回滚或删除候选版本，显示已创建但未生效；按错误修复/重比后继续 |
| 切换响应丢失 | 同 activate 请求确认；原操作成功不等于该版本现在仍生效，核对 operation 与最新 active |
| 用户退出、页面刷新 | 用列表/detail/active/freeze 状态重建进度；新确认前重新比较，不将浏览器本地状态当后端事实 |
| 冻结后取消整个流程 | 不自动解锁；用户可显式解锁，避免影响其他协作者依赖的冻结状态 |
| 当前 scope 删除后同名重建 | 旧流程终止，不迁移旧候选、revision、request_id 或表单到新 scope |

不新增持久化工作流状态、后端自动重试定时器或新审计事件类型。原 lock/create/activate 事件准确记录每次实际动作，比较不产审计业务事件。若未来要跨设备恢复未提交表单，另行设计，本轮不承担。

## 10. 实现组织与兼容要求

研发应使用 `$engineering-quality-preserving-refactor`（编码要求），实现前读取 `C:/Users/MalfuRisa/.codex/skills/engineering-quality-preserving-refactor/SKILL.md`。

- 差异输入输出与纯比较规则建议放同 service 包的 `release_compare_types.go`、`release_compare.go`；规范化较多时单独放 `release_compare_content.go`。不要继续将所有逻辑追加进已较大的 release_service.go。
- 复用 scope 解析、无损 JSON、release 标记渲染/扫描及错误包装；handler 只负责绑定、身份上下文、调用和显式 JSON 合同。
- 用 map 建立两侧 resource_key 和清单索引，线性扫描配置与内容，结果排序。目标复杂度为内容/资源清单线性处理加变化项排序 `O(N log N)`，不做每个配置遍历整份清单的平方扫描。允许每页重新计算，但不要读磁盘资源字节或为每项反复查库。
- 在 `idl/biz/release.proto` 增加比较消息/RPC，用 hz 生成路由和 pb；禁止手改 `biz/model/*.pb.go`、`biz/router/`。工具不可用时明确阻塞，不手工模拟生成代码。
- 空数组、零计数、null 必须符合 §5，不重现 omitempty 引起的前端崩溃。注册路由鉴权枚举应覆盖新 compare；维护中间件只增加这个明确只读接口，不以 release/* 通配放行写入。
- 不新增可持久化对象，不改快照内容/摘要、不改版本不可变、版本逻辑删除、范围身份和备份 v2 语义；不用新流程绕过维护闸门。
- 更新 `docs/docs/api/` 的现有专业接口文档并补流程说明，保持当前 Fork API 入口已确认的设计格式，不改首页展示或链接。

## 11. 验收标准

### 任务一：比较准确且只读

| 场景 | 必须验证的结果 |
| --- | --- |
| added/modified/removed/unchanged 混合 | 计数、条目、changed_fields 和排序逐项正确；换 offset/limit 汇总不重不漏 |
| 首次发布、双方空、目标空 | baseline:null 和零值/空数组稳定；删除项不遗漏 |
| 相同配置重复创建两个快照 | snapshot/asset ID 不同但相同资源与内容不报修改 |
| 同名图片实际字节变化，或 type/name/query/fragment 变化 | 相应配置 content 变化，不以 file_id 或 URL 掩盖真实改变 |
| 仅名称、备注、description、is_perm 或 type 变化 | 返回正确字段；不把版本说明/创建时间算变化 |
| object/keyvalue 大整数、键重排、数组重排 | 9007199254740993 与 9007199254740992 有差异；对象键重排无差异；数组重排有差异；1 与 1.0 按合同处理 |
| 富文本媒体、CSS、srcset、query/fragment | 复用已有合法引用夹具，受管资源规范化完整，不因快照 URL 变化误报 |
| 用户字面量类似内部资源比较标记 | 字面量不能与受管引用碰撞成假相等 |
| active 在读取间切换、A→B→A、分页中切换 | 起始不一致则冲突；在途结果只来自固定一对版本；后续页与 activate 拒绝过期 revision |
| 同名范围重建、目标删除、snapshot 不符 | 409/410，不能查询到新代或别的版本 |
| payload/清单损坏、引用缺元数据 | 503，不返回部分差异或假零差异；实际文件缺失仍由原 activate 完整检查拦截 |
| 无会话/缺 CSRF/错误 Origin | 401/403，不落 head/版本/事件，不因只读变公开接口 |
| 备份维护期间 compare / create / activate | compare 按只读策略可用；两个写接口仍被原维护保护拦截 |
| 超时、取消 | 有界结束，无后台残留任务，无持久化副作用 |

### 任务二：一个连续流程能安全继续

在独立 SQLite 实例、临时目录和非用户业务端口做真实 HTTP 验证：

1. active=v1(A)，编辑区=B → 读取 scope → 锁版 → 创建 v2 → compare 显示 B 相对 A → active 仍 v1 → 用户确认模拟请求 activate → runtime 读到 v2(B)。
2. create 同请求重放只创建一个快照；activate 重放只产生一次实际切换，不改已有指纹语义。
3. compare 后另一个管理员先切到 v3，旧确认请求 409；重新比较后显式新确认可以继续。
4. 创建后取消、切换失败和刷新恢复：候选版本保留、业务仍读原版，重新读取后可从 compare 继续。
5. 激活响应丢失后又被他人切版，确认原请求同时呈现历史 applied_revision 与最新 active，不能误报仍在生效。
6. 已锁范围跳过 lock、已有候选跳过 create、首次无 active、切回旧版分别跑通；旧独立接口依旧可用。

测试分层：纯比较单测、真实 handler/鉴权合同测试、受影响 Go 回归与 vet、上述 HTTP 连续场景。以 5000 项/每侧最多 64 MiB 的合法快照做一次本机边界测量，记录耗时和峰值内存及环境，不预先宣称性能百分比；确认在 60 秒期限内可取消且不加载资源全量字节。MySQL/PG 若不运行应明确未验，不把 SQLite 通过等同全数据库通过。不得在 18080/18081 等用户业务实例删改数据。

## 12. 实施顺序与交付

1. 核对依赖基线，复用既有发布夹具，建立固定快照对的比较规则测试。
2. 实现只读捕获、规范化、分页摘要与 compare handler/IDL；补鉴权和维护分类。
3. 按 §8–9 串联现有接口写真实 HTTP 场景；只有发现合同缺口才做范围内共用适配，不增加工作流表或聚合发布 API。
4. 运行相称的 Go/HTTP 检查，更新专业 API 文档，在本文件登记真实结果与未验证项。

交付回传写清两任务分别完成什么、新接口与复用接口、执行过的验证、剩余限制和前端接入要点。中途进度更新留在本 PRD，不为每轮修复另写桌面报告。未获用户新授权前不修改 Vue、不派发其他聊天、不提交/推送，不部署到现有演示实例。

## 13. 实施与验收结果（2026-10-26）

状态：后端两个任务已实现；未修改 Vue。

### 13.1 落地内容

| 职责 | 落点 |
| --- | --- |
| 比较输入/输出/摘要类型 | `biz/service/release_compare_types.go` |
| 只读捕获、冲突校验、分页 | `biz/service/release_compare.go` |
| 资源语义规范化 | `biz/service/release_compare_content.go` |
| Handler/显式 JSON 合同 | `biz/handler/release/release_compare.go` |
| IDL | `idl/biz/release.proto` 的 `ReleaseCompareRequest/Response` 与 `Compare` RPC；release.pb.go 在独立 hz 工程重新生成 |
| 路由/维护分类 | `biz/router/release/release.go` 新增 `POST /api/v1/release/compare`；`BackupMaintenance` 明确放行该只读 POST |
| 文档 | `docs/docs/api/release-compare.md` |
| 测试 | `release_compare_test.go`、`release_http_test.go` 中的 compare 合同测试、`middleware/maintenance_test.go` |

集中发布没有新增聚合接口、工作流表或审计事件；继续复用 freeze/lock、release/create、release/compare、release/activate。

### 13.2 验收记录

自动化：

```text
go test ./...     通过
go vet ./...      无输出
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./... 通过
```

| 场景 | 结果 |
| --- | --- |
| added/modified/removed/unchanged 混合 | 单测构造 added 1、modified 1、removed 1、unchanged 1；changed_fields、排序、summary、分页、offset 越界空页均正确 |
| 首次发布/无 active | baseline=null，目标全部 added；target_is_active=false |
| 目标就是 active | baseline=target，total_changes=0、target_is_active=true |
| 同内容重复快照 | 两个快照的 snapshot/asset ID 不同，比较为 0 变化 |
| 对象键重排 | 对象键顺序/空白变化不报修改；大整数原样比较 |
| 图片实际字节变化 | 上传同文件名同类型但不同字节的资源后，REL_IMAGE 报 content modified |
| revision 冲突 | 返回 409 `RELEASE_ACTIVE_REVISION_CONFLICT`，包含 latest_active |
| baseline snapshot 不符 | 返回 409 `RELEASE_COMPARE_BASELINE_MISMATCH` |
| target snapshot 不符/非法 limit | 分别返回 `RELEASE_SNAPSHOT_MISMATCH` / `RELEASE_INVALID_REQUEST` |
| HTTP 合同 | 直接 handler 测试验证 items=[]、next_offset=null、summary、Cache-Control、409/400 错误体 |
| 维护分类 | 单测验证 compare POST 在维护期间 200，config/create 仍 503 BACKUP_MAINTENANCE |
| 集中发布真实 HTTP | 独立 SQLite 实例：v1 生效(A) → 编辑 B/新增/删除 → v2 创建 → compare(B vs A) 得到 1/1/1/1 → runtime 仍读 v1 → 并发创建并生效 v3 → 用旧 revision 确认 v2 返回 409 latest_active=v3 → 重新 compare → 用新 revision 生效 v2 → runtime 读 B |
| 幂等/取消/恢复 | 真实 HTTP 中 create/activate 原 request_id 重放均 replayed=true，版本列表仍只有 v1/v2/v3；单测覆盖取消/切换失败不改 active、候选保留 |
| 只读性 | 比较不写 head/版本/事件；事务内捕获后回滚，不使用业务写闸门 |

### 13.3 未完成/限制

- MySQL/PostgreSQL 未运行真实比较测试；SQLite 已实测。
- 5000 配置/64 MiB 边界未做一次正式的性能/内存测量，只按上限约束实现。
- 未做 60 秒超时的强制慢查询注入；超时路径返回 503 `RELEASE_COMPARE_TIMEOUT`。
- 未跑 `go test -race`（本机 CGO 默认关闭）。
- 前端尚未接入 compare 与集中发布步骤；页面需按 §8/§9 的既有接口顺序串联，并显示“候选版本已创建但未生效”。
