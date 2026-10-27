# 管理端整机业务备份与恢复：后端 PRD

日期：2026-10-25。状态：设计稿，未实施。

## 1. 目标与设计决定

管理员可以生成并下载一份完整的虹桥业务备份；发生误操作或需要迁移实例时，可以上传备份、查看校验结果，明确确认后恢复。

备份同时包含数据库里的业务数据、原始资源、版本快照资源，保证来自同一个业务时点。不能用现有 transfer 导出包或版本静态 ZIP 冒充完整备份。

第一版流程：

```text
备份：创建任务 → 暂停业务写入并等在途写完成 → 复制一致数据 → 恢复写入 → 打包校验 → 下载
恢复：上传文件 → 隔离校验与预览 → 确认准备恢复 → 暂停业务写入 → 准备候选数据
      → 等待部署方重启 → 启动时整体切换 → 校验通过才开放访问
```

**恢复不是在线热替换 SQLite 文件。** 管理接口把数据准备好后返回“等待重启”，实际重启由现有启动方式、运维或容器编排执行。后端不通过 HTTP 接口执行 shell、杀进程、调用 Docker/kubectl，也不承诺点击确认后立即恢复完成。前端后续必须展示这一阶段。

选择这个边界，是因为数据库和资源可能被当前进程持有；恢复还会影响当前管理员会话。先关闭旧进程、再在启动开放端口之前切换，可以减少 Windows 文件占用和新旧数据混读问题。

## 2. 工作范围与当前事实

仓库：`D:/kylin/rainbow_bridge`。本轮核对 `main_v1` 提交 `c9aefe5`，工作区干净。实施前再次确认实际基线，建议独立分支 `codex/backup-restore` 和独立工作树；该分支尚未创建。

已核实：

- 默认数据库是本地 SQLite，可选 MySQL/PostgreSQL。
- 原始文件主要在 `<cwd>/data/uploads`；快照默认在 `<cwd>/data/release-snapshots`，也可被 `RAINBOW_RELEASE_DIR` 指向其他目录。
- 发布涉及版本、清单、生效指针和操作事件；锁版有独立状态及事件。
- 普通编辑现在已有 edit_revision，恢复时必须避免旧编辑页重新获得有效基线。
- withWriteGate 已协调业务写入，但不是全实例备份维护状态；认证会话另有数据库写入。
- `script/release-recovery.ps1` 是停实例后复制恢复的专项测试，不是用户可用的管理 API。
- 启动器的 DataDir 还放有程序、配置、日志和前端产物；不能整份打包，也不能把整个 DataDir 当业务目录替换。

### 第一版支持边界

支持 **单进程、SQLite 文件库、本地资源、Redis 关闭**。三个业务存储都必须位于 `<cwd>/data` 这个可整体替换的业务根内、同一本地文件系统中。数据库的根内相对路径可变化，快照自定义目录只有解析后仍位于业务根内才支持。

不支持：MySQL/PostgreSQL、MinIO、多个副本/多个写进程、Redis 开启、网络共享盘、数据库/上传/快照跨业务根或跨卷、内存 SQLite。返回能力不可用说明，不退化成只备份部分数据。该限制适合当前小团队单实例，并非声称其他部署模式的数据不值得备份。

通过 capabilities 明确告知不支持原因；不偷偷修改用户原部署来满足条件。业务根、控制目录配置如有冲突，应拒绝启用此功能。

仅实现后端与命令行恢复工具、测试和 API 文档；**不修改 Vue 管理页面**。未来前端直接复用本 PRD 的接口和状态。定时备份、云端上传、自动保留策略、增量备份、加密包、多种数据库适配、无需重启恢复不在本轮。

## 3. 数据范围、账号和标识规则

### 3.1 必须备份的业务表

以本版模型明确建立允许列表：环境、渠道、`resource_config`、`asset`、`config_freeze_state`、`config_freeze_event`、`config_release_version`、`config_release_asset`、`config_release_head`、`config_release_event`。实施时核对环境/渠道的真实 TableName，不从字符串猜测。

保留主键、业务标识、创建/修改时间、内容原文、锁版状态/revision、全部历史、发布请求指纹、逻辑删除标记及版本号占位。包括可恢复业务历史所需的软删除行；不能用 GORM 默认过滤漏掉。

备份原始资源元数据对应的本地文件，以及全部已提交快照（包含逻辑删除版本）的清单文件。未被配置引用但仍有 asset 元数据的资源也备份。未提交 `.staging`、没有数据库记录的孤儿文件不属于备份，预览中汇总被忽略项，不自动清理源目录。

任一应有文件缺失、长度不符、快照摘要不符，整个备份失败，不交付“可下载但其实不完整”的包。未受管外部 URL 只保留引用并显示“不含外部资源”的警告，不抓取外站内容。

### 3.2 不进入下载备份的内容

- `auth_users`、`auth_sessions`、密码哈希、Cookie、CSRF、环境变量、config.yaml、MinIO/数据库凭据。
- Redis 内容、写协调的瞬态值、备份控制记录、旧回退副本、日志、二进制、Vue 产物、依赖缓存。

这是“业务数据备份”，不是操作系统/部署备份。配置里的业务内容可能本身包含敏感值，因此备份仍只允许管理员下载；不因为排除了认证表就称备份无敏感数据。

### 3.3 恢复后的账号与编辑标识

- 覆盖已有实例时，保留**目标实例当前的管理员账号和密码**，不让备份回滚管理员密码；所有旧会话撤销。账号信息只在恢复启动阶段由目标旧库读入候选库，不出现在下载包中。
- 恢复到全新实例时，由部署方通过既有首次密码环境变量建号；缺少首次凭据则不能开放服务，不提供固定后门密码。
- 恢复后所有现存配置生成新的 edit_revision，原业务字段和时间保留；已发布 payload 和摘要不改。这避免恢复到较早标识后，旧页面/外部脚本持有的标识又被接受。
- freeze/head revision 保留备份值，会话全撤销且恢复维护状态禁止在途管理写；恢复后客户端必须重新登录、重读状态。
- 发布 request_id 历史保留；备份/恢复任务自己的 request_id 记录在业务根外，不能被恢复回滚。两套语义不混用。

## 4. 启用配置与目录

新增 backup 配置，默认关闭：

```yaml
backup:
  enabled: true
  control_dir: "D:/RainbowBridge-control/local-demo"
```

control_dir 必须绝对路径、不能位于业务根内或其祖先目录，不能包含/覆盖配置和程序目录。它与 `<cwd>/data` 必须处于同一本地卷，便于恢复候选目录与业务根通过 rename 切换。

服务端在控制目录下自行生成如下结构，HTTP 不接受任意绝对路径：

```text
control_dir/
  identity.json             # 当前目标实例ID，不能从上传文件覆盖
  jobs/                     # 持久任务记录与request_id索引
  backups/                  # 已完成备份ZIP
  imports/                  # 上传的备份包和验证结果
  staging/                  # 备份/恢复候选临时目录
  rollback/                 # 恢复前业务根，只供本机操作员回退
  restore-journal.json      # 当前未完成恢复的切换记录
  restore-journal.last.json # 已成功启动后归档的最近一次切换记录
  audit.jsonl               # 操作者、对象、结果，不含凭据
```

默认固定限额：备份/上传包 2 GiB；解压后数据库和文件合计 10 GiB；最多 50,000 个普通文件；单文件 2 GiB；manifest 最大 16 MiB；创建/校验/准备各最多 10 分钟，维护等待在途业务写最多 30 秒。达上限允许，超过拒绝；分别测试等值和+1。第一版可通过内部测试注入缩小限额，不开放任意无限制开关。

每次估算本机额外空间：私有快照/重建库、复制资源、最终ZIP、候选库；恢复还要保留原业务根。可用空间须覆盖本次预计峰值并预留至少 256 MiB，否则开始前拒绝。估算后仍可能遇到磁盘满，需在所有写入/Sync/Close/rename 处处理真实错误。

同一实例同时只运行一个创建备份、校验或准备恢复任务，不排无限队列；并行下载可允许最多2个。第一版不自动删除旧备份；控制目录最多保留10个完成备份，达到后提示先下载并删除旧备份。任务历史保留最近100个终态及所有非终态/request_id记录，达到控制存储限制时明确拒绝新增，不丢弃仍需要重放的记录。

## 5. 一致性备份算法

### 5.1 进程和维护保护

为支持本功能的本地 SQLite 实例，在任何开库/迁移前取得按规范化业务根确定的操作系统文件锁，应用与离线工具共用；锁文件位于业务根外，不随 rename 失效。第二个同版进程必须启动失败。不能以 PID 文本或仅进程内 mutex 声称已阻止跨进程并发。

该模式不支持旧二进制并行运行、外部工具直接改库/文件。文档明确需替换旧进程；测试覆盖应用与CLI竞争。不要把能力扩展为任意部署的通用分布式锁。

业务写入入口增加维护协调，与 withWriteGate 配合：先登记在途写，再进数据库写协调；开始备份后不再接受新业务写，等已有写结束，才复制。配置写、资源上传、导入迁移、环境渠道变更、锁版、创建/切换/删除版本、路径改写都在此范围。Service 层必须再次保护，不能只靠按钮或 HTTP 中间件。

维护时新业务写返回 503 `BACKUP_MAINTENANCE`、`Retry-After: 5`。已有操作允许正常提交或按原期限失败；超出30秒仍未排空，本次备份失败并恢复写入，不强杀业务操作。排空过程不得拿着数据库唯一连接等待别的操作释放连接。

公开读取继续可用，SQLite 获取连接和复制会有延迟，不承诺备份完全无影响。auth 会话读写可继续：认证数据不在业务备份范围，SQLite 快照本身仍保证数据库一致。不要将 VACUUM INTO 放进现有 withWriteGate 的事务，SQLite 不支持这种用法。

### 5.2 生成私有数据副本

1. 排空业务写后记 `consistent_at`；保持维护保护直至数据库与所需文件全部复制到私有目录。
2. 用 SQLite `VACUUM INTO` 生成独立数据库快照，不直接复制运行中主库/WAL/SHM。目标文件由服务端产生且尚不存在，参数正确绑定，禁止把HTTP字符串拼入SQL。
3. 快照可能含认证表，严格限制本地权限，**不得作为下载内容**。从快照按已知列允许列表、分批读行，在服务端创建的干净业务数据库中写入 §3.1 的表和约束，保持整数/内容字符串原样。最终包内数据库从未写入认证数据；不采用“删掉auth表但残留页仍含凭据”的做法。
4. 以业务数据库元数据确定所需文件，计算长度与 SHA-256并流式复制。原始文件不能按客户端给的Path任意读取，必须满足本地资源根规则；快照资源路径由snapshot_id/asset_id推导。
5. 对私有库执行 integrity_check、关系与快照检查；文件全部验证完成后释放维护保护。之后只对私有副本打包，不继续读取在线业务文件。
6. ZIP写入 `.part` 文件，完整关闭并同步后再原子发布；记录ZIP SHA-256和大小后任务进入 succeeded，之前不可下载。失败清除本任务临时生成物、释放维护状态，在线业务数据不变。

成功响应或任务接受不能冒充备份已完成。请求断开后已接受的任务继续运行，避免浏览器关闭时留下永久维护状态；每个后台任务自身有期限，服务关闭会取消并记录。

SQLite依据：[VACUUM INTO生成一致快照](https://www.sqlite.org/lang_vacuum.html)、[备份机制说明](https://www.sqlite.org/backup.html)。这只覆盖SQLite部分的一致性；数据库和资源属于同一业务时点仍依赖上述业务写入排空。

## 6. 备份包格式与验证

格式标识 `rainbow-business-backup-v1`，ZIP固定逻辑布局：

```text
manifest.json
database/business.sqlite
uploads/<file_id>/<file_name>
release-snapshots/<snapshot_id>/assets/<asset_id>.bin
```

ZIP内目录是逻辑名称，不采用服务器绝对路径。manifest至少包含：format、backup_id、created_at、consistent_at、producer（app_version/git_commit）、schema_version、counts（环境/渠道/配置/资源/版本/删除版本/历史事件数量）、files数组（相对path、size十进制字符串、sha256）、warnings数组。时间RFC3339 UTC；空数组为[]。ZIP总体摘要在任务记录里，避免摘要包含自身的循环。

schema_version 使用本次显式的业务备份版本1，不由应用显示版本猜测。v1只导入具有当前已知表/列/约束的格式；较新/缺列/未知业务架构拒绝，不运行上传文件附带SQL、迁移、触发器或脚本。兼容扩大必须后续显式实现。

上传验证必须：

- 限制HTTP实际读入字节，不能只相信Content-Length；流式落盘，不完整包不能进入有效目录。
- 对每个实际解压字节计数，限制总量/数量/单条，不能只信ZIP头；拒绝加密ZIP、嵌套归档自动解压、链接、设备文件、重复或大小写归一冲突条目、绝对路径、盘符、UNC、`..`、NUL及反斜杠歧义。
- 只允许清单中的文件和固定布局，清单路径同样校验；所有entry与manifest集合一致，长度和sha256一致。
- 数据库用只读方式打开，禁用扩展加载；先检查sqlite_schema不含用户触发器/视图等未知对象，再使用固定模型/列读取，不能信任备份内DDL来创建生产库。
- 校验数据库完整性、主键/别名唯一、环境渠道归属、freeze/head关系、active不能指向缺失/已删版本、发布payload/manifest摘要、资源引用及实际文件。不能只检查文件存在就称可恢复。
- 管理端显示资源和版本数量、备份时点、创建程序信息、外部URL警告、覆盖范围和会话失效提示。严重问题令valid=false，禁止准备恢复。

SHA-256用于发现损坏，不是来源认证或签名。第一版只允许管理员导入可信备份，不增加公钥/签名/加密平台。

## 7. 恢复准备、重启切换与失败回退

### 7.1 在线准备

1. 从有效预览发起 execute，必须提交对应 preview_id、package_sha256、request_id和固定确认串 `RESTORE_ALL_BUSINESS_DATA`；本次是整实例覆盖，不合并、不中途按环境挑选。
2. 后端绑定验证过的服务端文件ID，核对文件摘要未变；HTTP不能传入输出绝对路径或任意数据库文件。
3. 进入维护并排空在途业务写，重新检查目标能力和包兼容性。允许先在隔离目录构造候选，只有最终准备期间才维持写禁用。
4. 用服务端模型建立候选业务根；复制校验后的文件，数据库写入备份业务数据，重新生成配置edit_revision；保留发布内容、资源ID、锁版、active和历史。候选DB不拷贝上传DDL，不回放SQL脚本。
5. 此时不替换在线库，也不在候选里提前固定目标管理员密码；目标账号在实际启动切换时再读取，避免准备后改密导致回滚密码。
6. 持久保存恢复计划/候选摘要及审计，然后任务进入 `restart_required`。从此新业务写继续503，公开读取仍为原数据；明确表示尚未生效。前端可显示重启说明；不自动发命令或承诺持续轮询不会中断。
7. 等待重启没有“10分钟后自动恢复写入”的超时；管理员可调用 cancel 放弃尚未开始切换的计划，解除维护。重启前不能允许悄悄新增的业务写在恢复中丢失。

### 7.2 启动切换

必须在 `database.Open`、AutoMigrate、默认数据/路径迁移、初始化会话与注册HTTP之前检测恢复计划。进程运行目录/控制目录必须确定，配置解析不能已经创建或修改业务数据库。

流程：取得实例文件锁 → 检查journal → 校验候选和空间 → 只读读取目标旧账号 → 在候选库建立目标账号且空会话 → 确认所有候选连接关闭 → 记录prepared → 原业务根rename到rollback → 记录old_moved → 候选rename到业务根 → 记录new_installed → 校验新业务库与资源 → 常规初始化（仍不开放HTTP） → 记录committed及任务succeeded → 开放HTTP。

要求：

- 原业务根、候选、rollback都在同卷，禁止跨卷复制代替rename后仍声称是原子切换。
- 两次rename不是一个原子事务；每步之前和之后的状态均需持久记录，journal采用临时文件、Sync、原子替换，必要的目录同步按平台实现。崩溃恢复依据实际路径、候选ID和文件摘要判定，不仅凭stage字符串。
- 操作系统断电耐久性能力按Windows/Linux实际支持记录，不声称任何硬件故障下都绝对不丢数据。无法确定新旧根身份时拒绝启动、保留所有副本，不猜测删除或自动创建空库。
- 失败发生在commit前，尽量恢复旧业务根，任务标failed/rolled_back；回退也失败则保持端口关闭，提供本机恢复工具说明。普通读取看不到混合数据。
- 新根安装后的校验、账号建立/常规初始化必须都通过才能记录committed和任务成功；如果常规初始化失败，应先关闭本轮已打开的数据库/文件句柄再回退。若committed已落盘后在真正监听前崩溃，重启校验当前根后继续常规启动，不重复覆盖，不重新回填一套编辑标识。journal和任务元数据如只完成一半，以已确认的根和journal完成对账后才返回成功。
- rollback保留一份本次切换前完整数据，不自动删除。存在未处理回退副本时拒绝下一次恢复，避免覆盖最后回退点；用户可在确认新数据可用后通过本机工具明确清理，不提供不经确认的远程递归删除。

恢复成功后会话全部失效，需重新登录；原始配置/资源/快照及生效版本按备份时点恢复。管理备份任务和恢复审计位于控制目录，不随旧业务库回滚消失。

### 7.3 服务无法启动时的离线工具

提供Go命令 `cmd/backup-restore`，复用同一包校验、候选构建和journal逻辑。至少支持 validate、stage、status、cancel及本机rollback；接收操作员显式给出的配置/包路径，拒绝对正在运行的目标写入。离线使用同一实例文件锁，不信任端口空闲就认定实例未运行。

工具不得绕过包校验；rollback也必须先校验副本、关闭所有数据库连接再操作。显式执行一次已完成恢复的rollback时，同样保持目标当前管理员账号、清空会话、更换编辑标识并留审计，不能把旧会话一并复活；切换失败且从未开放流量的内部自动回退可直接恢复原根。对新实例缺账号时使用已有环境初始化方式。不要依赖PowerShell专属API实现备份核心；Windows启动器只需保证重启后使用同一DataDir、相同控制配置，不能调用演示密码重置改变恢复账号语义。

## 8. 管理 API 合同

统一在部署BasePath下的 `/api/v1`。IDL新增 backup.proto，模型与路由经hz生成。所有接口包括列表/状态/下载均要求现有管理员会话，POST要求CSRF和既有来源规则；绝不能加入公开runtime白名单。所有JSON和下载均 `Cache-Control: no-store`。

新模块成功信封使用code=200（异步接受code=202）、msg="OK"、data；错误含code/msg/error_type。终态任务失败通过HTTP200的status数据表达，不把轮询失败任务误写为HTTP调用失败。

| 方法和路径 | 输入 | 输出/语义 |
| --- | --- | --- |
| GET `/backup/capabilities` | 无 | supported、reasons[]、limits、restore_requires_restart=true、mode=business_full；不返回服务器绝对路径 |
| POST `/backup/create` | JSON request_id(UUID)、reason(选填≤500 Unicode) | 202，data.job；异步创建备份 |
| GET `/backup/list` | limit默认20/max100，before_id不透明游标 | data.items[]、has_more、next_before_id；只列可下载完成备份及摘要 |
| GET `/backup/status` | job_id | data.job；包括运行中与终态 |
| GET `/backup/download` | backup_id | 成功ZIP、Content-Disposition和X-Rainbow-Backup-Sha256；不使用带令牌的公开URL |
| POST `/backup/delete` | backup_id、expected_sha256、request_id | 200，deleted=true；禁止删除被未完成恢复引用或正在下载的包，文件与目录由服务端定位 |
| POST `/backup/restore/preview` | multipart file、request_id | 上传完成后202，data.job；异步校验，完成后产生preview |
| GET `/backup/restore/preview` | preview_id | valid、package_sha256、backup摘要、warnings[]、issues[]、expires_at、target_instance_id |
| POST `/backup/restore/execute` | preview_id、package_sha256、target_instance_id、request_id、confirmation、reason | 202，data.job；后台准备，状态最终到restart_required，不在HTTP里换库 |
| POST `/backup/restore/cancel` | job_id、request_id | 200，cancelled=true；仅允许尚未prepared切换且job处于restart_required，放弃计划并解除维护 |

以上表内路径均需前置 `/api/v1`。create/delete/execute/cancel JSON严查字段；预览有效期24h，上传文件按job ID命名，已execute的文件不能过期自动删除。过期预览不能execute，需重新上传；过期临时文件只清理无计划引用的本任务目录。

job统一字段：job_id、request_id、kind(create/validate/restore)、state、phase、created_at、updated_at、actor、reason、backup_id/preview_id(不适用时null)、result(未完成null)、error(无错误null)。state为 queued/running/succeeded/failed/interrupted/restart_required/cancelled；phase是人可解释阶段，不报虚假百分比或ETA。时间RFC3339，数组空值[]。

示例接受响应：

```json
{
  "code": 202,
  "msg": "OK",
  "data": {
    "job": {
      "job_id": "b1111111-1111-4111-8111-111111111111",
      "request_id": "c1111111-1111-4111-8111-111111111111",
      "kind": "create",
      "state": "queued",
      "phase": "waiting",
      "created_at": "2026-10-25T02:00:00Z",
      "updated_at": "2026-10-25T02:00:00Z",
      "actor": "admin",
      "reason": "",
      "backup_id": null,
      "preview_id": null,
      "result": null,
      "error": null
    }
  }
}
```

### 8.1 幂等与任务持久化

同一操作kind+request_id固定原payload；重复返回同一个job，不新建第二个任务。同ID不同字段返回409 BACKUP_REQUEST_CONFLICT。上传预览在文件落盘并计算摘要后按内容确认幂等；正在上传的重复ID返回409 BACKUP_BUSY，不读取两份并行大文件。

任务登记持久成功后才能返回202；单worker只认落盘任务。服务重启后，未涉及恢复切换的running任务标interrupted并清理自己的临时文件、解除维护，不伪报succeeded；重复旧ID仍返回原interrupted结果，新尝试用新ID。restart_required以及切换中的journal按§7恢复，不能统一标interrupted丢弃计划。

控制目录元数据使用原子文件记录/单实例串行变更，不将任务登记塞进待备份的SQLite事务造成循环依赖。审计写失败则不开始破坏性切换；状态持久失败期间不能返回已完成。

### 8.2 主要错误

| HTTP | error_type | 含义 |
| --- | --- | --- |
| 400 | BACKUP_INVALID_REQUEST / BACKUP_PACKAGE_INVALID | 请求或包结构/完整性不合法 |
| 401/403 | 沿用AUTH错误 | 未登录、来源/CSRF不合法 |
| 404 | BACKUP_NOT_FOUND / BACKUP_PREVIEW_NOT_FOUND | 对象不存在 |
| 409 | BACKUP_BUSY / BACKUP_REQUEST_CONFLICT / BACKUP_STATE_CONFLICT | 已有任务、重用ID或阶段不允许 |
| 409 | BACKUP_TARGET_MISMATCH / BACKUP_IN_USE | 目标实例/包摘要变化，或备份被引用/下载 |
| 410 | BACKUP_PREVIEW_EXPIRED | 预览过期 |
| 413 | BACKUP_LIMIT_EXCEEDED | 包、条目、总大小或数量超限 |
| 503 | BACKUP_UNSUPPORTED / BACKUP_MAINTENANCE / BACKUP_SPACE_INSUFFICIENT | 能力不支持、维护中、空间不足 |
| 500 | BACKUP_STORAGE_ERROR / BACKUP_INTERNAL_ERROR | 控制记录、磁盘或内部故障 |

capabilities对不支持模式仍HTTP200并supported=false，实际操作503。failed任务的error对象使用相同分类，不回显SQL、绝对路径、凭据或配置正文；完整细节仅写脱敏服务日志并通过job_id关联。

## 9. 研发职责与实施顺序

实施前阅读 `$engineering-quality-preserving-refactor`：`C:/Users/MalfuRisa/.codex/skills/engineering-quality-preserving-refactor/SKILL.md`。

| 职责 | 建议落点 | 约束 |
| --- | --- | --- |
| 备份配置、能力校验 | pkg/config、service backup_capabilities | 解析现有路径，不改现有资源引用语义 |
| 维护与本机锁 | 独立maintenance服务、启动入口、操作系统锁适配 | 先于开库取得锁；保护业务Service入口，避免与数据库写协调互相等待 |
| 一致数据库副本 | pkg/database或同域backup_database | VACUUM INTO及允许表复制，禁止复制热库文件 |
| 清单/流式包/验证 | backup_manifest、backup_archive、backup_validate | 与transfer和发布ZIP区分，不扩大旧入口 |
| 任务/审计/恢复journal | backup_jobs、restore_journal | 状态写入业务根外，不写一个巨型handler |
| 请求编排 | biz/service/backup_*、biz/handler/backup | 细则复用，handler只负责绑定、身份、状态映射 |
| 恢复切换/CLI | 启动前恢复模块、cmd/backup-restore | Windows/Linux同一业务协议，路径处理按平台实现 |
| IDL与文档 | idl/biz/backup.proto、API index/管理专题 | hz生成；保留已确认的main格式 |

执行顺序：

1. 验证当前驱动的 VACUUM INTO 可用、业务根可识别、实例锁和journal同卷切换可行；用独立目录验证，不接演示库。
2. 完成维护排空、一致业务DB副本、文件复制/清单、ZIP；先交付真实备份恢复到隔离目录的内部闭环。
3. 完成上传验证/预览、候选重建、账号保持及标识更新。
4. 完成启动前切换、崩溃状态恢复与CLI，再接HTTP异步任务。
5. 执行验收、补专业API和操作说明。在本PRD登记最终结果，不为每轮修复另写报告。

如文件系统或现有驱动不支持所需行为，应报告实际证据，保持功能不可用，不以“先裸复制库、忽略失败、先重命名试试”降级交付。

## 10. 验收场景

| 编号 | 触发/输入 | 预期 |
| --- | --- | --- |
| B01 完整备份 | 两环境、多配置类型、大整数、原始未引用资源、多版本/删除版本/历史 | 包含全部声明业务数据和文件，恢复后原文/摘要/字节一致，快照与active正确 |
| B02 并发一致性 | 暂停资源上传/版本复制/导入在途，发起备份，再尝试编辑/切版 | 先前写完整结束才快照；后续写503；DB与文件同一业务时点，无半包 |
| B03 维护排空失败 | 在途写超过等待期限、备份复制错误 | 备份failed、不改变在线数据、维护解除；后续正常写可执行 |
| B04 SQLite模式 | 分别使用rollback journal/WAL，有未checkpoint提交 | VACUUM快照均含提交数据；不依赖复制WAL/SHM；不将VACUUM置于事务 |
| B05 凭据隔离 | auth含可辨识密码hash、session、CSRF，运行配置含敏感值 | 下载库无auth数据页/表内容或配置文件；私有含auth快照不可下载且清理；HTTP/日志不泄漏 |
| B06 包损坏 | 缺条目、改字节、错清单、截断、旧格式/更新schema | preview valid=false或任务failed，目标数据未改，execute拒绝 |
| B07 路径/解压 | ../、绝对盘符、UNC、软链接、重名大小写碰撞、ZIP解压量超限 | 实际字节计量并拒绝，无越界读写，无无限内存增长 |
| B08 预览只读 | 正常上传/轮询/过期 | 在线配置、文件、freeze/head/事件不变化；过期410 |
| B09 确认阶段 | 错确认串、错instance、摘要变化、同ID改字段、重复execute | 拒绝错误；真重放同job，只有一份计划，原库不被在线覆盖 |
| B10 等待重启 | 准备完成后读配置、尝试业务写、cancel | 读取仍旧版，写503；重启前cancel不改数据并解除维护 |
| B11 真实重启恢复 | 源备份后另改业务，再execute并重启真实进程 | 端口开放前完成整体恢复；保留目标账号密码、旧Cookie401；新edit_revision拒绝旧页面基线 |
| B12 分阶段崩溃 | prepared、old_moved、new_installed以及校验/常规初始化边界强制退出 | 重启有确定处理；成功或完整回退，不出现空库/混合数据；无法确认时不开放端口 |
| B13 Windows占用 | 候选/原库连接未关、文件被占用、rename失败 | 返回可诊断失败，保留原数据；不降级跨卷拷贝或删除原根 |
| B14 磁盘/权限 | 空间预检不足、运行时写满、Sync/Close/审计写失败、回退失败 | 不显示成功；无半成品可下载；回退失败拒绝启动且副本保留 |
| B15 生命周期 | 同ID重放、进程重启、running中断、控制文件截断 | 不重复执行；正常任务interrupted，恢复计划按journal续作；损坏不猜测写入 |
| B16 身份/下载 | 无Cookie、旧Bearer、缺CSRF、下载中删除、跨ID请求 | 既有401/403；有效会话可操作；下载/计划引用期间不能删除；文件均非静态公开路径 |
| B17 支持边界 | PG/MySQL、MinIO、Redis开启、外部快照根、第二进程 | 能力明确不可用或启动排他失败，不生成“部分全量备份” |
| B18 CLI灾难恢复 | 服务不能启动、独立新目录、旧数据回退、锁被占用 | CLI复用校验与journal，恢复可启动；不与运行实例同时写 |
| B19 限额 | 包/单条/条目数/解压总量各等值与+1、并发创建 | 等值可用，超限413或task错误；只一重任务，不排无限队列 |
| B20 非回归 | 普通编辑冲突、锁版、发布切换删除、旧runtime与静态包 | 原合同保持；恢复成功可用真实HTTP访问资源/ZIP，缺文件仍明确503 |

必须在临时实例真实执行“备份→改数据→预览→准备→重启→重新登录→逐项读取核对”，不是只比较ZIP文件存在。至少Windows完成真实进程验收，Linux完成文件锁/rename/启动恢复验证后才标记跨平台支持；未跑的环境如实登记。

测试建议：受影响Go包及全量编译/测试、vet，新增真实HTTP专项和可控崩溃脚本，复用发布恢复断言；脚本使用独立库/目录/端口和临时凭据，不修改18080/18081业务实例。尚无前端页面，不把API验收称为管理页已交付。

## 11. 前端后续接入约定

页面只需要能力提示、备份列表/创建/下载、上传预览、确认恢复、任务状态。恢复前明确显示：“覆盖全部业务数据；保留当前管理员账号；恢复后重新登录；需要重启服务”。不要求每步输入管理员密码，不复用锁版原因必填设计。

轮询状态直到succeeded/failed/interrupted；restart_required显示重启说明和取消入口。网络断开不自动重复execute；同request_id查询/确认同一任务。重启后的401回登录，再查任务结果；只有succeeded才展示“恢复完成”。

第一版没有一键重启后端接口，这是明确边界，不以页面一直转圈掩盖。定期备份和离机保存先写操作建议，不自动创建计划任务。

## 12. 实施与验收结果（2026-10-25）

状态：后端第一版已实现，Windows 本机完成真实进程闭环，未修改 Vue 管理页面。

### 12.1 落地内容

| 职责 | 落点 |
| --- | --- |
| 配置、能力与限额 | `pkg/config.BackupConfig`、`biz/backup/capabilities.go`、`limits.go`、`paths.go` |
| 实例文件锁、目录/标识 | `biz/backup/boot.go`、`lock_windows.go`、`lock_unix.go`、`identity.go` |
| 一致数据库副本 | `biz/backup/create.go`（`VACUUM INTO`）、`schema.go`（业务表白名单复制） |
| 文件清单、manifest、ZIP | `biz/backup/files.go`、`manifest.go`、`archive.go`、`validate.go` |
| 维护排空 | `biz/backup/maintenance.go`、`biz/service/freeze_guard.go`、`biz/middleware/maintenance.go` |
| 任务、幂等、审计 | `biz/backup/jobs.go`、`audit.go`、`preview.go`、`backups.go` |
| 恢复候选与启动切换 | `biz/backup/restore.go`、`journal.go`、`switch.go`、`auth_preserve.go` |
| 启动入口 | `main.go`、`initialize.go`（开库前取锁与切换） |
| 管理 API | `biz/handler/backup`、`biz/router/backup`、`biz/model/backup` |
| IDL | `idl/biz/backup.proto`；本机 hz v0.9.7 未产出代码，模型/路由按现有生成文件形态手写，未整库重生成，避免覆盖既有生成文件 |
| 离线工具 | `cmd/backup-restore`（validate/stage/status/cancel/rollback/cleanup） |
| 测试 | `biz/backup/*_test.go`、`biz/service/backup_maintenance_test.go` |
| 文档 | `docs/docs/api/backup.md`、API 侧边栏条目、`config.dev.yaml` 注释示例 |
| 范围身份兼容（2026-10-26 追加） | 备份 schema_version 升为 2，白名单加入 environment_id/scope_id/active_slot 与 `scope_lifecycle_event`；schema 1 旧包在隔离候选库中按旧白名单校验后执行身份回填并升级，再按 v2 校验/恢复 |

实现中额外修正了两处真实问题：

- 受理响应与最终结果的 `backup_id` 曾不一致；已改为创建时分配、全程稳定。
- Hertz 默认预解析 multipart 会让流式上传读到空 body；已启用 `WithStreamBody` + `WithDisablePreParseMultipartForm`，普通资源上传仍通过 `FormFile` 工作。
- 任务刚变为终态时 worker 仍在收尾，紧接着创建下一任务可能误报 busy；`acquireWorker` 现在短暂等待该收尾完成，不排队第二个任务。
- 若 `committed` journal 永久保留，未来版本新增业务表时每次启动都会被旧 journal 当成未知架构拒绝；初始化成功后现在把 journal 归档为 `restore-journal.last.json`，CLI 仍可读取历史记录执行回退。

### 12.2 验收记录

自动化命令（Windows，Go 1.27.1）：

```text
go test ./biz/backup -v        通过
go test ./biz/service -run TestServiceRejectsBusinessWritesDuringBackupMaintenance 通过
go test ./biz/... ./pkg/...    通过
go vet ./biz/backup ./biz/handler/backup ./biz/middleware ./cmd/backup-restore 通过
```

| 编号 | 结果 | 证据 |
| --- | --- | --- |
| B01 完整备份 | 通过 | 单测覆盖两环境/两渠道、原文含 64 位大整数、未引用资源、生效版本、已删除版本与历史；恢复后字节/摘要一致 |
| B02 并发一致性 | 通过 | 维护门闩单测验证在途写排空、新写 `BACKUP_MAINTENANCE`；真实进程准备后配置写 HTTP 503 |
| B03 排空失败 | 通过 | 排空超时单测：返回 503、清除 active、后续写恢复 |
| B04 SQLite 模式 | 通过 | `TestBackupIncludesCommittedWALData` 在 WAL + 关闭自动 checkpoint 下备份，包内数据库含已提交行 |
| B05 凭据隔离 | 通过 | 包内数据库查询确认无 `auth_users`/`auth_sessions`；私有快照任务结束删除；真实进程下载包不含认证表 |
| B06 包损坏 | 通过 | 篡改数据库条目后预览产生 issues，不执行候选构建 |
| B07 路径/解压 | 通过 | ZIP 单测拒绝 `../`、绝对路径、盘符、反斜杠、软链接、大小写重名 |
| B08 预览只读 | 通过 | 预览/准备期间真实进程读取仍为修改后旧数据；预览不改在线库 |
| B09 确认阶段 | 通过 | 单测拒绝错误确认串（400）、错误目标实例（409）、错误摘要（409）；真实进程正确确认成功 |
| B10 等待重启 | 通过 | 取消单测：任务 `cancelled`、维护解除、候选/计划清理、在线数据与版本不变；真实进程写 503、读旧值 |
| B11 真实重启恢复 | 通过 | Windows 18099 临时实例：登录→建环境/配置/资源→备份→改配置→上传预览→execute→重启；旧 Cookie 401，原密码登录成功，内容恢复为备份值，edit_revision 已更换，资源 HTTP 200；启动成功后 `restore-journal.json` 归档为 `restore-journal.last.json` |
| B12 分阶段崩溃 | 未做故障注入 | 已实现 prepared/switching/old_moved/new_installed/committed 状态机与 marker 校验；本轮未强制杀进程逐阶段验证 |
| B13 Windows 占用 | 部分 | 真实进程 rename 成功；未构造候选/旧库句柄未关闭的强制失败场景 |
| B14 磁盘/权限 | 部分 | 实现空间预检、写入/Sync/Close/rename 错误处理；未做磁盘写满注入 |
| B15 生命周期 | 部分 | 验证同 request_id 重放、服务重启后 committed 对账；未注入运行中任务被杀的阶段 |
| B16 身份/下载 | 通过 | 真实进程旧 Cookie 401、被恢复引用包删除 409、第二进程启动失败；下载头含 SHA-256 且 `no-store` |
| B17 支持边界 | 通过 | 能力单测：PostgreSQL/MinIO/Redis 返回不支持原因；静态配置默认部署即此状态 |
| B18 CLI 灾难恢复 | 通过 | 停服后 CLI `status`/`validate`/`rollback` 成功；rollback 后重启恢复为回退前数据，管理员密码保留、旧会话 401；另有显式 `cleanup -confirm DELETE_ROLLBACK_COPY` 清理回退副本 |
| B19 限额 | 部分 | 默认限额、文件数/单文件/总量检查已实现；未逐项测试“等值/+1”边界 |
| B20 非回归 | 通过 | 既有 `biz/...`、`pkg/...` 测试通过；真实恢复后资产下载 200；普通编辑冲突/锁版/发布测试仍在原套件中通过 |

真实进程验收使用独立目录 `output/e2e-backup`、端口 `127.0.0.1:18099`、SQLite、`storage.type=local`、Redis 关闭，未修改 18080/18081 业务实例。CLI 回退后再次核对：内容为回退前 `changed-after-backup`、密码仍为原密码、全部会话失效、`edit_revision` 再次更换、控制目录保留两份 rollback 副本。

### 12.3 当前限制与未验证项

- 仓库自带 `config.yaml` 是 PostgreSQL + MinIO + Redis 开启，能力接口会返回 `supported=false`；要启用需切换为 SQLite + 本地存储 + Redis 关闭的单实例部署。
- 真实进程只在 Windows 验证；Linux `GOOS=linux CGO_ENABLED=0 go build ./...` 通过，但文件锁、rename 与启动恢复未在 Linux 运行，不能声称跨平台验收完成。
- B12/B13/B14/B15 的故障注入与 B19 的全部“等值/+1”边界未执行，代码路径已实现但未据此宣称通过。
- 未执行 `go test -race`：本机默认 `CGO_ENABLED=0`，Go 拒绝 race 构建；并发语义由维护门闩单测和任务状态测试覆盖，不能声称已通过竞态检测器。
- 本机 `hz` 未能从 `idl/biz/backup.proto` 产出文件；新模块的 model/handler/router 为手写并按现有生成文件形态放置，后续在可用 hz 环境重新生成时应核对 JSON 字段与路由。
- 未新增 Vue 页面、定时备份、云端上传、增量/加密包或多数据库适配。

## 13. 前端接入与验收（2026-10-25）

本节记录后续授权的前端实施，前文第 12 节中的“未新增 Vue 页面”仅指后端交付时点。

### 13.1 实施范围

- 新增 `/backups` 管理页及“备份恢复”导航，沿用光谱 UI、原版标志、背景、深浅主题与现有弹窗；桌面 1280/1440 像素完成截图检查。
- 支持能力检测、备份创建、阶段查询、列表分页、ZIP 下载和删除；不支持的部署展示具体原因并禁用操作。
- 恢复流程为上传校验、数量与警告预览、覆盖风险确认、准备恢复、等待部署人员重启、重新登录后查询真实结果。可以取消待恢复计划；不会通过页面重启服务或把受理成功当作恢复完成。
- 使用已有 Cookie/CSRF、登录跳转和 BasePath。维护响应保留输入；网络结果不确定时保存原请求编号及参数，由用户显式确认同一请求，不自动重复发起恢复。仅保存任务编号与请求意图，不保存凭据或文件字节。
- API 适配、任务编排、持久化、展示文案、列表、预览和任务卡片分别放置；没有修改后端协议、部署实例或现有业务页面逻辑。

### 13.2 验收结果

| 检查 | 结果与证据 |
| --- | --- |
| 构建 | `cd vue; npm run build` 通过，包含 TypeScript 检查 |
| Mock 回归 | 原有与新增前端套件共 73 项通过，其中备份专项 16 项；覆盖 202、200 失败任务、409/503、过期/无效预览、下载异常、同请求确认、401 重登、卸载停止轮询、深浅主题和桌面布局 |
| BasePath | `/rainbow-bridge/` 完整回归通过；根路径 `/` 的不支持部署、上传校验与原请求确认 3 项补充检查通过 |
| 下载兼容 | 真实后端返回 `application/x-zip-compressed`，前端接收合法二进制 ZIP，拒绝 JSON 错误响应和非 ZIP 内容；会话变化时丢弃旧响应 |
| 真实 UI 闭环 | Windows 独立 SQLite + 本地资源 + Redis 关闭实例，端口 18097：页面创建备份、下载、改数据、上传校验、准备、取消、再次准备、进程重启、原账号重登、查询 `succeeded`，全部通过 |
| 数据核对 | 配置原文及大整数恢复；`edit_revision` 更换；原资源 HTTP 200 且内容一致；重启前读旧数据、写入 503 `BACKUP_MAINTENANCE`；旧会话重启后 401 |

真实闭环复现：`./script/test-backup-ui.ps1`（已有本轮构建可加 `-SkipBuild`）。脚本仅创建隔离测试目录并启动/停止自身记录的测试进程，结束恢复环境变量，不操作 18080/18081 实例。测试凭据随机生成，不写入验收证据。

本轮真实证据位于 `output/backup-ui-20261025/instance-4b49825f/frontend-evidence.json`；桌面截图位于本地可视化目录的 `backup-restore/backup-preview-{1280,1440}-{light,dark}.png`。未把 Mock 验收当作真实数据恢复验收；Linux 运行及故障注入的未验证项仍以第 12.3 节为准。
