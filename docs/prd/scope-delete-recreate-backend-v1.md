# 后端任务一：解锁后删除环境/渠道，允许同名重建

日期：2026-10-25。状态：待实施。配套：[任务二：选择配置来源导出](transfer-source-export-backend-v1.md)。

## 1. 用户要求与完成标准

- 删除环境：全部**当前有效渠道**均未锁版即可删除，不再因生效版本、历史版本或已删除版本存在而阻止。
- 删除渠道：当前渠道未锁版即可删除，不要求先清空配置或逐个删除版本。
- 删除后同一个 environment_key/pipeline_key 可以重新创建；新范围初始为空、未锁版、无生效版本，可以重新使用 v1.0 等版本名。
- 旧配置、资源、生效指针和历史不能重新出现在新范围；旧的页面提交不能删除/修改重建后的新对象。

这是改变删除业务规则，不是只修改错误提示。不增加物理清理、恢复回收站、强制删除锁版范围或复杂权限。

## 2. 工作范围与现状

仓库 `D:/kylin/rainbow_bridge`，已提交基线 `main_v1/c9aefe5`。工作区另有尚未提交的备份后端、启动器及备份页面改动。实施前记录实际基线，使用独立工作树/分支 `codex/scope-delete-recreate`（待创建），不能复制旧代码覆盖备份成果；整合时必须适配实际备份模块。

当前 environment/pipeline 是软删除，唯一索引只包含业务key，故同名插入会冲突；配置、冻结、发布等主要按环境/渠道key关联。只去掉版本计数检查或只解除唯一索引，会让同名新旧数据混用。默认环境/渠道还会在启动时自动创建，必须一起调整。

涉及：模型/DAO、范围解析、环境/渠道删除与创建、受影响的配置/资源/冻结/发布/导入导出、缓存、启动迁移、备份白名单及API文档。前端视觉由后续任务负责，本PRD明确其必需透传字段。不得顺带重构鉴权、性能或存储平台。

## 3. 新旧数据区分：业务key相同，内部身份不同

采用**内部身份 + 逻辑删除标记**，不用用户填写的发布版本号代替身份。版本号只在某一批范围内唯一，不能区分未发布的配置或渠道。

### 3.1 数据字段

| 对象 | 新增或调整 | 作用 |
| --- | --- | --- |
| environment | environment_id：服务端UUID，稳定唯一；active_slot：可空字符串 | 每次创建是新身份；有效行为active_slot='active'，删除后NULL |
| pipeline | scope_id：服务端UUID；environment_id；active_slot | 每次创建渠道是新范围；属于确定一代环境 |
| config、asset | scope_id | 配置/原资源只属于该批渠道，不能只按key查找 |
| freeze state/event、release version/head/event | scope_id | 状态、版本、历史只属于该批渠道 |
| release asset | 继续通过全局唯一snapshot_id关联版本 | 无需重复附加scope_id，但查询需校验所属版本生命周期 |

原业务key保留不变，便于展示和API兼容。scope_id/environment_id只作为内部及请求的身份条件，不是权限令牌。

唯一约束改为：environment `(environment_key,active_slot)`；pipeline `(environment_id,pipeline_key,active_slot)`；配置 `(scope_id,resource_key)`及已有alias唯一约束按scope_id；冻结/head各scope_id唯一；版本 `(scope_id,version)`唯一。身份UUID各自唯一；snapshot_id/file_id/request_id现有全局唯一规则保持。

删除后active_slot=NULL，NULL多行语义适用于当前支持的SQLite/MySQL/PostgreSQL；实施需验证实际DDL及旧索引移除，不能把deleted_at放进组合唯一索引后假定有效行唯一性已解决。暂不运行的数据库如实登记未验。

### 3.2 初始化与重建

- 新环境生成新environment_id，自动创建的新默认渠道生成新scope_id。新渠道也是新scope_id。
- 删除后重建必须创建新行、新UUID，不清除旧行deleted_at来复活，不搬回旧配置/资源，不继承freeze/head。
- 已逻辑删除的旧v1.0仍占用**旧scope**版本名，但不占用新scope的v1.0。
- 默认环境/默认渠道与普通对象采用相同删除规则。系统默认种子只用于从未初始化的库；发现曾被用户删除的默认对象时，不自动重建。用户显式新建default环境时正常创建一个全新默认渠道。不得每次启动把用户删除对象“补回来”。

## 4. 删除流程与一致性

继续在现有备份维护守卫和数据库withWriteGate内执行；冻结检查和全部标记同一事务完成。

### 删除环境

1. 根据业务key定位有效environment，校验expected_environment_id。
2. 按environment_id取全部当前有效渠道，检查任何scope是否locked；有则409 SCOPE_FROZEN，返回实际受阻渠道，完全不删除。
3. 无锁则给环境和这些渠道设置deleted_at/active_slot=NULL；对应配置及原资源设逻辑删除，尚未删除的发布版本设置删除标记与scope_deleted原因，清空这些scope的active指针。此前已删除版本的原删除时间/操作者/原因保留，范围生命周期另行记录，不能覆盖旧审计事实。
4. 保留冻结状态/事件与发布历史记录，按旧scope归档；新增范围删除审计事件，记录操作者、时间、对象身份、受影响数量。不要把父级删除伪装成大量用户手动删版本事件。
5. 提交后按旧scope失效缓存，返回删除结果。失败整体回滚；不在事务内物理删除文件。

### 删除渠道

同上，只处理当前scope；只检查自身冻结状态，不能因为其他渠道被锁就拒绝。不得影响其他渠道或环境名称、配置、指针。

旧快照和原文件保留磁盘，不立即释放空间。所有新的原始资源下载、快照资源下载、配置详情、历史列表、导出都需校验其scope所属有效环境/渠道；已删除范围不能靠file_id/snapshot_id直接访问。已开始且已固定数据的流式下载/导出可完成，删除后新请求拒绝，不中途改读新scope。

## 5. API及错误合同

### 5.1 读取身份

环境list/detail/create/update响应增加environment_id；渠道list/detail/create/update、runtime/overview渠道条目增加scope_id（同时给出environment_id）。配置管理、freeze、release元数据响应携带scope_id。发布不可变payload及其既有摘要不重写，scope身份作为表列/响应外层元数据。

新普通查询按业务key解析**当前有效身份**后再查业务表；直接已删除身份返回410，不退回同名新身份。用scope_id不能越过账号鉴权或跨环境边界。

### 5.2 删除请求（字段为必需）

```json
{
  "environment_key": "prod",
  "expected_environment_id": "11111111-1111-4111-8111-111111111111"
}
```

渠道删除：

```json
{
  "environment_key": "prod",
  "pipeline_key": "main",
  "expected_scope_id": "22222222-2222-4222-8222-222222222222"
}
```

保留POST `/api/v1/environment/delete`、`/pipeline/delete`，Cookie/CSRF不变。缺失或非法身份400 `SCOPE_INVALID_REQUEST`；同key已重建、身份不同409 `SCOPE_IDENTITY_CONFLICT`；指定旧身份已删除410 `SCOPE_DELETED`；从未存在404 `SCOPE_NOT_FOUND`；存在冻结409 `SCOPE_FROZEN`。不再以`RELEASE_SCOPE_HAS_VERSIONS`阻止这两个删除。

成功HTTP200/code200，data含deleted=true、目标environment_id/scope_id、deleted_at、affected（pipelines/configs/assets/versions计数）。环境有生效版本也允许删除；前端确认需说明“该范围的配置接口将停止提供数据”，不要求多输密码或先删每个版本。删除请求不新增request_id：结果丢失后查询对象身份；禁止仅凭同key再次自动删除。

### 5.3 防止旧页面操作新范围

删除重建会让freeze/head数字revision重新从0开始，所以只检查原revision不够。

- 修改环境、创建渠道请求带expected_environment_id；修改/删除渠道以及对现有scope执行的配置创建/更新/删除、资源上传、freeze lock/unlock、release create/activate/delete带expected_scope_id。
- 后端先校验UUID与当前身份匹配，再用既有edit_revision/freeze revision/head revision/request_id规则；不能只在handler里比较。
- 无身份的旧管理客户端需同批升级，不能保留默认跳过条件；前端字段透传属整合依赖，不在未适配页面上单独启用。
- 管理列表/详情可带scope_id定位，普通页面默认只显示当前范围。导入/迁移在写协调内解析目标身份一次并绑定整个操作；用户有明确选中的目标时透传其expected身份，不能准备阶段解析旧目标、提交阶段自动换新目标。归档来源身份不能作为目标身份直接落库。

### 5.4 业务读取与固定版本

按key的默认runtime继续跟随当前有效scope，新范围没有active时404 RELEASE_NOT_ACTIVE。固定`version=v1.0`也在**当前有效scope**内解释。

为真正固定一批范围，runtime/release及固定静态导出支持可选scope_id，并在响应返回scope_id；携带旧scope_id后父级已删返回410 SCOPE_DELETED，不解析成同名新v1.0。不带scope_id的兼容调用方在同名重建后会读取新范围，必须在API文档明确这一语义变化；希望长期固定应使用scope_id/version，并在导出时继续核对snapshot_id。

旧snapshot_id/file_id属于旧scope，删除后新请求410；仅按key找不到有效范围返回404。不要把所有错误都改成“检查冻结”。

### 5.5 幂等、缓存及历史

release请求指纹增加scope_id；重放先按原request_id查记录并检查原scope生命周期，旧scope已删返回410，不在新scope再次执行。不能通过换key归属给旧请求补一个新scope。

缓存键增加身份或统一版本前缀，隔离同key两代；服务端作写依据不查缓存。删除后不能先取缓存旧配置再省略父级校验。历史接口默认当前scope，旧审计保留供备份和排障，不新增回收站/历史范围UI。

## 6. 迁移与备份兼容（必须实施）

先为所有已有行分配稳定身份并回填关联，再创建新唯一约束，最后移除阻止复用的旧key索引。全部完成前不开放HTTP。重启幂等，不更改已发布payload/摘要。旧软删除父级与其后代按原归属归档；发现无法唯一判定的孤儿/冲突拒绝自动猜测，输出不含敏感内容的诊断。

别名约束初始化器、默认种子、路径归一化迁移也必须改为按身份判断，不能每次启动重新加回旧索引。旧二进制不维护scope身份，本版不支持新旧写进程混跑。

`biz/backup/schema.go` 是显式列白名单，当前只认识schema1。必须增加身份、active_slot和删除审计表/字段的备份与验证，更新schema_version至2。新备份保留所有scope（包括已删）及文件。

现有schema1备份仍可恢复：先按旧白名单校验读入隔离候选，再运行确定性的身份回填转换成schema2并校验，不能直接按新列SELECT旧库，也不执行包内DDL。schema2不能被旧二进制恢复；错误提示说明需相应程序版本。旧配置edit_revision恢复换新规则、管理员保持、任务/journal边界继续有效。B01/B11备份闭环需回归，不能在页面改完才发现备份无法用。

## 7. 实施组织与测试

研发阅读 `$engineering-quality-preserving-refactor`：`C:/Users/MalfuRisa/.codex/skills/engineering-quality-preserving-refactor/SKILL.md`。范围身份解析、生命周期错误、事务删除各独立职责，复用同包小方法；不在巨型handler里复制多套条件。IDL更新后hz生成，禁止手改pb/router；若本机生成器不可用应说明阻塞，不沿用手写生成代码的偏差。

顺序：模型迁移与身份解析 → 删除/重建事务 → 全部读写入口与缓存/旧请求防护 → 备份兼容 → API合同与回归。两份PRD最终以同一后端身份模型整合，不各自造scope字段。

| 场景 | 预期 |
| --- | --- |
| 环境所有渠道解锁，含生效/未生效/已删版本 | 环境删除200，当前列表/配置/版本/资源新访问均消失或410，文件仍在 |
| 一条有效渠道locked | 整体409，其他数据不动；删除其他未锁渠道可成功 |
| 同key重建 | 新身份、空配置、新freeze/head；可创建新v1.0；旧数据不进入新列表/导出 |
| 删除与锁版/发布/编辑同时发生 | 数据库串行合法结果；不得发生deleted scope新写入或把旧发布指向新范围 |
| 旧删除/旧freeze或activate请求在重建后到达 | 身份冲突或410；即使数字revision相同也不修改新对象 |
| 原file_id/snapshot_id访问 | 删除后410，不能利用直接下载绕过生命周期 |
| 删除default后重启 | 不复活、不唯一索引报错；手动同key新建可用 |
| 不同环境同pipeline_key | 身份独立，删一个不影响另一个 |
| 迁移重启/旧缓存/DB失败 | 迁移幂等、缓存不串代、删除标记/指针/审计失败整体回滚 |
| 备份v1兼容及v2全量恢复 | 老包可升级恢复；新包保留同名多代和删除记录，恢复后仍不串代 |
| 既有配置编辑/冻结/发布/迁移/导出 | 按新身份合同适配后仍通过；未携带身份明确400而非静默覆盖 |

使用独立库和实例验证真实HTTP删除→重建→发布→旧请求重放及默认对象重启。Go模型/DAO/service/handler/router、备份专项及现有相关套件回归；MySQL/PG真实库未运行则注明。不要在18080/18081用户业务实例删环境验证。

## 8. 实施与验收结果（2026-10-26）

状态：后端第一版已实现；前端身份字段透传不在本轮。

### 8.1 落地内容

| 职责 | 落点 |
| --- | --- |
| 数据模型 | `Environment.environment_id/active_slot`、`Pipeline.scope_id/environment_id/active_slot`、配置/资源/冻结/发布 `scope_id`、`scope_lifecycle_event` |
| 启动迁移 | `biz/dal/db/scope_migration.go`：身份回填、旧索引移除、身份唯一索引、别名索引改按 scope |
| 身份解析/错误 | `biz/service/scope_identity.go`、`scope_resolver.go`、`scope_errors.go`（SCOPE_* 映射） |
| 删除事务 | `biz/service/scope_delete.go`：只检查有效渠道冻结、配置/资源/版本逻辑删除、清指针、写范围事件、保留历史 |
| 默认种子 | `EnsureSystemDefaults` 只在从未初始化库创建；删除 default 后重启不复活 |
| 读写入口 | 环境/渠道/配置/资源/冻结/发布 Handler 与服务入口携带并校验身份；缓存键按 scope 隔离 |
| 同名重建 | 新行新 UUID；旧编辑/旧 freeze/旧 activate request_id 按原 scope 生命周期返回 410/409 |
| 备份兼容 | `biz/backup` schema_version=2，白名单加入身份列与范围事件；schema 1 包在隔离候选库升级后恢复 |
| IDL/文档 | 各 proto 增加身份字段；pb.go 用 hz 在独立生成工程重生成；新 API 文档见 `docs/docs/api/scope-identity.md` |

### 8.2 验收记录

```text
go test ./...     通过
go vet ./...      无输出
```

| 场景 | 结果 |
| --- | --- |
| 迁移幂等/旧索引替换 | 单测用旧表/旧唯一索引+旧数据执行迁移，验证身份回填、旧索引移除、新唯一索引创建、重复迁移不改变身份 |
| 真实旧库升级 | 用 c9aefe5 旧二进制创建 SQLite 实例与旧配置，再用本版本启动；旧配置、环境/渠道获得身份后可正常读取 |
| 删除含版本渠道 | 服务测试+真实 HTTP：unlocked 渠道即使有生效/历史版本也可删除；返回受影响数量 |
| 锁版阻断 | 服务测试：任一有效渠道 locked 时环境删除 409，其他数据不变；解锁后删除成功 |
| 同名重建隔离 | 服务测试+真实 HTTP：新 scope_id、空配置/版本、旧配置详情和旧资源均 410；旧 expected_scope_id 写入 410，新身份写入成功 |
| 默认对象不复活 | 服务测试：删除 default 后再次执行 EnsureSystemDefaults 不重建；显式同名创建成功 |
| 旧请求防护 | 旧身份写配置返回 410 SCOPE_DELETED；release request_id 重放检查原 scope 生命周期 |
| 备份 v2 | 备份单测全量通过；包内 manifest schema_version=2、包含 scope 表/身份列 |
| 既有回归 | `biz/...`、`pkg/...` 全部测试通过；runtime 旧入口、发布、迁移、导入导出测试通过 |

真实 HTTP 验收使用独立 SQLite 实例：创建环境/渠道/配置/资源→冻结→创建并激活 v1→编辑→创建 v2→导出三种来源→导入新范围→删除渠道→同名重建→旧配置 410→新代写入成功。

### 8.3 未完成/限制

- MySQL/PostgreSQL 只实现迁移分支，未在真实库运行；SQLite 已实测。
- schema 1 备份的“旧包→v2 隔离升级”代码路径已实现并有迁移基础测试，但未用一份真实旧版备份包做完整 HTTP 恢复演练。
- 崩溃/断电阶段注入、跨卷 rename、并发删除与备份同时进行的极短竞态未做故障注入。
- IDL 已更新并用 hz 生成新 pb.go；路由文件仍按现有仓库结构维护，新增 `/transfer/export-preview` 为手工注册。
- 文件物理清理、单 scope 回收站、跨环境批量删除不在本轮。
- 未执行 `go test -race`（本机默认 `CGO_ENABLED=0`）；并发语义由现有写协调/冻结测试覆盖。
