# 原因选填与非生效版本逻辑删除：后端 PRD

日期：2026-10-20。状态：后端实现已完成，本机SQLite复核验收通过；前端待接入。需求已由用户确认。

## 1. 目标与工作位置

面向小团队本机演示，减少反复填写原因的操作，提供清理无用版本的接口。用户已确认：原因选填；只允许逻辑删除非生效版本。

实施位置：`C:/Users/MalfuRisa/.codex/worktrees/admin-login-integration/rainbow_bridge`，分支 `codex/admin-login-integration`，以整合提交 `731dd9c` 和当前固定演示密码、启动器修改为基线。保留已有未提交内容，不覆盖原仓库和其他工作树。本轮写 PRD 并亲自完成后端，不改 Vue，不派发，不提交、不推送、不修改现有实例业务数据。

研发使用 `$engineering-quality-preserving-refactor`（编码要求），按职责拆分：删除编排放 `release_delete.go`，DAO 负责 SQL，handler 负责绑定/响应。复用数据库写协调和幂等指纹，不增加任务队列、权限平台或物理清理系统。

## 2. 改动前的现状

- `freeze_service.go`、`release_service.go` 的原因校验拒绝空值，使用字节长度限制500；前端也有必填限制。
- `ConfigReleaseVersion` 没有删除标记；列表、详情、指定版本读取和快照资源下载都能访问历史版本。
- 版本唯一约束、独立资源文件、生效指针和追加式事件已存在；`withWriteGate` 以数据库事务协调所有管理写操作，适合复用以避免删除和切换竞态。
- 初版没有版本删除接口。本需求引入“不可变内容之外的生命周期状态”，不修改已创建快照正文或复用版本号。

## 3. 原因选填

适用现有 `POST /api/v1/freeze/lock`、`/freeze/unlock`、`/release/create`、`/release/activate`，以及新增删除接口。

- 字段名保留 `reason`，不破坏旧调用方；请求省略、空字符串、纯空白均合法，trim后保存为空字符串。
- 有内容时 trim 后最多500个 Unicode 字符（中文按一个字符计算），超过返回400，沿用冻结/发布各自错误码。
- 类型错误仍按现有绑定规则处理；不为本需求改变 JSON null 的既有绑定行为。
- 不自动填充“测试”“默认原因”等文案。操作者、时间、状态变化和事件照常记录。
- 保留登录、CSRF、冻结检查、revision 条件更新和 request_id 幂等约束。
- 前端后续将输入框改为“备注（选填）”，空原因显示“未填写备注”；兼容冻结生成响应省略空字段，按 `reason ?? ''` 读取。

## 4. 删除接口

新增 `POST /api/v1/release/delete`，路径自动叠加部署 BasePath。与其他管理写接口一样必须登录并携带可信 Origin、`X-CSRF-Token`，身份来自服务端会话。

请求示例：

```json
{
  "environment_key": "default",
  "pipeline_key": "default",
  "version": "2",
  "expected_snapshot_id": "<列表中的快照UUID>",
  "request_id": "<本次操作UUID>",
  "reason": ""
}
```

请求字段约定：

| 字段 | 必填 | 约束 |
| --- | --- | --- |
| `environment_key` | 是 | trim后非空，沿用现有范围键长度上限191 |
| `pipeline_key` | 是 | trim后非空，沿用现有范围键长度上限191 |
| `version` | 是 | trim后匹配 `^[a-z0-9][a-z0-9._-]{0,63}$`；不允许保留字 `active/current/latest` |
| `expected_snapshot_id` | 是 | 列表中目标版本的UUID；必须与该范围、该版本的实际snapshot_id完全匹配 |
| `request_id` | 是 | UUID；在删除事件内唯一，遵循既有事件类型与request_id组合约束 |
| `reason` | 否 | trim后0—500个Unicode字符，省略等价于空字符串 |

request_id 首次点击生成，确认原请求时整份重发；改变目标或备注则用新 request_id。不额外要求填写 active revision，后端在写协调事务内检查实时生效指针。删除不要求编辑区锁定或解锁，允许对历史非生效版本清理；冻结状态没有变化。

成功 HTTP 200，显式 JSON 字段（空备注和 false 不省略）：

```json
{
  "code": 200,
  "msg": "OK",
  "data": {
    "request_id": "<本次操作UUID>",
    "snapshot_id": "<快照UUID>",
    "version": "2",
    "deleted": true,
    "deleted_at": "<UTC RFC3339>",
    "deleted_by": "admin",
    "reason": "",
    "replayed": false
  }
}
```

| 情况 | HTTP / error_type |
| --- | --- |
| 缺少参数、非法UUID、备注超长 | 400 `RELEASE_INVALID_REQUEST` |
| 未登录、缺CSRF或非法来源 | 沿用401/403认证错误 |
| 范围或版本不存在、跨范围定位 | 404，沿用范围/版本不存在错误 |
| expected_snapshot_id 不一致 | 409 `RELEASE_SNAPSHOT_MISMATCH` |
| 目标是当前生效版本 | 409 `RELEASE_DELETE_ACTIVE` |
| 同一删除 request_id 改了请求内容 | 409 `RELEASE_REQUEST_CONFLICT` |
| 新请求删除已删除版本 | 410 `RELEASE_VERSION_DELETED` |
| 同一删除 request_id、相同请求重放 | 200，原删除时间/操作者/备注，`replayed:true` |

错误使用已有JSON信封：`code`、`msg`、`error`、`error_type`，HTTP状态与错误码一致；不把业务错误包装成HTTP200。身份认证先由中间件执行，避免匿名请求得到版本状态。通过认证后先检查参数，再在事务内检查已提交删除事件：相同request_id但指纹不同优先返回冲突，相同指纹返回原结果；新请求按范围、目标快照、已删除状态、生效状态顺序判断。服务内部也必须验证可信管理员身份，不依赖调用方自报actor。

## 5. 数据与事务

版本表 `config_release_version` 新增：

| 字段 | 数据语义 |
| --- | --- |
| `deleted_at` | nullable时间，NULL代表未删除，非NULL代表已删除；时间由服务端生成 |
| `deleted_by` | 最多64字符，NOT NULL，默认空字符串；成功删除时写入会话账号名 |
| `delete_reason` | 最多500字符，NOT NULL，默认空字符串；成功删除时写入归一化备注 |

显式判断删除，不使用 GORM 全局软删自动过滤，避免创建时忽略占位版本或旧请求误建新快照。复用启动时AutoMigrate，迁移只新增列/必要索引，旧行 `deleted_at=NULL`，不得重建或清空版本表、取消唯一约束、重写快照目录。旧二进制不认识删除状态，上线后不得让旧实例继续对同一数据库提供版本读取或写入。

新增 `delete` 类型事件；沿用 `(event_type, request_id)` 唯一约束，指纹包含操作、范围、版本、expected_snapshot_id、trim后备注。版本标记与审计事件同事务提交；审计失败全部回滚。

删除在 `withWriteGate` 内完成：重放检查 → 范围/版本/快照匹配 → 已删除检查 → 查询实时生效指针 → 标记删除 → 追加事件。切换也在同一写协调中检查目标未删除。因此：切换先成功则删除被拒；删除先成功则切换被拒。删除不改变冻结状态、生效指针或 head revision。

事件记录目标snapshot_id/version、原始备注、操作者和时间；删除事件的 `from/to active` 与 `from/to head revision` 相同，因为删除没有切换生效版本。不存在head时记录空指针和revision 0，不为删除创建head。沿用写协调的60秒总期限和10秒协调等待期限；超时或取消遵循既有503契约。提交后响应丢失时，调用方用同一请求确认，不能另发请求推断首次失败。

保留所有版本号唯一索引、payload、资源清单和磁盘文件；不释放磁盘、不提供恢复/回收站界面或物理清理接口。环境/渠道原有删除保护继续统计包括墓碑在内的历史版本，避免重建同范围后复用旧版本号。本轮不解决父级范围物理删除。

## 6. 已删除版本对其他接口的影响

- 正常版本列表过滤已删除项，分页基于过滤后的集合，不产生空洞页；历史记录继续显示创建、切换、删除事件。
- 管理详情、指定版本 runtime（含raw/json）、静态ZIP导出、旧快照资源URL、新切换操作均拒绝已删除版本，HTTP410 `RELEASE_VERSION_DELETED`，错误响应 `Cache-Control:no-store`。不存在的目标继续404。
- 创建同名版本返回409 `RELEASE_VERSION_EXISTS`；原创建请求的重放若目标已删，返回410，不能因重放恢复可用状态。
- 原切换请求在目标被删后重放，仍只确认历史切换成功并返回目前 active，沿用既有幂等语义，不再次切换，不复活目标。新 request_id 切向已删除版本则410。
- 默认读取仍跟随当前生效版本。若数据库被外部修改导致 active 指向已删除版本，明确503 `RELEASE_INCOMPLETE`，不回退编辑区或其他版本。
- 请求在删除提交前已经取得未删除版本行，可以完成本次读取/导出/下载；删除提交后新取得版本行的请求返回410。底层文件保留，避免在途请求读取一半丢文件。
- 删除只能阻止后续服务端读取，不能撤回调用方已经缓存的配置或下载文件。

关键受影响接口清单：

| 接口 | 已删除目标的行为 |
| --- | --- |
| `GET /api/v1/release/list` | 从正常列表排除；`has_more`与游标在排除后计算 |
| `GET /api/v1/release/detail` | 410 |
| `POST /api/v1/release/activate` | 新请求410；历史成功请求按前述幂等规则处理 |
| `GET /api/v1/runtime/release?version=...` | json/raw均410 |
| `GET /api/v1/runtime/release/static?version=...&expected_snapshot_id=...` | 正确定位已删目标后410；快照不匹配仍409 |
| `GET /api/v1/release/asset/<snapshot_id>/<asset_id>` | 先检查所属快照状态，已删410；文件仍在磁盘也不得绕过 |
| `GET /api/v1/release/history` | 保留全部历史，新增 `event_type:"delete"` |

编辑区配置和原始上传资源不属于被删除快照，不随之删除或禁用。逻辑删除只改变目标快照的对外可用状态；不更改其他版本的配置、资源或已生成离线ZIP。

## 7. 前端后续接入

本轮不改前端。旧页面仍有原因必填校验，需要下一轮接入；后端接口先允许省略。删除入口仅对非生效项显示，普通确认弹窗提醒“固定使用此版本的应用将无法继续读取；底层文件暂时保留”，备注选填。无需重新输入密码或版本名。

成功后重新拉取列表和 active，不手动猜测状态。409按 `error_type` 分别处理：`RELEASE_DELETE_ACTIVE` 提示目标已生效并刷新；`RELEASE_SNAPSHOT_MISMATCH` 提示目标变化并刷新；`RELEASE_REQUEST_CONFLICT` 提示本次请求标识冲突，不自动更换标识重试。410刷新后移除旧项。网络失败时保留首次payload和request_id供“确认结果”，不要自动生成新请求。历史增加 `delete` 文案，不把它显示成创建或切换。

## 8. 实施与验收

1. 写本PRD，新增显式生命周期字段、DAO逻辑删除与列表过滤。
2. 公共原因归一化；增加删除编排、错误码与所有读取入口的删除检查。
3. 修改IDL并通过hz生成模型和路由，不手改生成文件；实现独立删除handler。
4. 补下列正式测试，并运行Go全量测试、vet、真实隔离实例HTTP冒烟；不拿Mock结果替代真实接口结果。

| 编号 | 具体输入/触发与预期 |
| --- | --- |
| B01 | 四种原操作不传/空/空白备注成功，DB为空；中文500合法、501拒绝；状态及actor记录正常 |
| B02 | 删除非生效版本成功，head/freeze不变；payload、清单、资源字节保留；列表隐藏、历史保留 |
| B03 | 生效版本删除409；未知/跨范围404；快照不匹配409；都没有删除标记或删除事件 |
| B04 | 同request_id重试200且单条事件、原始时间与actor不变；改字段409；新ID删墓碑410 |
| B05 | 已删详情、runtime两格式、ZIP、直接资源、切换均410；默认active和其他版本仍可读 |
| B06 | 已删版本号新建409；原创建请求重放410且不新建目录；切换历史重放不改变当前active |
| B07 | 删除与切换并发，只能得到合法串行结果，不能产生active指向墓碑；并发相同删除ID只一条事件 |
| B08 | 删除审计失败回滚标记；重启/新服务对象仍识别墓碑；旧表迁移后既有版本仍可读 |
| B09 | 暂停已取得版本行的读取/导出，提交删除后放行，在途成功；后续请求410 |
| B10 | 注册路由枚举覆盖删除鉴权；真实HTTP检查无Cookie401、无CSRF403、管理员删除200与公开读取410 |

测试使用独立SQLite和临时目录；MySQL/PostgreSQL无真实实例则明确未执行，不声称已验。保持当前运行实例数据不变。

## 9. 最终结果

### 9.5 后端最终交付与复核验收

日期：2026-10-20。结论：**本机SQLite后端验收通过，未发现阻断前端接入的问题。**前端尚未实现，不能据此认定页面已经支持选填备注或删除。

最终行为：四种原操作和新增删除接口均支持空备注；备注按Unicode字符限制500字。逻辑删除仅允许非生效版本，保留payload、资源清单、资源文件和版本号唯一占位；列表过滤，详情、固定runtime、ZIP和快照资源返回410。删除与切换同用数据库写协调，删除标记与事件同事务，原请求重试不复活版本。

主要落点：`biz/service/operation_reason.go`、`release_delete.go`及读取入口；`biz/dal/model/release.go`、`biz/dal/db/release_delete.go`和列表过滤；`biz/handler/release/release_delete.go`；IDL及hz生成路由/模型；服务层专项`release_delete_test.go`。

本次复核实际执行与证据：

| 项目 | 结果与证据 |
| --- | --- |
| Go全量 | `go test -mod=readonly -tags=dev -count=1 -timeout 180s ./...`通过；`.dev-integration/cleanup-accept-go.log` |
| 构建与静态检查 | `go build -mod=readonly -tags=dev`生成独立复核程序；`go vet -mod=readonly -tags=dev ./...`通过；`.dev-integration/cleanup-review/vet.log` |
| 注册路由鉴权 | 重新执行路由枚举，47条声明路由/34条管理路由通过，包括新增删除；`.dev-integration/cleanup-review/routes.log` |
| 独立HTTP冒烟 | 当前源码重新编译、独立数据库、18083：31项通过，涵盖空备注、鉴权、删除守卫、幂等、列表/历史和公开读取；`.dev-integration/cleanup-review/instance-28d73af6/http-smoke.json` |
| 真实重启补验 | 同一隔离库重启后，默认版本仍v1.0，已删文件字节仍在；raw/json固定读取、详情、ZIP、资源共5个入口均410且no-store；同目录`restart-probes.json` |
| 事务/并发/在途语义 | 全量测试实际包含删除守卫、审计失败回滚、删除与切换并发、同request_id并发、暂停后的在途读取/导出、异常active及父级保护等专项 |
| 旧表迁移 | 复核研发留存的两代实例日志与JSON：旧版本迁移前后均v1.0，payload摘要相同；新建并删除v2后列表只留v1；`.smoke-cleanup/migration/`。本次未重复搭建旧二进制迁移演练 |

接口与前端契约以本文第3—7节为准，前端实施要求见同目录`release-cleanup-frontend-v1.md`。旧文档副本已同步，不保留“尚未验收”和“已完成”相互冲突的阶段结论。

未验证边界：MySQL/PostgreSQL未用真实实例执行；未做真实HTTP并发压测或部署混用旧二进制演练。逻辑删除不释放磁盘、不提供恢复，历史版本仍阻止环境/渠道删除，这是本版已确认边界。

本次只增加复核证据与PRD，未修改后端业务代码或Vue。复核临时进程已停止，18083已释放；未停止或替换18080/18081，未修改其业务数据。代码仍在整合工作树，未提交、未推送、未部署到现有演示实例。