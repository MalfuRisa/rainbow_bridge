# 后端任务二：明确导出来源，支持选择生效或指定发布版本

日期：2026-10-25。状态：待实施。依赖：[任务一：范围删除及同名重建](scope-delete-recreate-backend-v1.md) 的scope身份规则；可以先开发归档转换，但最终验收必须整合身份模型。

## 1. 要解决什么问题

当前“导入导出”只导出编辑区现有配置，没有生效/指定版本选择，包里也没有足够直观的来源说明。用户无法判断导出的是草稿还是应用正在使用的配置。

本轮提供三种明确来源：editor（编辑区当前内容）、active（选中时的生效快照）、version（指定发布版本）。三种来源均生成**可重新导入编辑区的迁移归档**；不把已有runtime静态资源包换名字冒充可导入归档。

导入结果仍进入编辑区，不自动锁版、创建发布版本、切换active或恢复历史。整实例备份恢复、业务静态ZIP都保持独立用途。

## 2. 工作边界与现状

仓库 `D:/kylin/rainbow_bridge`，已提交基线 `main_v1/c9aefe5`，备份和前端接入工作未提交，不覆盖其他窗口文件。建议独立分支 `codex/transfer-source-export`（待创建），与任务一用提交进行整合，不在共享目录切分支或全仓add。

重点入口：`ExportConfigsSelective`、`GetExportTree`、`config_archive.go`、`archive_export.go`、`readConfigArchive`、导入预览/写入及发布payload/rendering/storage。现有归档为configs.json + files/<file_id>/<file_name>；版本静态包为config.json + manifest.json + assets/<asset_id>.bin，两者不同。

新增输入、来源查询和归档合同，必要的import兼容支持属于范围；不改Vue视觉、不新增定时导出/云存储/导出任务中心，不增加备份支持的数据库类型。沿用当前同步导出，但临时文件流式返回和超时清理按本PRD约定。

## 3. 选择规则

| 来源 | 可选范围 | 可选内容 | 取数 |
| --- | --- | --- | --- |
| editor | 多环境、多渠道 | 整范围或配置勾选 | 当前有效scope的编辑区，不存在发布版本也可用 |
| active | 明确的一个环境/渠道 | 第一版整份快照 | 用户看到的生效版本，传回对应snapshot_id防切版误导 |
| version | 明确的一个环境/渠道和版本名 | 第一版整份快照 | 固定scope_id+version+snapshot_id |

发布来源第一版不做跨多个scope选不同版本，也不做快照内部分配置裁剪，避免隐式合并多个不同版本。用户仍可在editor模式自由勾选；发布模式可自由选择该渠道内**未删除的**版本，包括当前未生效的版本。

选择列表来自当前scope的release/list，分页按已有接口；active信息来自release/active。选中版本的名称、snapshot_id、scope_id作为固定目标。前端不必输入ID，后端接口合同必须提供可透传的字段。

## 4. API合同

### 4.1 来源查询

复用GET `/api/v1/release/list`、`/release/active`、`/release/detail`取得版本及身份；不为同一内容再造一套版本枚举API。

GET `/api/v1/transfer/export-tree`保留编辑区树，并为环境/渠道条目返回environment_id/scope_id。该树只表示编辑区，不能因选中active就继续展示编辑区config_count当快照数量。

新增只读 POST `/api/v1/transfer/export-preview`，JSON与export相同；仍沿用管理Cookie/POST CSRF。返回code200/data包含resolved_source、counts、warnings[]、estimated_bytes（十进制字符串），不生成包、不改配置。

### 4.2 导出请求

保留POST `/api/v1/transfer/export`，JSON扩展为：

```json
{
  "format": "zip",
  "source": "version",
  "environment_key": "prod",
  "pipeline_key": "main",
  "expected_scope_id": "11111111-1111-4111-8111-111111111111",
  "version": "v1.0",
  "expected_snapshot_id": "22222222-2222-4222-8222-222222222222"
}
```

- source只允许editor/active/version；缺省editor兼容旧客户端，显式空值或未知值400。
- format缺省zip，只允许zip/tar.gz；不静默把无效格式当zip。
- version模式要求范围、expected_scope_id、version、expected_snapshot_id。按scope_id+version解析；ID不一致409。
- active模式要求范围、expected_scope_id、expected_snapshot_id，不传version；请求开始时读取active一次，若与用户所见不符409 `TRANSFER_ACTIVE_CHANGED`，让用户刷新确认，不偷偷导出新生效版本。
- editor模式使用原selections；禁止同时提交发布来源字段。单条选择仍允许environment_key、pipeline_key空表示全环境、resource_keys为空表示整渠道，并增加expected_environment_id/expected_scope_id：明确选中对象时传对应身份；整环境按expected_environment_id绑定，再展开其有效渠道。
- 对历史缺省editor且无身份字段的请求，仍允许服务端在请求捕获点按key解析一次**当前**身份，并固定整个导出；新前端显式传身份以发现同名重建。不把此兼容规则扩展到任务一的管理写接口。
- 同一环境/渠道key与identity冲突409 SCOPE_IDENTITY_CONFLICT；来源scope已删410 SCOPE_DELETED；版本已删410 RELEASE_VERSION_DELETED。不会退回editor或其他版本。

预览成功data.resolved_source含kind、environment_key/pipeline_key（editor多范围时在scopes[]）、environment_id、scope_id、version/snapshot_id（editor为null）、captured_at、freeze/head相关信息（仅适用时提供）。counts含configs/assets/bytes。warnings用于外部引用和editor内容可能变动说明。

editor预览不是锁版，预览后修改编辑区，实际导出可反映**导出捕获时**的内容；文件元数据必须记录实际captured_at/counts，不能使用旧预览数字伪装同一份。需要固定内容的用户先锁版或创建版本再导出。

### 4.3 响应

export成功返回ZIP或GZIP二进制和Content-Disposition，文件名包含来源与版本（如rainbow-transfer-version-v1.0-<短snapshot>.zip），不包含绝对路径。错误返回真实HTTP状态+code/msg/error/error_type，不返回200错误体让前端下载成坏ZIP。

响应头：Cache-Control:no-store、X-Rainbow-Export-Source，发布来源另带X-Rainbow-Snapshot-Id和X-Rainbow-Version；已配置跨域时暴露这些响应头和Content-Disposition，不改变来源/鉴权策略。

| HTTP | error_type | 场景 |
| --- | --- | --- |
| 400 | TRANSFER_INVALID_REQUEST | 混用字段、非法source/format、缺选择参数 |
| 404 | TRANSFER_SELECTION_NOT_FOUND / RELEASE_VERSION_NOT_FOUND / RELEASE_NOT_ACTIVE | 配置选择不存在、版本不存在、无生效版本 |
| 409 | TRANSFER_ACTIVE_CHANGED / RELEASE_SNAPSHOT_MISMATCH | 选择时和提交时目标变化 |
| 409/410 | SCOPE_IDENTITY_CONFLICT / SCOPE_DELETED | 同名范围已替换或删除 |
| 410 | RELEASE_VERSION_DELETED | 指定版本已删 |
| 413 | TRANSFER_EXPORT_LIMIT_EXCEEDED | 输出超限 |
| 503 | TRANSFER_RESOURCE_INCOMPLETE / TRANSFER_EXPORT_TIMEOUT | 资源不完整或超时 |
| 401/403 | 既有AUTH错误 | 管理鉴权失败 |

## 5. 迁移归档格式及来源记录

继续使用可导入的 `configs.json` 和 `files/<file_id>/<file_name>`，ZIP/TAR统一逻辑。configs.json中增加 `archive_format:"rainbow-transfer-v2"`、`export_source`、resources清单摘要；保留既有configs/environments/pipelines/assets等字段结构（以当前writer真实名称为准），不得新造同义字段后让旧reader静默忽略。

export_source包含kind、captured_at、producer、scopes[]（各scope身份/key/源version/snapshot_id）。内部身份是来源追溯信息，导入时不能当目标身份使用。

- 内容原文、大整数、description、alias和类型按现有无损规则保留。不能经过float64或浏览器解析再回传打包。
- export中不包含密码、会话、锁版状态表、发布历史或恢复任务。备份包用途不变。
- 包内范围描述是**被导出来源**的元数据，version模式优先快照创建时保存的名称，不取后来修改的编辑区名称冒充快照内容。
- 归档资源只含导出配置实际引用的受管资源，不把整范围无关资源悄悄塞入。editor保持引用所需资源；如旧行为额外包含无引用资源，这是本轮有意收敛并在文档说明，另用完整备份保存全部资源。
- 空范围/空快照允许生成合法0配置归档；范围元数据和来源仍在。导入预览显示0，不捏造失败；导入执行可返回0项成功但不自动发布。选中不存在的具体resource_key返回404，不把输入错误当空包。

## 6. 快照转可导入资源的关键规则

不能直接导出runtime渲染后的URL，也不能用SourceFileID去读原始上传目录。原文件可能已改或已删除；必须从该snapshot的发布清单和独立资源文件读取并校验SHA。

1. 读取一次不可变快照payload、清单及完整资源计划；按版本现有渲染器识别 `release-asset://<asset_id>`，保留query/fragment和富文本/CSS/srcset规则。
2. 用 `(snapshot_id,asset_id)` 生成稳定的迁移资源file_id，建议项目已有UUID v5(namespace固定且标明版本)；同一快照重复导出得到同ID，不同快照同名资源得到不同ID。不能使用当前原始上传file_id作为跨快照身份。
3. 将受管标记改为 `asset://<导出file_id>`，构造对应asset元数据和files条目。配置中不得保留编辑区URL、release-asset标记或依赖在线虹桥的快照下载URL。
4. 完整验证引用闭包后写归档；任何缺清单/缺文件/摘要不符均整体失败。允许的外部超链接保留原值并写warnings，不声称已离线固定外部内容。
5. 导入到另一个scope时，按目标scope生成/映射目标资源身份并重写引用；v2建议以固定namespace对 `(目标scope_id,归档file_id)` 做UUID v5，保证同包重试在同目标可复用已有相同字节资源，不同目标不得共用错误归属。目标已有同标识但不同字节时保留原文件并报告冲突，不能按overwrite配置选项直接覆盖资源。冲突时按现有明确的逐资源结果报告，不能“导入成功”但静默丢资源；此新映射先用于v2，旧包资源协议保留已有兼容行为。

补齐导入reader对archive_format/export_source的识别和校验：v2正常预览/导入；旧无标识包继续兼容；未知格式400；runtime静态ZIP和业务备份ZIP明确提示包类型不符，不尝试猜格式执行。

导入保持现有overwrite选项和目标冻结校验，创建新的edit_revision，不复活来源已删除scope，不复制来源freeze/head/版本号/active。导入同名key范围时，明确映射到用户选择的当前目标身份；如需新建范围则调用任务一的新建身份规则。

## 7. 并发、取数和清理

复用一个“捕获导出计划→渲染归档→发送临时文件”的流程，editor和release各自提供捕获适配，不复制两套writer。不要把所有逻辑继续追加进transfer_service.go。

- editor：在现有业务写协调保护下直接从DAO取配置和元数据、验证选择范围并复制资源到本任务临时目录；不从Redis取导出基线。协调只用于捕获，释放后打包发送。不能在多scope中按顺序边读在线数据边打包得到混合时点。
- active/version：开始时固定scope和snapshot，并校验生命周期/资源；用现有只读快照计划。已固定导出在删除/切版后可完成；删除后的新请求必须拒绝，不换目标续做。
- 所有source统一总期限60秒（不延长调用方更短期限），未压缩归档输出最多192MiB，失败不发送半成品。打包/发送后临时文件清理含取消、断网和错误；流式处理，不能将发布资源整份复制成多份内存对象。
- editor取协调必须遵守已有backup维护保护：维护中返回503，不偷偷绕过为另一套写锁。发布只读导出在不影响维护数据一致性条件下按既有读取规则可用。
- 精确使用tuple身份去重，不能靠env+":"+pipeline+":"+alias字符串拼接产生歧义。选择或读资源失败整体报错，不继续跳过后返回不完整“成功包”。

## 8. 实施顺序与代码职责

研发使用 `$engineering-quality-preserving-refactor`（`C:/Users/MalfuRisa/.codex/skills/engineering-quality-preserving-refactor/SKILL.md`）。

1. 确认任务一scope合同；给export输入定义source和选择字段，补IDL并经hz生成。
2. 抽取可复用归档计划/writer；editor改为固定DAO计划，保持原文与资产核验。
3. 新增快照到迁移计划适配器，复用release标记渲染规则，不另写富文本正则。
4. 增加preview及来源元数据、响应头、错误映射；导入reader支持v2、旧格式兼容与目标资源映射。
5. 更新API index/management及迁移说明，保留用户要求的main文档格式。前端后续据source切换模式，不在本批次改页面。

建议独立文件：transfer_export_types、transfer_export_plan、transfer_export_release、transfer_export_archive、transfer_export_errors；输入绑定留handler，业务取数留service，路径与写包小方法复用现有archive代码。不引入任务队列、数据库新历史表或新的缓存框架。

## 9. 验收场景

| 场景 | 必须验证 |
| --- | --- |
| editor与active内容不同 | editor包是编辑内容，active包是生效快照；来源和版本记录准确 |
| version选未生效旧版 | 包来自所选快照，编辑区修改/原资源替换或丢失都不改变快照导出 |
| 下载中切active | 用户选择目标不符时409；已开始合法捕获后切换，整个包仍同snapshot |
| 同名范围删除重建 | 旧scope/旧snapshot请求410或409；不导出新范围同名v1.0 |
| 版本包往返 | 发布版→迁移ZIP/TAR→独立目标导入→检查配置原文及资源SHA/URL→锁版发布成功 |
| 多类型富文本资源 | img/srcset/CSS/query/fragment等受管引用均改成可导入asset，包内无在线快照依赖 |
| 导入两个目标/重复导入 | 归属各自正确，重复不会冲突丢文件，不污染其他scope |
| 大整数及字段 | 9007199254740993原文无损，description/type/alias不丢 |
| 旧editor请求及旧包 | 缺source仍editor；旧归档可导入；非法source/混字段400 |
| 空范围/无active/不存在配置 | 空范围合法0包；无active404；指定无效配置404，不能偷跳过 |
| 资源缺失/清单损坏/超时 | 明确503/413，文件清理，无坏ZIP伪成功 |
| ZIP/TAR、带前缀/根路径 | 内容等价，无反斜杠ZIP路径和双前缀，导入后资源可真实读取 |
| 权限/维护/快照删除 | 未登录401、缺CSRF403、维护写保护按合同，已删版本410 |

必须实际构造“编辑区A、生效B、未生效C”三份不同数据，分别导出后读包比较；再将指定版本归档导入另一个独立实例，读库并按原始SHA核对资源。仅Mock或文件下载成功不足以证明按版本导出正确。

执行受影响Go DAO/service/transfer handler/发布/备份相关测试、vet和独立HTTP往返脚本，不对演示数据做覆盖测试；不要求本轮做压力测试或跨数据库支持扩展。

## 10. 实施与验收结果（2026-10-26）

状态：后端已实现；前端页面不在本轮。

### 10.1 落地内容与格式

- 入口：`POST /api/v1/transfer/export` 支持 `source=editor|active|version`；新增 `POST /api/v1/transfer/export-preview`。
- 计划/取数：`transfer_export_types.go`、`transfer_export_plan.go`、`transfer_export_release.go` 复用现有 release 渲染/清单校验；editor 在写协调内捕获，发布来源读取不可变快照。
- 归档：`transfer_export_archive.go` 生成临时 ZIP/TAR.GZ，`configs.json` 增加：
  - `archive_format:"rainbow-transfer-v2"`
  - `export_source:{kind,captured_at,producer,scopes[]}`
  - `resources:[{file_id,file_name,content_type,size,sha256}]`
  - 保留 `environments`、`pipelines`、`business_configs`、`assets`、`warnings`
- 版本资源：`release-asset://` 按现有规则改写为 `asset://<UUIDv5(snapshot_id,asset_id)>`，保留 query/fragment；缺清单/文件/摘要失败。
- 导入：reader 识别 v2/旧包/未知格式；v2 按 `(目标 scope_id, 归档 file_id)` UUIDv5 映射并重写引用；同目标重复导入复用，不共用其他目标身份。
- 响应头：`X-Rainbow-Export-Source`；发布来源附加 `X-Rainbow-Snapshot-Id`、`X-Rainbow-Version`；`Cache-Control:no-store`。
- 文档：`docs/docs/api/transfer-export.md`。

### 10.2 验收记录

服务/集成测试与真实 HTTP 均通过：

| 场景 | 结果 |
| --- | --- |
| editor/active/version 三份不同内容 | 测试构造编辑区 C、生效 v1=A、未生效 v2=B；三份包分别读到 C/A/B，`export_source.kind` 正确 |
| 版本未生效旧版 | v2 未被激活仍可导出 B；原资源变化不影响快照包 |
| 快照切换/摘要不符 | active 提交前切版返回 409 `TRANSFER_ACTIVE_CHANGED`；snapshot 不符返回 `RELEASE_SNAPSHOT_MISMATCH` |
| 版本包往返 | v2 版本包导入独立 `qa_import/main`；配置原文、资源字节 `PNG-A-BYTES` 正确，资源 ID 为目标 scope 新 UUIDv5，重复导入不写入重复配置 |
| 资源闭包 | 版本包只含快照实际引用资源；缺文件/摘要错误整体失败；外部 URL 只 warning |
| 同名范围删除重建 | 旧 scope 导出请求 410；新 scope 不会读到旧版本；旧 expected_scope 导入被拒绝 |
| ZIP/TAR 与字段 | ZIP 实测通过；大整数/description/alias/type 由现有无损 JSON 路径保留；TAR 由同一 writer 分支和既有 archive 测试覆盖 |
| 旧 editor 请求/旧包 | 无 `source` 仍按 editor；旧无标识归档继续导入；未知格式拒绝 |
| 权限/维护 | 未登录 401、缺 CSRF 403；editor 导出受写协调/备份维护保护；发布来源只读 |
| 既有回归 | `go test ./...` 通过；`go vet ./...` 无输出；runtime 静态导出/备份路径保持独立 |

真实 HTTP 验证了三种导出的响应头、`export-preview` 计数、版本包导入、资源下载与删除重建后的 410。

### 10.3 未完成/限制

- 真实 HTTP 往返使用 SQLite；MySQL/PostgreSQL 未运行。
- 192 MiB 未压缩上限、60 秒超时、断网清理做了代码路径约束但未做压力/故障注入。
- TAR.GZ 使用同一计划/writer，已有 archive 测试覆盖；未单独做一次真实进程 TAR 往返。
- 新路由 `/transfer/export-preview` 按仓库现有路由结构手工注册；pb.go 已用 hz 在独立工程重生成。
- 未执行 `go test -race`（本机默认 `CGO_ENABLED=0`）；导出计划/临时文件清理的并发边界由单测覆盖。
- 前端需要 source 选择、范围/版本选择、来源摘要；编辑器勾选保持；发布模式整版选择；页面提示“迁移包：导入后进入编辑区，不自动生效”。
