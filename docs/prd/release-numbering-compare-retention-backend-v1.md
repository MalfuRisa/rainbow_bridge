# 任意版本比较、自动编号与恢复副本管理：后端 PRD

日期：2026-10-26。状态：设计完成，待研发。本轮仅规划后端，不代表已实现或已验收。

## 1. 已确认需求与交付边界

| 任务 | 用户确认的行为 | 本轮完成标准 |
| --- | --- | --- |
| A 任意版本定向比较 | 从 v2 到 v5 看新增、修改、删除；交换方向，新增与删除对调 | 同一 scope 内任意两个未删除版本可比，原发布前比较继续可用 |
| B 自动递增版本号 | 每个环境、渠道组合独立 v1、v2、v3 | 新版本由后端原子分配，无需手填；删除、回退、重试不造成重复编号 |
| C 已完成恢复的副本自动管理 | 成功恢复留下的旧副本不再阻止下一次恢复 | 自动识别、保留及轮换成功副本；进行中、失败未处理的恢复继续保护 |

默认比较仍为“当前生效版本 → 准备发布的版本”，复用现有资源、JSON、大整数比较规则。保留独立锁版、创建版本、切换、删除及集中发布流程。

本轮不做热恢复、零停机、多 Pod 改造、跨环境比较、编辑区实时 diff、图片像素比较、审批流或新的全局鉴权系统。整实例恢复仍需重启；本轮消除的是“成功恢复后必须手工处理旧副本才能再次恢复”。业务迁移包导入与整实例恢复继续分开。

## 2. 现状与工作位置

仓库 `D:/kylin/rainbow_bridge`，编写时 `main_v1`、已提交 HEAD `c9aefe5`。范围身份、导出来源、备份、比较及前端等成果仍有未提交内容，实施必须以整合后的实际文件为准，不能从旧 HEAD 覆盖这些依赖。

研发先记录状态。需要隔离时建议独立分支 `codex/release-numbering-retention`（尚未创建）和独立工作树；先明确未提交依赖怎样整合，不在共享目录切分支、不整包 add、不还原其他窗口修改。

已核对的代码事实：

- `biz/service/release_compare.go` 当前只把目标与固定 active 基准比较；`compareSnapshots`、`release_compare_content.go` 已具备可复用的纯比较与规范化能力。
- `CreateRelease` 接受手填 version，创建与 activate 复用版本校验函数，不能放宽 create 时连带破坏 activate 的必填规则。
- `config_release_head` 已有每 scope 生效指针/revision，但没有发布编号计数器。`withWriteGate` 用数据库事务写协调，适合在同一事务内分配编号。
- `hasUnhandledRollback` 当前仅因 rollback 目录存在非空子目录就阻止恢复；并未区分成功留存与异常待处理。
- `CompleteStartup` 在正常初始化成功后收尾，但 `StageCommitted` 在它之前已可能写入，不能只看 committed 就认定全部启动成功。
- 备份为 schema 2，显式业务表白名单及 schema 1 升级路径需要与编号字段一起更新。
- 已存在 `BACKUP_RESTORE_PENDING`、`BACKUP_ROLLBACK_REVIEW_REQUIRED` 及前端旧错误兼容修正，应保留。当前本机曾手工归档到 `reviewed-rollback` 的副本也必须保留，不作为本轮自动删除对象。

参考：[既有比较及发布 PRD](release-compare-workflow-backend-v1.md)、[比较 API](../docs/api/release-compare.md)、[备份 API](../docs/api/backup.md)、[范围身份](../docs/api/scope-identity.md)。

## 3. 任务 A：两种比较模式，复用同一个接口

继续使用管理接口 `POST /api/v1/release/compare`，新增 `comparison_mode`：

| 模式 | 用途 | 与 active 的关系 |
| --- | --- | --- |
| `active` | 发布前比较，缺省模式，兼容原请求 | 必须固定 active snapshot/revision，发生切换返回冲突 |
| `fixed` | 任意两个已发布版本的定向比较 | 固定双方 snapshot，不要求也不查询 active/head 来决定结果 |

显式空串或未知模式 400 `RELEASE_INVALID_REQUEST`。双方必须属于当前同一 scope 的有效未删除版本；旧 scope 410，不支持跨范围及已删除版本。

### 3.1 fixed 请求

目标继续复用 `version` 和 `expected_snapshot_id`，新增基准字段，避免建立第二套比较端点。

```json
{
  "comparison_mode": "fixed",
  "environment_key": "prod",
  "pipeline_key": "default",
  "expected_scope_id": "11111111-1111-4111-8111-111111111111",
  "baseline_version": "v2",
  "expected_baseline_snapshot_id": "22222222-2222-4222-8222-222222222222",
  "version": "v5",
  "expected_snapshot_id": "55555555-5555-4555-8555-555555555555",
  "offset": 0,
  "limit": 20
}
```

- 表示“从 baseline_version 到 version”，before 对应 v2，after 对应 v5。
- fixed 下双方 version/snapshot 都必填；拒绝出现 `expected_active_revision`、`expected_active_snapshot_id`（即使空串也不接受），防止混用。需通过 IDL optional/presence 或 handler 请求结构明确识别字段是否提交。
- active 下保留原请求合同，拒绝提交 fixed 基准字段；省略 comparison_mode 的旧 active 请求继续可用。
- offset/limit、60 秒期限、Cookie/CSRF/Origin、备份维护期只读放行、空数组/null 合同沿用已有比较接口。

### 3.2 响应及语义

两种模式均新增 `comparison_mode`，保留 target、baseline、summary、items、has_changes、分页和资源字段比较规则。

- fixed 返回 `active_revision:null`、`target_is_active:null`，不暗示此刻哪个版本在生效；active 模式继续返回原字符串 revision 和布尔值。
- fixed 的 baseline 必不为空；同一版本与自身比较合法，返回全部 unchanged、items:[]、has_changes:false。
- v2→v5 与 v5→v2 的 modified/unchanged 数量相同，added/removed 互换；before/after 对调。changed_fields 固定顺序不因方向变化。
- 比较按 `(scope_id,resource_key)` 配对，不能靠版本号大小推断方向，旧的非数字版本名也可自由选择。
- 固定模式翻页沿用双方 snapshot。中途 active 改变不使页面失效；双方任意版本被删除或 scope 被删除后，新请求返回 410，不返回残缺内容。在途捕获完成的固定比较可按原只读合同完成。
- 继续复用 release/detail 获取完整前后内容并核对 snapshot，不在比较分页返回中复制整份 content。

### 3.3 并发、错误和发布关系

fixed 在同一只读事务中解析 scope 与双方版本、校验 snapshot 和清单，随后结束事务并调用共用纯比较算法。不创建 head、不读写 active 作为前提，不增加工作流表。

- 双方 snapshot 不匹配：409 `RELEASE_SNAPSHOT_MISMATCH`，错误额外携带 `side:baseline|target`，原错误字段保持。
- 双方版本不存在：404 `RELEASE_VERSION_NOT_FOUND`；已删：410 `RELEASE_VERSION_DELETED`；同样补 side 便于定位。
- 非法/混用字段 400；scope 错误、503 不完整和超时沿用既有错误。
- 资源、JSON、nonce 防字面量碰撞、数字词元、排序和计数完全复用。新能力只改变“如何选取两侧快照”，不能再实现一份规范化算法。

前端默认用 active 模式显示日常发布差异，用户切换为任意版本比较才用 fixed。**fixed 结果不能直接充当发布确认依据**：要切换生效时必须按当前 active 重新执行 active compare，再把该响应的 active_revision 交给 activate，保留 CAS 与重新确认规则。

## 4. 任务 B：每个 scope 自动编号

### 4.1 业务规则与旧版本兼容

- 新 scope 的首次成功创建为 `v1`，之后 `v2`、`v3`。按 scope_id 隔离，不是全站编号；同 key 删除重建得到新 scope，可重新从 v1 开始。
- 版本号在创建快照时分配，不在切换生效时分配。创建不生效、切回旧版、删除非生效版本都不能降低计数器。
- 删除后的已使用编号不复用。事务未提交的失败创建没有对外可读版本，可回滚编号；不承诺无空号，但任何成功提交过的编号不能在同实例同 scope 再分配。
- 旧版本如 `v1.0`、`test-1`、`123` 原名、URL、snapshot、payload、摘要和审计保留，不重命名、不强行排序成数字版本。
- 迁移时扫描所有版本（含逻辑删除），仅规范正整数模式 `^v[1-9][0-9]*$` 参与历史编号上界。已有 v2、v5、已删 v9，则下一个为 v10；只有 v1.0、test-1 则从 v1 开始。`v01`、`v0` 保留为旧名但不计入规范序列。
- 编号用后端 int64 正整数，HTTP 只在 version 字符串中传递，禁止前端解析加一。达到 int64 上限后创建返回 409 `RELEASE_VERSION_SEQUENCE_EXHAUSTED`，不得溢出或回绕；迁移发现规范编号超过可支持范围要明确诊断该 scope，不默默忽略冲突数据。

### 4.2 存储、迁移与原子性

在 `config_release_head` 新增 `last_version_number BIGINT NOT NULL DEFAULT 0`，复用该表每 scope 的唯一行，不另加序列表。

1. 启动迁移为既有 scope 初始化/补齐 head 行；值取当前字段与所有历史规范编号最大值的较大者。迁移幂等；补 head 不改变 active/revision/changed_*，没有 active 时仍返回 active:null。
2. 创建先验证身份并在现有 withWriteGate 事务内查幂等提交结果；真正新操作才检查冻结/存储、获取 head、分配 N+1。
3. 分配值、快照版本/资源清单/创建事件在同一事务提交；维持 `(scope_id,version)` 唯一约束，不能在事务外 `MAX()+1` 或进程变量计数。
4. 同 scope 两个不同 request_id 并发创建，若都成功应为不同递增编号；同 request_id 重放返回同一个 snapshot/version，不再消耗编号。不同 scope 独立递增。
5. 计数器更新不增加 active revision、不改变 active 指针，不生成假激活事件。修改 activate 的 CAS 更新时不得覆盖 last_version_number。
6. 事务失败沿用已有快照暂存/孤儿清理规则，不把文件落盘当作编号已提交；响应丢失后先幂等确认，不能另建新版本。

### 4.3 创建 API 的明确变化

继续 `POST /api/v1/release/create`，新请求不再要求 version：

```json
{
  "environment_key": "prod",
  "pipeline_key": "default",
  "expected_scope_id": "11111111-1111-4111-8111-111111111111",
  "expected_freeze_revision": "7",
  "request_id": "77777777-7777-4777-8777-777777777777",
  "reason": "更新首页图片"
}
```

- 新操作 version 省略或空串都表示自动分配；非空返回 400 `RELEASE_VERSION_AUTO_ASSIGNED`，不能静默忽略旧客户端填写的名字后生成另一个版本。
- reason 仍选填，freeze/scope/request_id 校验保留。create 成功沿用 `data.meta.version` 返回实际编号；客户端只在成功后显示实际编号，提交前显示“系统自动生成”，本轮不新增预占号码/查询下个号接口。
- list/detail/activate/delete/runtime、导出、compare 中选择已存在版本的 version 仍是必填/原合同，不能因为 create 改为自动而一起放宽。
- 新 auto 创建指纹增加编码命名域 `create-auto-v1`，只绑定原请求字段（scope、范围键、freeze revision、reason），不把服务端实际编号写进客户端需重放的 payload；相同 request_id 换 payload 409 `RELEASE_REQUEST_CONFLICT`。
- 历史手填版本的已提交 request_id 仍须原样确认：先查提交记录，根据记录指纹合同走原校验，返回旧版本；不能因新“禁止手填”规则拒绝原请求。新 auto 记录重放时客户端擅自补入实际 version 也视为字段改变，不当成合法重放。
- 保留原“先确认已提交结果，再校验当前存储/冻结能力”的顺序，以及 scope 删除后不可确认到新代的保护。

前端需要与后端同批接入创建合同：去掉手填版本框，保留实际版本展示，create 成功再使用返回值做 compare/activate。本后端批次不修改 Vue；不能只升级演示实例后让旧页面持续提交非空版本。

## 5. 编号与备份恢复的兼容（任务 B 必做）

新增 head 列后备份业务 schema 升为 **3**，包 format 仍是 `rainbow-business-backup-v1`，不能仅改 schema 常量：

- 新 schema 3 白名单包含 last_version_number，检查非负且不小于该 scope 已有规范版本最大值，包含已删版本。
- 保留 schema 1、schema 2 的独立旧白名单和完整恢复能力。先按原 schema 校验包、文件摘要、数据与 counts；只在隔离候选数据库里按 1→2→3 或 2→3 升级，不在原 ZIP 上写入，不执行上传 DDL。
- 不得沿用“只允许 1 和当前 schema”的判断，否则升级为 3 后会误拒绝现有 schema 2 备份。
- 快照 payload/manifest 摘要不因编号计数器升级而修改；归档来源版本名原样保留。迁移包导入不导入版本计数器，目标 scope 按自身历史编号。

### 同实例恢复旧备份后编号不得倒退

例：v1 时备份，后来成功创建 v2，再恢复该备份；下一次应该创建 v3，不能把 v2 这个名字指向另一份新内容。

启动恢复及 CLI 回退流程中，在替换业务根之前从旧根取得编号上界；对候选库中仍存在的**相同 scope_id**，将计数器设为 `max(候选值,旧根值,双方已有规范版本最大值)`。新增/旧版根缺计数列时按旧版本记录推导。旧根来自当前根或崩溃重放时的 plan.RollbackRoot，不能只在内存保存，跨崩溃要能重算。

不把备份后新建、但候选库没有的 scope/配置/版本导回业务库；只合并同身份编号上界，这是除保留管理员外的一个明确恢复例外，不影响备份配置回到当时时点。恢复前后可重新生成 edit_revision 的既有行为保持。

恢复到另一套空实例无法知道原部署备份后的编号使用历史，只能从包内上界继续。文档明确这个边界；需要跨迁移长期固定的业务应使用 snapshot_id，不能宣称版本标签跨所有独立实例永久唯一。

## 6. 任务 C：成功副本是正常保留项，不再是冲突

### 6.1 状态分类与新的拦截规则

| 分类 | 是否阻止下一次恢复 | 自动处理 |
| --- | --- | --- |
| 正在准备/切换、等待重启计划 | 是 | 保留现有冲突和取消/启动恢复流程 |
| rollback_required、失败待核对、身份不明或控制文件损坏 | 是 | 不自动删除/忽略，返回明确原因 |
| 完整启动成功后确认的旧根副本 | 否 | 正常保留，按数量策略轮换 |
| 已完成自动清理的旧副本 | 否 | 保留审计摘要，不再要求目录存在 |

**删除 hasUnhandledRollback 的“非空目录一律拦截”逻辑，但不能改成“有目录也全部放行”。**在线 ExecuteRestore、离线 StagePackage、CLI 状态/回退和启动恢复共用同一个状态分类器；目录读取失败必须报告存储错误，不能当作“没有冲突”。

成功副本仍留在 `rollback/<operation_id>/data`，不强制移动目录才能解锁，不再要求普通用户归档/清理。保持最近一次成功恢复的 CLI 回退能力。

### 6.2 可持久化的成功证明

新增控制记录 `rollback/<operation_id>/retention.json`，属于本机控制目录，不进入业务备份。至少含 operation_id、job_id、completed_at、status（retained/pruning）、固定格式版本；实际 data 路径由受信任根与 operation_id 计算，不接受请求或记录里的任意绝对删除路径。

这份记录仅在 **正常数据库初始化、身份/编号迁移等全部成功之后**，由 CompleteStartup 幂等写入。不能仅凭 switch 中提前写入的 committed/job succeeded 就生成，也不能在启动失败回退前生成。

- 保存成功证明、归档 journal、任务收尾组成可恢复的顺序。任一步失败不能误标成功或删除唯一回退副本；下次启动可根据原 journal/根状态补齐。
- 如果业务恢复成功但保留记录写失败，服务可保留真实业务状态，但后续恢复要明确报控制存储错误并记录日志，不让管理员靠删文件解除。
- 每次成功副本都有自己的证明，不能只靠会被后续恢复覆盖的 restore-journal.last.json 或会被清理的终态 job 记录。
- active journal/plan 的引用优先于旧 retention 记录；未完成切换绝不因残留成功标记被当作可清理项。
- OfflineRollback/崩溃重放/AbortInitialization 需要同步处理，不能失败后留下一个“已确认安全”的假标记。

### 6.3 已有目录迁移

旧版没有 retention.json。启动时只对能通过现有 archived journal、对应成功 job、当前实例身份/已安装根标记相互核对，并确认当前正常初始化成功的那次历史恢复补成功证明。仅有目录名、mtime 或文件存在不能作为成功证据。

其他无法确认的副本继续保护，返回 `BACKUP_ROLLBACK_REVIEW_REQUIRED`；诊断写本地日志，HTTP 不暴露服务器路径。已经手工保留在 `reviewed-rollback` 的副本不搬回、不删除，不因没有新证明阻止恢复；它们属于此前人工确认的独立归档目录，本轮不自动遍历清理。

### 6.4 自动保留与清理

配置新增 `backup.rollback_keep_count`，缺省/0 按 **3 份**，允许 1–20，越界明确配置错误。首次默认即启用，不要求用户填写；不提供 0 份以免正常恢复后立即失去回退副本。

- 成功完成新一次恢复后，保留 completed_at 最新的 N 份成功副本（同时间按 operation_id 稳定排序）。只在启动收尾及备份恢复 worker 空闲的维护时机运行，独立于业务导入，不新增轮询服务。
- 最新成功副本、任何 plan/journal/回退任务正在引用的根、候选目录、未知副本、失败未处理副本不清理；存在保护引用时允许实际份数暂时超过 N。
- 先持久化 pruning 状态，再删除已确认位于 rollback 根内的指定旧 operation 目录；符号链接/Windows reparse point、越界、活动 business root 一律拒绝。恢复中断后可继续幂等清理。
- 清理成功写 audit；成功记录摘要可留在独立控制审计中。不能在同一轮删除恢复收尾仍会使用的根或编号上界来源。
- 自动清理失败只形成日志/审计警告 `rollback_cleanup_pending`，保留副本并在下次空闲重试；不能把已恢复成功改成失败，也不能重新用该成功副本阻止下一次恢复。磁盘实际不足时仍按原空间检查返回 `BACKUP_SPACE_INSUFFICIENT`。
- 控制目录跨进程实例锁、单重任务和维护闸门继续保留，防止清理与离线回退并行；本轮仍只支持既有单实例部署。

### 6.5 API、用户提示及权限

不新增需要前端调用的“处理旧副本”接口。用户仍然上传 → 预览 → 确认 → 等待重启 → 核对恢复结果；成功后可重复恢复。

在 GET backup/capabilities 的 data 中新增只读 `rollback_retention:{keep_count:3,managed:true}`，便于后续页面解释“系统自动保留最近 3 次恢复前的数据”。不暴露路径，不要求用户确认清理。

- `BACKUP_RESTORE_PENDING`：真正有计划等待重启，提示先完成或取消。
- `BACKUP_ROLLBACK_REVIEW_REQUIRED`：仅用于异常/无法证实安全的旧副本，提示联系部署人员；正常成功留存项不返回此错误。
- `BACKUP_STATE_CONFLICT`：仍用于取消/清理等不适用状态，不再笼统提示“刷新即可”。
- `BACKUP_STORAGE_ERROR`：控制记录无法读取/保存等存储异常；不得吞掉错误放行。
- Cookie、CSRF、Origin、恢复确认短语、request_id、包 digest、目标 instance_id 均保持。真正的异常恢复保护不因易用性要求被撤掉。

## 7. 三项整合与实施组织

研发应使用 `$engineering-quality-preserving-refactor`，开始前读取 `C:/Users/MalfuRisa/.codex/skills/engineering-quality-preserving-refactor/SKILL.md`。

| 职责 | 建议改动位置 | 应复用的边界 |
| --- | --- | --- |
| active/fixed 选取双方 | release_compare.go、types、handler、release.proto | 共用 loadCompareSnapshot/compareSnapshots，不复制内容算法 |
| 编号分配/回填 | release_numbering.go、release DAO/model、初始化 | 同一数据库 gate 事务，不在 handler 加计数 |
| 自动创建与旧请求确认 | release 创建 service/handler | 拆分 create 与 activate 输入校验；旧指纹按原合同确认 |
| 备份 schema 3 及编号合并 | backup/schema、validate、switch、auth-preserve 相邻独立职责文件 | 旧白名单分版本；合并编号单独函数，不混入密码处理 |
| 副本状态、证明和轮换 | backup/rollback_retention.go 及测试 | online/offline/boot 共用，避免散落 if 目录存在 |
| 配置与接口说明 | pkg/config、API 文档 | 保持 Fork 文档格式、BasePath、鉴权 |

修改 IDL 后用 hz 生成，禁止手改 pb/router。生成工具不可用时报告阻塞，不手工模拟生成文件。新 handler 显式保持零值/null 字段。

实施顺序：

1. A：扩展比较模式与选择层，旧 active 合同先保持可用。
2. B：编号模型/迁移/事务分配、自动创建及旧幂等请求兼容。
3. 同批补齐 schema 1/2/3 恢复、编号上界保留，避免新建版本功能破坏备份。
4. C：副本分类/成功证明/旧目录识别/保留策略，online/offline 共用。
5. 独立实例跑完整连续恢复和发布流程，更新 API 文档及本 PRD 实施登记。

自动编号是创建 API 的明确变更，前端后续需升级，但本批次不改 Vue、不自动派发。本轮也不升级当前演示实例、覆盖用户数据或替用户执行恢复。

## 8. 验收清单

### A：版本定向比较

| 场景 | 预期 |
| --- | --- |
| v2→v5 有增删改 | summary、before/after、changed_fields 与实际相符 |
| 交换为 v5→v2 | 增删对调、修改/未变计数不变，前后值对调 |
| 相同版本自比 | items:[]、零变化，baseline/target 固定 |
| fixed 比较中切换 active 或 A→B→A | fixed 翻页继续；active 比较/确认按 revision 冲突 |
| 任一侧已删/不匹配 snapshot、跨 scope/同名重建 | 对应 404/409/410，明确 side，无串范围 |
| 无 mode 的原请求、无 active 首次发布 | 原合同保持；首次 baseline:null |
| fixed 结果用于发布 | 必须重新 active compare，不能把 null revision 用于静默生效 |
| 资源 ID 不同但同内容、富文本/CSS、对象键/数组、大整数 | 复用原用例且比较结果不退化 |

### B：编号与兼容

| 场景 | 预期 |
| --- | --- |
| 两个 scope 各创建两次 | 各得 v1/v2，active 不随创建变化 |
| 同 scope 不同 request_id 并发创建 | 成功请求编号唯一递增；无重复版本或错配文件 |
| 同 request_id 并发及响应丢失重放 | 同一 snapshot/编号，计数只增一次 |
| 删除最高编号、切回旧版、普通重启 | 下次编号不倒退、不复用 |
| 创建复制失败/事务失败/提交后断开 | 未提交可回滚；提交结果原请求可确认，不能双占号 |
| 旧 v1.0/test-1、v5/已删 v9 迁移 | 旧名不变，按规则接续；重复迁移不继续加号 |
| 旧手填请求已提交后重放、新手填请求 | 前者确认原版本；后者明确 400，不悄悄换名字 |
| 同名重建 scope | 新身份 v1，旧请求不能写入新代 |
| int64 临界值 | 最后合法编号可用，下一个明确耗尽，数字不经 float64 |
| 备份后创建 v2，再恢复 v1 时点备份 | 配置回到备份内容，下次 v3；CLI 回退及崩溃重放也不降低上界 |
| schema 1、2、3 真实包 | 分别验证并恢复；现有用户 schema 2 包不得被拒绝 |

### C：自动副本管理

| 场景 | 预期 |
| --- | --- |
| 连续两次上传同一有效包，分别确认并重启 | 两次成功；第一次副本保留但不挡第二次 |
| 默认策略连续恢复四次 | 最新三份成功副本保留，最旧安全副本自动轮换，审计可核对 |
| 显式 N=1/3、非法值 | 按规则保留，非法值不静默改变策略 |
| 只有 StageCommitted、正常初始化随后失败 | 仍可原路回退，不被误标成功或自动清理 |
| 正常初始化完成但收尾各阶段中断 | 重启幂等补齐，无误删、无“成功副本永久拦截” |
| 旧版单份已成功恢复目录 | 用 archived journal/job/根验证后自动纳管，用户无需手工删 |
| 未知目录、损坏 journal/retention、目录读取失败 | 明确保护或存储错误，不能当成功副本放行 |
| 存在待重启/rollback_required | 保留准确拦截，重复上传不能绕过 |
| 自动删除无权限/Windows 句柄异常 | 警告并保留数据，不把已成功恢复改成失败，不凭副本存在拒绝下次恢复 |
| 正在被回退/恢复使用的根、symlink/reparse 越界 | 不自动删除，禁止跨根路径操作 |
| CLI 最新副本回退、手动 reviewed-rollback 旧归档 | 前者仍可用；后者完整保留且不参与自动删除 |

验证要求：Go 服务/DAO/handler/backup 行为测试、鉴权/维护分类与受影响回归、vet；独立 SQLite 实例做真实 HTTP 与实际重启闭环，不止测“候选已准备”。并发编号至少构造两个独立进程共享测试 DB，验证现有数据库闸门确实协调；这不是宣称已支持 K8s。

备份包必须包括本轮生成的 schema 3 和保留的真实旧 schema 2，schema 1 用可信旧版夹具或旧程序产包；不能只修改 manifest 的版本号冒充兼容测试。使用临时库/端口，不对 18080/18081 等用户实例反复恢复。MySQL/PG、跨卷、race 若未执行，明确登记。

## 9. 前端交接与交付方式

后端完成后给出接口示例、自动编号兼容说明及真实结果；前端需做三处适配：

1. 比较可选双方和交换方向，默认 active 模式；历史比较不能绕过发布前重新确认。
2. 创建不再提交手填 version，成功后使用返回编号继续比较/切换，保留原 payload 幂等重试。
3. 正常成功副本不再要求“处理旧数据”，可简短说明自动保留数量；只对真正异常显示人工处理提示。

实施进度、真实测试及限制登记在本文件末尾；不要把设计写成已完成，不为普通中间修复另写桌面报告。当前授权只到本 PRD，不自动发消息、改业务代码、提交/推送或部署。

## 10. 实施登记（2026-10-26）

### 已完成

| 任务 | 实施结果 |
| --- | --- |
| A 任意版本比较 | `POST /release/compare` 新增 `comparison_mode=active|fixed`；fixed 通过 `baseline_version` + `expected_baseline_snapshot_id` 选取两侧，不读 head；active 字段与 fixed 字段混用 400（handler 按原始 JSON 字段存在性判断，空串也拒绝）；fixed 响应 `active_revision`、`target_is_active` 为 null；错误增加 `side:baseline|target`；方向交换、自比与分页复用原 `compareSnapshots`/规范化算法。 |
| B 自动编号 | `config_release_head.last_version_number` 由 AutoMigrate 增加；启动调用 `db.EnsureReleaseNumbering` 为所有已知 scope 补 head 并取历史规范 `vN`（含已删）最大值，幂等且不改 active/revision；create 在既有 `withWriteGate` 事务内分配 N+1，失败/回滚不消耗编号；HTTP 新请求省略/空 version 自动分配，非空返回 400 `RELEASE_VERSION_AUTO_ASSIGNED`；auto 指纹使用 `create-auto-v1`；旧手填 request_id 仍按原指纹确认；int64 耗尽返回 409 `RELEASE_VERSION_SEQUENCE_EXHAUSTED`。 |
| B 备份兼容 | 业务 schema 升为 3；保留 schema 1、2 独立白名单；恢复 schema 1/2 时在隔离候选库补列并用历史版本回填；恢复/CLI 回退在替换根前读取旧根编号上界，按相同 `scope_id` 取 `max(候选值, 旧根值, 双方历史最大值)`，不导回旧根新增数据；备份创建也保证私有副本满足 schema 3 不变量。 |
| C 恢复副本管理 | 删除“非空 rollback 目录一律拦截”；新增 `rollback/<operation>/retention.json`，只在基础初始化全部成功后由 `CompleteStartup` 幂等写入；状态分类区分 pending、rollback_required、成功留存、损坏/未知；成功副本按 `backup.rollback_keep_count`（默认 3，1-20）保留最新 N 份，先写 pruning 再删除；目录读取/记录损坏分别返回存储错误或 review；`reviewed-rollback` 不遍历不删除；online/offline/boot 共用分类器与清理函数。 |
| 接口与文档 | 重新用 hz 生成 `release.pb.go`；更新 release compare/create 与 backup API 文档；capabilities 返回 `rollback_retention:{keep_count,managed}`。未改 Vue。 |

### 自动化验证

- `go test ./...`：通过（含新增 fixed 比较、auto 编号/回填/耗尽、handler fixed/auto/replay、schema 1/2 升级、编号合并、retention 分类/轮换/中断清理测试）。
- `go vet ./...`：通过。
- `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build`：通过。
- schema 2 测试由本轮生成的 schema 3 包真实移除 `last_version_number` 列并重写 manifest/摘要后校验升级；schema 1 测试进一步移除身份列、`scope_lifecycle_event` 并重建旧表后校验身份迁移和编号回填。没有只改 manifest 版本号；但两者都是从当前结构反向构造的夹具，不是旧二进制原件。

### 真实 HTTP 与重启验证

独立目录 `D:/tmp/rb-release-e2e`，SQLite，端口 18120，`rollback_keep_count=1`：

1. 登录、锁版后 HTTP create 无 version，返回 `v1`；显式 `version=v9` 返回 400 `RELEASE_VERSION_AUTO_ASSIGNED`。
2. 创建 v2 后 fixed `v1→v2` 得 added=1，反向得 removed=1；自比 items=[]；fixed 混入空 `expected_active_revision` 返回 400；baseline snapshot 不符返回 409 `RELEASE_SNAPSHOT_MISMATCH` 且 `side=baseline`；active 比较返回字符串 revision 和布尔值。
3. 切回 v1、删除 v2 后自动创建得到 v3，确认删除/回退不复用编号。
4. 恢复 v1 时点备份并重启；恢复后数据库只剩 v1，head 计数器由旧根合并为 3；再次 create 得到 v4。第二次用同一包恢复、再次重启：成功，说明成功副本不再阻止下一次恢复；`keep_count=1` 时旧副本被自动轮换，新副本 `retention.json` 为 `retained`，审计出现 `rollback_cleanup`。
5. 停止服务后执行 `backup-restore -command status` 与 `-command rollback`：CLI 回退成功；再次启动后恢复 v1+v4、计数器仍为 4，说明 CLI 回退和崩溃重放不会降低上界。
6. 双进程并发：两个 E2E 二进制共享同一 SQLite 库（端口 18130/18131，backup disabled），同时以不同 request_id 自动创建，均 HTTP 200，分别得到 v1/v2，head `last_version_number=2`，验证现有数据库写闸门跨进程协调。

### 未执行/边界

- 未执行 MySQL/PostgreSQL、跨卷、K8s 多 Pod 或热恢复；本轮仍只支持现有单实例 SQLite 恢复重启。
- 未执行 `-race`：当前 Windows 环境没有 gcc，`go test -race` 因 CGO 不可用而无法启动；已执行上述普通测试、vet 和 Linux 构建。
- 未修改 Vue、未提交/推送、未升级演示实例、未对 18080/18081 等用户实例执行恢复。
- 前端需要同批改为 create 不传 version；本批次后端已明确返回 400 防止旧页面静默改号。
