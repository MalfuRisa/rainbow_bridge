# RB-FREEZE-01 交付说明:按环境与渠道提供可解除的配置冻结

状态:已实现并通过本机专项验证与既有 Full 回归;2026-10-19 按用户要求与前两轮优化依次整理提交到 Fork,未向上游提交 PR。未声称生产验收通过。
分支:`codex/config-freeze`(origin=MalfuRisa/rainbow_bridge,upstream=yi-nology/rainbow_bridge)。
基线:PRD 文档提交 e0e6823;实现期间保留既有未提交批次(biz/* 优化、QA 脚手架)不动、不回退。

## 1. 写入口覆盖表(全部经同一写协调)

| 写操作 | 入口 | 守卫位置 | locked 行为 |
| --- | --- | --- | --- |
| 配置新增/更新/删除 | `/api/v1/config/{create,update,delete}` | `Service.Add/Update/DeleteConfig` → `withWriteGate` + `ensurePipelineUnlocked` | 409 SCOPE_FROZEN(HTTP 409 + code 409 + error_type) |
| 资源上传 | `/api/v1/asset/upload` | `Service.UploadAsset`:冻结检查先于任何目录/文件创建;文件+元数据同一协调 | 409,且不产生文件/目录 |
| JSON 导入 | `/api/v1/transfer/import`(JSON 体) | `Service.ImportConfigsWithResult`:写前对全部目标范围整体检查 | 409,包括 overwrite=false |
| 归档导入 / 选择导入 | `/api/v1/transfer/import`(archive)/ `import-selective` | `ImportArchiveWithResult`:单次协调跨"校验→元数据准备→配置提交→资源恢复";目标集合含映射后范围 + 包内将修改的环境/渠道元数据 | 409,零数据库/资源写入 |
| 迁移 | `/api/v1/transfer/migrate` | `MigrateConfigs`:目标 locked 在复制资源前整体拒绝;源 locked 允许只读复制 | 目标 409;源不受限 |
| 渠道更新/删除 | `/api/v1/pipeline/{update,delete}` | `Update/DeletePipeline` + `ensurePipelineUnlocked` | 409 |
| 环境更新/删除 | `/api/v1/environment/{update,delete}` | `ensureEnvironmentMetadataUnlocked`(环境内任一渠道 locked 即拒绝) | 409 |
| 新建渠道/新建环境 | `/api/v1/pipeline/create`、`environment/create` | 允许;新渠道补建 unlocked 状态行 | 正常 |
| 启动内容迁移 | `MigrateToFullAssetPaths` | 逐配置检查持久状态,locked 范围跳过并记录 | 跳过不改 |
| 内部资源辅助 | `restorePlannedResource`、`copyConfigAssets`、`importEnvironments/Pipelines`、`ensure*Exists` | 同协调事务内的 tx-scoped Logic;元数据写入走保存点,单条失败不毒化事务 | 由顶层请求的整体检查覆盖 |

冻结/解锁本身也在协调事务内:**状态更新与审计事件同一事务提交**,审计写失败则状态不变(测试:`TestFreezeAuditFailureRollsBackState`)。

## 2. 状态机与并发设计

- **状态**:`config_freeze_state` 按 (environment_key, pipeline_key) 唯一;`unlocked`/`locked`;`revision` 单调递增;初始 unlocked,存量渠道初始化补齐(幂等,不重置)。
- **事件**:`config_freeze_event` 追加式记录(event 自增 ID、类型、前后状态、revision、原因、actor=`freeze-admin`、UTC 时间);普通导入与父级删除不清除;删除重建渠道沿用原 revision。
- **条件更新**:`UPDATE ... WHERE revision = expected AND state <> desired`;过期 revision → 409 `FREEZE_REVISION_CONFLICT`(带 latest_state/latest_revision);目标状态已是当前状态 → 409 `FREEZE_STATE_UNCHANGED`,不追加虚假事件。
- **写协调行**:`config_write_gate` 固定单行(id=1);所有业务写入与冻结状态变更在事务的**第一条语句** UPDATE 该行(PostgreSQL/MySQL 行锁;SQLite 事务写锁)。
- **两阶段期限**:连接获取/取协调 ≤10s(超时 503 `FREEZE_WRITE_GATE_BUSY`);整次写操作 ≤60s(超时 503 `FREEZE_WRITE_TIMEOUT`,回滚并清理本次新建文件)。二者为变量,仅测试可缩短(生产 10s/60s)。
- **SQLite 语义**:单连接池下并发写在连接获取处排队(读取在写入期间排队等待,不失败);DSN 附加 `_pragma=busy_timeout(10000)` 以支持跨进程写冲突的有界等待(`pkg/database`,含单测)。
- **跨进程证据**:`script/freeze-two-process.ps1` 两进程共用同一 SQLite 文件:并发锁版 200,409 各一;另一进程写入 409;历史 1 条;重启后仍 locked;无凭据解锁 401、持凭据解锁 200、解锁后写入 200。

## 3. API 契约(idl/biz/freeze.proto,由 hz 生成模型与路由,handler 手写)

| 方法/路径 | 说明 |
| --- | --- |
| GET /api/v1/freeze/status | state、revision、last_changed_*、admin_configured |
| POST /api/v1/freeze/lock | 范围+reason+expected_revision+Bearer;锁定前受管资源预检 |
| POST /api/v1/freeze/unlock | 同上 |
| GET /api/v1/freeze/history | limit 默认 20 上限 100,before_event_id 游标,倒序,has_more/next_before_event_id |

- 真实 HTTP 状态与同值业务 code:200/400/401/404/409/503/500;machine-readable `error_type`。
- 既有写接口 409 载荷:`{code:409, error_type:"SCOPE_FROZEN", error:"scope <env>/<pipe> is frozen (state=locked revision=N)"}`;受影响响应结构已在 IDL 声明 `error_type` 字段(config/environment/pipeline/asset/transfer)。
- revision 与事件 ID 均为十进制字符串。

## 4. 管理令牌(最小可用方案)

- 只从进程环境 `RAINBOW_FREEZE_ADMIN_TOKEN` 读取(≥32 字符);缺失/过短:状态与历史可用,锁定/解锁 503 `FREEZE_ADMIN_NOT_CONFIGURED`,已冻结范围仍阻止写入。
- `Authorization: Bearer <token>`,`crypto/subtle` 常量时间比较;错误凭据 401,不改状态;actor 固定 `freeze-admin`。
- 令牌只授予冻结/解冻:对普通写接口没有绕过特权(服务层无 token 旁路)。
- 前端:仅存当前操作内存(pass 到请求头),关闭弹窗即清除;不写 localStorage/sessionStorage/URL/导出/日志;专项用例断言令牌不出现在页面 DOM。
- 审计与日志校验:实例日志不含令牌(grep 验证);既有 Logging 中间件不打印请求头。

## 5. 前端行为

- 配置页选择器下方新增冻结面板:可编辑/已冻结徽标、最近操作时间与原因、锁版/解除冻结/操作记录。
- 锁版/解除弹窗:目标范围在打开时快照(避免切换选择后锁错渠道)、显示当前 revision、原因 1–500、管理令牌;冲突时展示最新状态并重读;资源预检失败列出问题与外部引用提示("外部内容不受冻结控制")。
- 写入口禁用:新增/编辑/删除配置、资源上传、渠道删除、含冻结渠道的环境删除;只读、预览、导出保持可用。
- 导入页:预览后为受锁目标显示提示;409 展示实际受阻范围并刷新状态,选择不丢。
- 迁移页:目标 locked 阻止预览/迁移并给出原因;源 locked 提示允许只读复制。
- 页面已打开后被外部锁定:F22 用例覆盖——保存收到 409 时**保留草稿**、提示范围与原因、刷新状态、不显示成功。

## 6. 验证矩阵结果(F01–F24)

| ID | 方式 | 结果 |
| --- | --- | --- |
| F01 | `TestFreezeLifecycleAndHistory` + 启动两次(引导幂等) | 通过:初始 unlocked;重启不重置状态/事件 |
| F02 | 状态机用例 + freeze-ui 弹窗 | 通过:原因/actor/UTC/revision 与事件一致 |
| F03 | `TestFreezeBlocksAllWriteEntries` + freeze-ui | 通过:新增/更新/删除 409,内容与数量不变 |
| F04 | 服务层无令牌旁路 + `TestFreezeBlocksAllWriteEntries`(管理令牌不能绕过) | 通过 |
| F05 | `TestFreezeAdminCredentialAndValidation` | 通过:401/400/409,无状态变化、无伪审计 |
| F06 | 同用例(空 token) | 通过:控制操作 503,状态可读,写入仍 409 |
| F07 | 状态机用例(lock→unlock→revision 递增) | 通过 |
| F08 | `TestFreezeScopeIsolation` | 通过:同环境其他渠道/其他环境正常 |
| F09/F10 | `TestFreezeImportAndMigrateRules` + freeze-import 页面用例 | 通过:写前整体 409,资源/文件/哨兵不变 |
| F11 | 同用例 + freeze-migration 页面用例 | 通过:locked 源→unlocked 目标成功且源不变;反向在复制前拒绝 |
| F12 | 服务层:locked 上传拒绝且目录/文件未创建;封锁期间资源行不变 | 通过(元数据/字节不变) |
| F13 | 父级用例段(渠道/环境更新删除、新渠道允许) | 通过 |
| F14 | Full 回归(读/导出/静态包不受冻结影响;含 JSON 精度与路径处理既有用例) | 通过 |
| F15 | `TestFreezeGateSerializationAndBusy`(确定性:持锁期间 503,释放后锁成功) | 通过 |
| F16 | `TestFreezeConcurrencyRejectsAfterLock`(10 并发全 409、无新资源) | 通过 |
| F17 | `TestFreezeResourceStageTiming`(人工阻塞资源阶段:锁不提前成功) | 通过 |
| F18 | `TestFreezeAuditFailureRollsBackState` + `TestFreezeImportCancelCleansCreatedFiles` | 通过:有锁必有事件;取消回滚并清理本次新建文件 |
| F19 | two-process 脚本(重启仍 locked、解锁需凭据)+ Full 的 postrestart/recovery | 通过 |
| F20 | `script/freeze-two-process.ps1` | 通过(数据库级协调,非进程 mutex) |
| F21 | `TestFreezeLockPrecheck`(缺失/跨范围拒绝、外部 URL 提示、空渠道可锁) | 通过 |
| F22 | freeze-draft 页面用例 | 通过 |
| F23 | 历史分页用例段 + freeze-ui 历史弹窗 + 令牌不入 DOM/日志 | 通过 |
| F24 | `go test -tags=dev ./...` 全绿 + Full 回归结果见下节 | 见 §7 |

**未测组合(如实列出)**:MySQL/PostgreSQL 的协调行锁与迁移行为、MinIO 取消/写入边界、K8s、生产 embed 制品、线上网关鉴权。以上按 PRD 只在有独立环境时扩展。

## 7. 原 Full 回归

运行 `script/test-local.ps1 -Suite Full`(已排除 @freeze 专项),四阶段全部通过:
prefix-main 26/26、postrestart 1/1、recovery 1/1、root 5/5(运行 `20261016-200130-efzv`)。
冻结实施前规范运行中的既有 QA 缺陷(并发重复 alias、归档 Windows 往返、object 大整数精度、未知 API 200、外网字体等)已由并行批次在同一工作区修复,本报告按"冻结实现不引入新失败"验收,其结果模式为全绿。
冻结专项(4/4)与双进程验证(全过)在同工作区、同二进制上执行,与上述修复共存。

## 8. 兼容变化与边界

- **行为变化(明确)**:普通写入新增 409 SCOPE_FROZEN;等待超时/总期限为 503;写操作总期限 60s、协调等待 10s;SQLite DSN 附加 busy_timeout(10s);生产需配置 `RAINBOW_FREEZE_ADMIN_TOKEN` 才能使用锁定/解锁。
- 未冻结链路与既有错误契约(别名冲突、未找到、部分成功协议)保持不变。
- 资源副作用:单次协调跨越数据库与资源阶段;数据库回滚会清理**本次新建**文件,绝不删除既有共享资源;进程崩溃可能留下未引用文件(可定位残留,不宣称跨存储原子事务);MinIO 未联调。
- 冻结不是资源备份:外部 HTTP 内容、数据库直连、直接改磁盘不受控;第二阶段不可变快照另行设计。
- 运维约束:冻结上线后禁止旧版本进程共用同一数据库(旧版无冻结实现,可绕过规则)。

## 9. 已知遗留与风险

- 2026-10-19 提交检查已清理 `decorateConfig` 和测试打印中的 protobuf copylocks 告警,改用 `proto.Clone` 与指针打印;相关测试与 `go vet -tags=dev ./...` 通过。
- 既有 QA 缺陷的修复由并行批次在同一工作区完成并已被本轮 Full 回归覆盖;其交付与验收以该批次报告为准,本说明只声明冻结实现不引入回归。
- 本机 hz 生成遇到 protoc 路径遮蔽问题:模型由 protoc 直接生成后按约定归位,router/handler 用 hz 生成;冻结模型目录曾落入嵌套路径已修正,后续生成请注意 `--out_dir/--model_dir` 显式传入。

## 10. 复现命令

```powershell
# 后端与前端检查
go test -tags=dev ./...
cd vue && npx vue-tsc -b --force

# 冻结专项(隔离实例 + Chromium + 随机测试令牌)
& 'D:/kylin/rainbow_bridge/script/test-local.ps1' -Suite Freeze

# 双进程写协调(F20/F19)
& 'D:/kylin/rainbow_bridge/script/freeze-two-process.ps1'

# 既有完整回归
& 'D:/kylin/rainbow_bridge/script/test-local.ps1' -Suite Full
```

## 11. 2026-10-19 提交前复核

- 按三轮拆分为基础可靠性、实测缺陷修复与测试工具、配置冻结实现;前两轮通过独立快照检查,完整工作区已备份。
- 三轮快照分别通过 `go test -tags=dev ./...`;第二轮与最终版本通过前端类型检查及构建。最终版本 `go vet -tags=dev ./...` 通过,生产模式在准备嵌入前端文件后构建通过。
- Full 运行 `20261019-submit-full`:主套件 26/26,三次重启各 1/1,恢复 1/1,根路径 5/5,共 35 次用例执行通过。
- Freeze 运行 `20261019-submit-freeze`:专项 4/4 通过。
- 双进程运行 `20261019-090404-tayh`:并发锁版 200/409 各一,跨进程写入 409,重启仍冻结,无凭据解锁 401,持凭据解锁及后续写入 200。
- 修复双进程测试脚本对 PowerShell 7 错误响应类型的兼容问题,复测通过;首次失败是测试脚本读取错误体失败,不是冻结拦截失败。
- TypeScript 构建信息与生成的 Vite JS/声明文件退出版本管理并加入忽略规则;本机配置、个人上手笔记、`tiny.txt`、运行数据库、日志、截图和构建产物不提交。

验证附件保存在桌面产出目录的 `附件/本机测试/20261019-submit-full/` 与 `20261019-submit-freeze/`;双进程证据在系统临时目录 `rainbow-freeze-2p-20261019-090404-tayh/artifacts/`。MySQL/PostgreSQL/MinIO 等未测范围仍以 §6 为准。
