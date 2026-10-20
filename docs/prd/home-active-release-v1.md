# PRD：首页展示生效版本，并导出同一份版本静态包

任务编号：RB-HOME-RELEASE-01

日期：2026-10-20。

状态：实施设计，尚未开发或验收。本文件放在仓库研发目录，不进入桌面最终工作产出目录。

仓库：`D:/kylin/rainbow_bridge`。现有分支：`codex/config-freeze`，本次核验HEAD：`f6184b4`。沿用已完成的配置冻结与版本快照，不重新设计发布流程。

## 1. 本轮要解决什么

首页叫“运行时配置”，用户自然理解为平台当前生效的配置。但首页实际读取编辑区，版本发布页却展示已发布快照，两处可以不同。这是首页的数据来源和产品含义不一致，需要修正。

现场已核对：默认环境、默认渠道中，首页旧接口返回5条配置，含新增的`ss`；生效版本`2`返回4条，不含该项。版本读取本身符合旧设计，问题在首页仍接编辑区。

本轮完成后：

1. 首页只展示所选范围的当前生效快照，明确显示版本号与本次加载时间。
2. 修改编辑区、新建但未生效的版本，都不会改变首页内容；切换生效后，首页下一次加载或刷新显示新版。
3. 首页导出的静态包与页面本次展示的快照完全对应，包括配置及受管资源。
4. 未发布、读取失败、资源不完整要明确表达，不拿编辑区或另一版本顶替。
5. 保留旧接口的编辑区行为，避免已有调用方被无声改变。

“运行时”在这里表示**平台已发布的生效配置**，不表示系统能知道每个外部应用缓存了哪个版本。业务应用仍需接入版本读取接口；本轮不强制迁移旧调用方。

## 2. 范围和现状证据

### 2.1 已核实的链路

| 位置 | 当前行为 | 本轮处理 |
| --- | --- | --- |
| `vue/src/components/RuntimeConfig.vue` | 调用`runtimeApi.config`，按编辑区结果渲染；错误仅toast，可能留下旧数据 | 改为加载生效快照，增加明确状态与批次隔离 |
| `vue/src/lib/api/runtime.ts` | `config`读取`/runtime/config`；`exportStatic`下载`/runtime/static` | 保留旧方法，新增明确的生效读取及版本导出适配 |
| 首页`handleExport` | ZIP失败后把页面数据另存JSON并提示成功 | 删除这条降级；新导出失败只提示失败 |
| `vue/src/components/ApiDocs.vue` | 旧接口仍叫运行时配置，静态接口误写POST和“实时状态” | 更新为生效/固定读取和版本导出；旧接口明确标为编辑区兼容接口 |
| `biz/service/release_runtime.go` | `RuntimeRelease`支持active/fixed，返回快照标识；资源下载已有完整SHA校验 | 复用快照解析、读取校验与渲染规则 |
| `biz/service/runtime_service.go` | `ExportStaticPackage`从编辑区及原始资源组ZIP | 不用于新版本导出，保留旧行为 |
| `idl/biz/release.proto` | 有版本读取与资源下载，没有版本静态导出 | 扩展读取格式参数，新增版本静态导出声明并重新生成 |

已有版本读取会输出对象型JSON内容；通用前端客户端使用`response.json()`，超大整数可能在浏览器再次转换时失真。现有Service内部保存内容字符串，本轮通过可选原文格式解决首页展示和复制精度，不重写全局JSON客户端。

### 2.2 不纳入本轮

不改变锁版、创建、切换、审计和不可变版本语义；不增加自动发布、自动轮询、个人权限、版本删除、生产部署或数据库迁移。首页不增加编辑区预览切换，编辑内容继续去配置管理页查看。

本机配置、启动脚本、`docs/onboarding/`、临时文件等已有未跟踪内容必须保护，不整包加入提交。本轮PRD不授权修改用户当前运行实例的配置或生效指针。

## 3. 首页应该怎样表现

### 3.1 正常展示

- 标题改为“当前生效配置”，说明“展示所选环境和渠道已发布的版本，编辑区修改需发布后才生效”。
- 保留环境、渠道选择器和刷新操作。未选齐范围时不发起配置读取。
- 成功结果同一次性更新：范围、版本号、快照标识、active revision、内容摘要和配置列表。主界面显示版本、配置数、本次加载时间；长标识可以放在可展开详情或复制项，不占主要表格。
- 环境/渠道键使用当前选择和响应的范围校验；快照中保存的历史显示名称不用于修改当前选择器。
- 按钮显示“导出此版本静态包”，旁边能看到导出的版本号。复制项复制本次快照内容。
- 展示“按本次加载结果展示，切换后请刷新”，不承诺页面常驻时实时追踪其他管理员操作。

不把“当前生效”标签、版本名和正文分成多个独立active请求拼接。统一以一次`/runtime/release`成功响应为页面事实。

### 3.2 状态区分

| 状态 | 页面文案与行为 |
| --- | --- |
| 未选择范围 | “请选择环境和渠道查看生效配置”；无版本、无内容，禁用导出 |
| 加载中 | 清空上一结果，显示加载态，禁用导出及旧内容复制 |
| 404 `RELEASE_NOT_ACTIVE` | “尚无生效版本，请先创建版本并切换生效”；提供携带范围的版本发布页入口 |
| 有生效快照且`configs=[]` | 显示该版本、“本版本没有配置”；允许导出空版本静态包 |
| 503 `RELEASE_INCOMPLETE` | “生效版本不完整，暂时无法读取”；不能写成暂无配置或未发布 |
| 404范围不存在、其他服务器或网络错误 | 独立错误态，显示原因与重试；无成功表格、无可用导出 |

刷新失败也清除可操作的旧结果，不继续把旧表格当作当前成功结果；不请求旧接口补位。失败时不创建JSON下载。

### 3.3 并发与切换

环境变化、渠道变化、手动刷新都开启新的请求批次。使用分别存储的范围字段和批次号，不使用可碰撞的`env + '/' + pipeline`拼接值当作唯一身份。

成功、错误、finally、复制提示与导出结果都必须校验操作捕获的范围和批次。A→B→A的旧A不能覆盖新A，旧请求结束不能提前关闭新请求的加载态。卸载后丢弃结果，可取消网络请求，但不依赖取消必然成功来保证正确性。

## 4. 首页读取契约：复用版本接口，增加可选原文格式

### 4.1 请求

```http
GET <basePath>/api/v1/runtime/release?content_format=raw
x-environment: default
x-pipeline: default
```

首页不传`version`，仍按现有规则读取active。保留原接口的固定读取方式：`?version=2`。`version`缺失与显式空值的既有处理不变。

新增query参数`content_format`：

| 取值 | 语义 |
| --- | --- |
| 缺失或`json` | 完全保留原有成功正文：object/keyvalue的content为原生JSON对象，其余为字符串 |
| `raw` | 每项content均为字符串；结构化内容返回资源地址渲染后的JSON原文 |
| 空值或其他值 | 400 `RELEASE_INVALID_REQUEST` |

在IDL中增加字段，不修改已有编号；handler保留query参数是否存在的判断。显式JSON输出仍保证空数组`[]`，不能因生成模型omitempty省略必要字段。

### 4.2 响应与精度

沿用`code/msg/data`和现有`data`字段：`snapshot_id`、`version`、`selection_mode`、`active_revision`、`source_freeze_revision`、`payload_sha256`、`environment`、`configs`。raw只改变content的表示，不改变配置type或版本选择规则。

例如原文`{"id":9007199254740993}`在raw响应中作为字符串传输。首页不得再对它执行`JSON.parse`后`JSON.stringify`来生成展示或复制内容，应直接使用字符串。其他标量字符串、空内容也不丢弃；不伪造createdAt/updatedAt等快照没有提供的时间字段，使用专门只读展示模型。

active响应添加`Cache-Control: no-store`，首页读取也使用不缓存策略。固定读取不改变现有选择含义。本次只修改目标API适配，不能为了raw模式改动所有旧接口的返回结构。

前端适配必须校验成功正文：`selection_mode=active`，version/snapshot_id非空，范围键与本次请求相同，configs为数组且每项content为字符串。不满足视为响应不兼容/加载失败；不能把200 HTML、空对象或旧服务器忽略raw参数后的结构化content强行转成成功。

## 5. 导出设计：固定导出页面刚展示的那份快照

### 5.1 关键决定

首页加载版本2后，另一管理员切到版本3，页面尚未刷新时，导出仍应得到页面展示的版本2。否则用户看到一份、下载另一份，又产生新的不一致。

因此导出请求固定携带成功加载时的范围、version与snapshot_id；不在导出时重新读取active。按钮“此版本”及本次加载时间表达这一点。刷新后页面显示版本3，后续导出才跟着变成3。

### 5.2 新接口

```http
GET <basePath>/api/v1/runtime/release/static?version=2&expected_snapshot_id=<UUID>
x-environment: default
x-pipeline: default
```

- `version`、`expected_snapshot_id`和两个范围头均必填；空值、非法格式返回400 `RELEASE_INVALID_REQUEST`。
- 先按范围与version查询已提交版本，再比对expected_snapshot_id；不允许凭其他范围的snapshot_id绕过定位。
- 无范围/版本分别沿用404 `RELEASE_SCOPE_NOT_FOUND` / `RELEASE_VERSION_NOT_FOUND`；标识不一致新增409 `RELEASE_SNAPSHOT_MISMATCH`。
- 成功200，`Content-Type: application/zip`，`Content-Disposition`附件下载，`Cache-Control: no-store`。
- 文件名固定使用已校验的snapshot UUID与version，如`release-<snapshot_id>-<version>.zip`，不直接拼未经约束的环境名或原文件名。
- `X-Rainbow-Snapshot-Id`与`X-Rainbow-Version`响应头供前端校验；同源可读，若支持既有跨域部署，在原CORS策略内显式暴露这两项和Content-Disposition，不放宽来源权限。
- 错误为JSON，真实HTTP状态与code一致。资源缺失、长度或SHA不匹配返回503 `RELEASE_INCOMPLETE`；磁盘/构包失败沿用500存储/内部错误；总输出超限413 `RELEASE_LIMIT_EXCEEDED`。
- 只读权限沿用现有版本读取权限，不新增管理令牌要求，不变更数据库或写审计。

接口放入`release.proto`及release handler，不改旧`runtime/static`。

### 5.3 ZIP格式

```text
config.json
manifest.json
assets/<asset_id>.bin
```

`config.json`使用新的版本静态包格式，仍为`{code:200,msg:"OK",data:{...}}`；data包含：

- `snapshot_id`、`version`、`source_freeze_revision`、`payload_sha256`；
- `selection_mode:"fixed"`；不生成“导出瞬间最新active”的声明；
- `environment`和`configs`，结构化content按原生JSON输出，使用已有无损JSON处理保留数字；空配置仍有`configs:[]`；
- 受管资源引用改成`./assets/<asset_id>.bin`，以解压包根目录为基准，保留原query/fragment。

`manifest.json`包含`format:"rainbow-release-static-v1"`、相同范围键/version/snapshot_id，以及`assets:[]`清单；每项含asset_id、path、原file_name、content_type、size、sha256。目录条目名只由受控asset_id构造，不使用原始文件名落盘；图片类型信息由清单保留。

`payload_sha256`指源快照payload摘要，不是重写后的config.json文件摘要。不要拿两者直接做相等断言。资源SHA必须与原清单一致。

清单资源全部包含且每份只写一次；不带编辑区其他资源。object/keyvalue嵌套引用、富文本src/srcset/poster/style及普通受管链接沿用现有引用识别和替换规则。包中不得残留`release-asset://`标记、编辑区资源路由或该平台快照下载路由；原本允许的外部业务超链接保留，不伪装为包内资源。

该包是配置与资源静态数据包，不生成业务网页，不作为旧导入格式或备份恢复包使用。旧静态包格式和旧导入契约不变。

### 5.4 后端一致性与资源释放

建议新增`biz/service/release_export.go`，按以下顺序处理：

1. 校验输入，固定本次范围及版本行，核对expected_snapshot_id。
2. 读取该snapshot的payload和清单，验证源payload与清单摘要。
3. 从快照目录读取清单资源；完整核对实际字节数与SHA。**将校验过的同一份字节用于构包**，不能验证一次后再无校验地重新打开文件，留下被替换窗口。
4. 用同一snapshot的payload与资源映射生成config.json/manifest，再完成ZIP。
5. 全包准备成功后才发200及下载头；失败返回JSON，取消/失败/成功完成都清理本次临时文件。

不能调用旧`ExportStaticPackage`、编辑区`prepareExportAssets`，不能通过HTTP回抓编辑区资源。复用快照存储读取、路径保护和渲染规则；如需抽取通用渲染器，让在线读取与ZIP分别提供目标地址解析，禁止用全局字符串替换拼凑资源URL。

导出不获取写协调闸门，不在大文件构包期间持有数据库写锁。版本不可变，短查询固定元数据后即可处理文件；并发生效切换不应污染本包。

复用现有快照数量/大小限额，另限制本次未压缩输出总量不超过192MiB（包含两个JSON文件和资源）；累计超过即413。该导出限额可能让少数合法但展开后过大的快照无法导出，应给出明确超限原因，不影响它按原方式在线读取。优先临时文件构包，避免同时把源资源、展开JSON和ZIP多份常驻内存。整次处理最多60秒，服从更短调用方期限；超时在响应开始前返回503 `RELEASE_EXPORT_TIMEOUT`，主动取消及时停止后续读取并清理，不追加写状态。已开始传输后的网络中断视为下载失败，不能承诺网络发送具有事务回滚能力。

## 6. 前端导出和接入说明

点击时捕获成功结果的范围、version、snapshot_id、批次；加载中、错误、未生效时不能导出，合法空版本可以导出。

下载适配必须同时验证HTTP成功、ZIP Content-Type及两项版本响应头与捕获值一致，然后才创建Blob下载；失败响应先解析JSON错误，不把HTML或错误体另存ZIP。响应头缺失或不匹配视为失败，禁止下载。

导出过程中切范围、刷新或卸载后，旧结果不自动触发下载、不弹旧成功提示；不取消服务端也必须丢弃结果。捕获当前操作对象，旧finally不得清除新导出的状态。导出成功/失败均释放对象URL等客户端资源。

彻底删除现有“ZIP失败后导出页面JSON”的catch降级，不再提示“已导出为JSON格式”。需要以后支持JSON下载时另设明确功能，本轮不混入。

首页“前端对接”列出：

| 方法和路径 | 说明 |
| --- | --- |
| GET `/api/v1/runtime/release` | 默认读取当前生效版本 |
| GET `/api/v1/runtime/release?version=<版本号>` | 固定读取指定已发布版本 |
| GET `/api/v1/runtime/release/static?version=...&expected_snapshot_id=...` | 固定导出已展示快照的静态包 |
| GET `/api/v1/runtime/config` | 兼容旧接口：读取编辑区，不受发布切换控制 |
| GET `/api/v1/runtime/static` | 兼容旧接口：导出编辑区静态包 |

说明范围请求头，路径复制根据实际basePath生成，避免漏前缀或加两次前缀。`content_format=raw`作为需要原文展示的补充用法说明，不要求所有业务调用方使用它。页面提供前往版本发布和配置管理的入口，不在首页新增发布写操作。

## 7. 实施位置与工程要求

研发必须使用`$engineering-quality-preserving-refactor`，实施前读取 `C:/Users/MalfuRisa/.codex/skills/engineering-quality-preserving-refactor/SKILL.md`，并遵守仓库AGENTS.md。

| 部分 | 建议落点 | 职责 |
| --- | --- | --- |
| 读取格式与接口声明 | `idl/biz/release.proto`、release handler | raw参数、版本导出输入/错误及二进制响应 |
| 快照导出 | 新`biz/service/release_export.go`及测试 | 固定快照、完整性校验、局部引用、ZIP和临时资源生命周期 |
| 共享渲染/校验 | `release_snapshot.go`、`release_html.go`、`release_runtime.go`、`release_storage.go` | 复用边界，不往841行release_service.go追加整个导出模块 |
| 首页数据及下载适配 | `vue/src/lib/api/runtime.ts`或release API补充、目标类型 | 只读结果、raw与二进制错误处理，不改变全局旧接口 |
| 展示状态 | `RuntimeConfig.vue`及必要的独立composable | 一个结果对象、范围/批次隔离、空态/错误、固定版本导出 |
| 文案 | `ApiDocs.vue`，必要时配置管理/版本页说明 | 生效配置和编辑区边界一致 |
| 正式回归 | Go导出测试、`tests/e2e/local/release/` | 真HTTP、ZIP内容与页面行为 |

IDL生成使用项目hz流程，不能手改pb.go和生成路由；只保留目标生成变化，排除工具注释漂移。不为了两处下载逻辑引入全局策略框架或全仓重构。共享方法保持小职责、清晰输入输出，取消/清理路径使用明确所有权。

## 8. 按顺序交付

1. 补raw模式和新版本导出接口，先用服务层测试证明不读编辑区、固定snapshot及资源字节一致。
2. 首页改接生效读取，建立完整状态和批次规则，再接固定快照下载，删除假成功降级。
3. 统一首页API说明与跳转，再补正式页面/API回归。
4. 在最终代码上执行相关Go测试/vet、前端类型检查与构建、Release专项。旧基础测试若断言首页展示编辑区，按新产品契约调整为显式调用旧接口验证兼容，不能直接删除该验证。

仅在触及原导入、冻结、旧静态导出等路径或发现新失败时扩展Full/Freeze回归。确认旧接口兼容的定向验证必须执行，不能因未改旧文件就省略。

## 9. 具体验收场景

所有写入、切换与缺文件故障在独立测试范围/实例执行，不改用户18080实例的default数据。

| 编号 | 输入或时序 | 预期与验证 |
| --- | --- | --- |
| H01 | 编辑区5条、active版本2仅4条 | 首页4条、版本2；编辑区特有ss不出现；请求记录无旧config读取 |
| H02 | 保存版本3但不激活，再修改编辑区 | 首页刷新仍为2，正文及快照标识不变 |
| H03 | 激活3后刷新，随后切回2再刷新 | 两次版本/标识/正文一起切换，不自动发布编辑区 |
| H04 | 有编辑区配置但从未激活版本 | 明确尚无生效版本，无编辑区替代、无导出；发布页入口范围正确 |
| H05 | 激活合法空快照 | 显示版本及空配置，允许ZIP，包内configs/assets均为空数组 |
| H06 | 成功后删除快照资源，刷新 | 503错误态，旧结果不可导出/复制；不变成暂无配置或成功JSON下载 |
| H07 | 网络失败、普通500、范围不存在、异常HTML响应 | 独立错误态和重试，不留下当前成功标签，不回退其他API |
| H08 | 扣住A，切B再回A，旧成功/错误分别最后返回 | 列表、元数据、错误和loading只由当前批次修改 |
| H09 | 当前B仍在途，只结束旧A | 继续加载，不提前显示空态；同范围连续刷新也验证 |
| H10 | 结构化原文含9007199254740993、长小数、数组及资源 | raw读取、页面展示、复制均保留原数字；对象type不变；默认json格式兼容 |
| H11 | 加载2后后台激活3，首页未刷新即导出 | ZIP版本和snapshot仍为页面2，不请求最新active来替换；刷新后导出才为3 |
| H12 | 覆盖图片/文件、object/keyvalue、富文本各已支持引用位置及带括号query/fragment | 解包后本地路径均能定位条目，资源SHA与快照清单一致，后缀保留；无平台受管URL/内部标记 |
| H13 | 改编辑区及删除原始上传文件后导出旧快照 | 包不受编辑区内容/源文件影响，不包含未发布配置或其他资源 |
| H14 | 跨范围version/snapshot、错误expected_snapshot_id、非法或缺失参数 | 分别404/409/400；不泄漏另一范围内容；成功/失败均不改变active和事件数 |
| H15 | 缺文件、同长度篡改、源payload/清单摘要错误 | 导出503JSON，无成功ZIP；用同长度不同字节证明不是只检查大小 |
| H16 | 同一后台构包请求中切换active | 整包仍为捕获的快照，元数据、正文、文件不混版 |
| H17 | 导出返回500/503/HTML，或200但版本响应头错误 | 页面无下载和成功toast，不另存JSON；只显示可理解错误 |
| H18 | 导出未返回时切范围/刷新，再放回旧响应 | 不触发旧下载/提示，旧finally不污染新导出状态；组件卸载同样验证 |
| H19 | 资源读取/ZIP写入错误、请求取消、60秒超时、总输出恰到上限/超1字节 | 构包失败不先发200；临时文件清理；恰好允许、超限413，超时503；失败不增加持久数据 |
| H20 | 根路径与/rainbow-bridge；中文范围名、特殊范围键、正常文件名 | 路由与复制地址仅一层前缀；ZIP条目无反斜杠/绝对路径/越界，下载文件名安全 |
| H21 | 调用原config/static接口；页面ApiDocs复制新旧链接 | 旧接口仍取编辑区，原静态格式可用；文案和真实HTTP方法一致，新首页不再使用旧导出 |
| H22 | raw缺省/json/raw/空/未知值，空字符串、空数组与多类型配置 | 原有默认契约保持，raw全部content字符串且精度保持，非法参数400；configs字段不省略 |

ZIP验证必须解包核对内容与字节，不以文件名或下载成功事件替代。前端时序用受控响应重排，后端构包并发用确定性暂停或故障注入；测试钩子不得成为生产可配置绕过。实际断言失败需区分夹具与产品原因，不能放宽断言掩盖失败。

## 10. 本轮终点与材料管理

本次请求只交付PRD，不改业务代码、不代为外派、不提交推送。后续实施应在同一PRD内登记必要设计偏差，不按每次对齐另写一篇报告。

研发完成后回传最终行为、关键验证、未测边界和是否提交即可。桌面`05-虹桥`保持用户要求的每轮最终产出结构；功能完成后再补一篇本轮最终工作记录，介绍做什么、怎么做、结果，不把本PRD和调试流水放进去。

## 11. 实施登记(研发回填)

本节登记实施落点、实际契约、与 PRD 的偏差与验证结果,不再另写实施报告。

### 11.1 落点与职责

| 部分 | 文件 | 职责 |
| --- | --- | --- |
| 读取格式参数 | `idl/biz/release.proto`(content_format)、`biz/handler/release/release_service.go`(resolveReleaseContentFormat) | 缺失/`json` 保持原契约,`raw` 输出字符串,空值与其他值 400 |
| 版本静态导出声明 | `idl/biz/release.proto`(RuntimeReleaseStaticRequest/Response + RPC) | `GET /api/v1/runtime/release/static`,二进制成功、错误 JSON |
| 导出实现 | `biz/service/release_export.go`(新) | 固定范围与版本行 → 核对 expected_snapshot_id 与两类摘要 → 逐份读取资源(边写边校验字节数与 SHA)→ 临时 ZIP → 全包成功后返回路径 |
| 共享渲染 | `biz/service/release_snapshot.go`(renderReleaseContentWith + releaseMarkerRenderer) | 在线读取解析为本服务下载地址,静态包解析为 `./assets/<id>.bin`,同一套标签/属性扫描规则 |
| 首页 API 适配 | `vue/src/lib/api/release.ts`(fetchActiveReleaseRaw / exportShownSnapshot) | raw 读取逐字段校验;导出校验 HTTP、ZIP 类型与两项版本响应头 |
| 首页状态机 | `vue/src/composables/useActiveRelease.ts`(新) | 单一结果对象、范围+批次隔离、错误分类、固定快照导出与对象 URL 释放 |
| 首页展示 | `vue/src/components/RuntimeConfig.vue` | 状态文案、版本/配置数/加载时间、复制原文、跳转入口;已删除"另存 JSON"降级 |
| 接口说明 | `vue/src/components/ApiDocs.vue` | 新接口 + raw 可选 + 旧接口标"兼容";复制路径带实际前缀 |
| CORS | `biz/middleware/cors.go` | 原策略内暴露 `Content-Disposition`、`X-Rainbow-Snapshot-Id`、`X-Rainbow-Version`,不放宽来源 |

### 11.2 实际契约

- 读取:`GET /api/v1/runtime/release[?version=..][&content_format=json|raw]`;`raw` 下每项 `content` 均为字符串,结构化内容为渲染后的 JSON 原文(大整数按字面保留);`configs` 始终为数组(空版本为 `[]`)。
- 导出:`GET /api/v1/runtime/release/static?version=..&expected_snapshot_id=..`(两个范围头必填)。成功 200 + `application/zip` + `Content-Disposition` + 两项 `X-Rainbow-*` 头 + `Cache-Control: no-store`;参数非法 400 `RELEASE_INVALID_REQUEST`,范围/版本缺失 404,标识不一致 409 `RELEASE_SNAPSHOT_MISMATCH`,资源缺失或摘要不符 503 `RELEASE_INCOMPLETE`,未压缩总量超 192MiB 413 `RELEASE_LIMIT_EXCEEDED`,超过 60 秒 503 `RELEASE_EXPORT_TIMEOUT`,存储与内部错误 500。导出不取写协调、不写审计、不改变 active。
- 包结构:`config.json`(新格式,`data.selection_mode=fixed`,受管引用为 `./assets/<id>.bin`,保留原 query/fragment)、`manifest.json`(`format=rainbow-release-static-v1`,资源按 asset_id 升序,含 asset_id/path/file_name/content_type/size/sha256)、`assets/<asset_id>.bin`(原字节);包内不得出现 `release-asset://`、编辑区资源路由或快照下载路由。

### 11.3 与 PRD 的偏差与决定

1. 导出限额与期限用**包级变量**(`releaseExportMaxOutputByte`、`releaseExportTotalTimeout`)便于测试收紧,不能由环境变量或配置改变,不构成生产可绕过开关;另加仅测试赋值的暂停钩子 `releaseExportPauseHookForTest`(默认 nil)。
2. `manifest.json` 资源 `path` 写 `assets/<id>.bin`(无 `./`),`config.json` 内容里的引用写 `./assets/<id>.bin`;两者都以解压包根为基准,便于同目录定位与相对引用。
3. `manifest.json` 额外提供 `config_count`,属新增只读字段(PRD 未列,便于校验空版本)。
4. JSON 条目用 Deflate、资源条目用 Store 写入 ZIP;未压缩总量按原始字节累计(与 PRD 的"未压缩输出总量"一致)。
5. 首页范围选择器沿用既有组件显示名称;响应中的历史名称只用于展示校验,不改写当前选择。
6. 生成代码按 hz 流程重生成:模型仍放 `biz/model/release/`;`.hz` 与 `biz/model/api/api.pb.go` 的工具漂移已还原,未纳入本次改动。
7. 新批次开始(切范围/刷新/创建或切换后的刷新)会作废旧导出:旧请求的 finally 不再影响当前状态,界面也不会卡在"导出中"。

### 11.4 验证与未测边界

见 `tests/e2e/local/release/home-active-release.spec.ts`(H01/H02/H03/H04/H05/H06/H07/H10/H11/H17/H18 的页面用例)、`biz/service/release_export_test.go`(包内容/字节/限额边界/超时/完整性/构包中切版/H13 独立性)、`biz/handler/release/release_http_test.go`(raw 契约、空 configs、导出状态码与响应头)。旧接口兼容由 `t03`/`t11`/`t16`/`t-root-replay` 继续显式调用 `/runtime/config` 与 `/runtime/static` 验证。

未在本轮验证:H19 的"ZIP 写入错误/请求取消"只能由单元级钩子覆盖(未做真实磁盘故障注入);H20 的中文范围名与特殊范围键未单独构造页面用例;真实跨域部署(浏览器跨源读 `X-Rainbow-*`)未在真实跨域环境跑;总输出恰好上限/超 1 字节由服务层单测覆盖而非 HTTP 端到端。

验证运行与结果(研发自检):

- `go test -mod=readonly -tags=dev -count=1 ./...` 通过;`go vet -mod=readonly -tags=dev ./...` 无输出。
- `cd vue && npx vue-tsc -b --pretty false` 通过;`vite build`(由套件构建阶段)通过。
- `script/test-local.ps1 -Suite Release`:**14/14 通过**(运行编号 `20261020-115112-ijrb`;8 条首页用例 + 既有 6 条发布用例),证据目录 `C:/Users/MalfuRisa/Desktop/产出/05-虹桥/附件/本机测试/20261020-115112-ijrb`。
- `script/test-local.ps1 -Suite Freeze`:4/4 通过(运行编号 `20261020-115235-bqxv`)。
- `script/test-local.ps1 -Suite Full`:**35 次执行通过**(运行编号 `20261020-115320-wtoy`,主套件 26 + 三次重启各 1 + 恢复 1 + 根路径 5),说明首页改动与 CORS 响应头未影响旧接口、导入导出与冻结路径。
- `script/release-two-process.ps1`、`script/release-crash.ps1`、`script/release-recovery.ps1` 均通过(`rainbow-release-2p-20261020-115251-cysk`、`rainbow-release-crash-20261020-115258-teji`、`rainbow-release-recovery-20261020-115309-rcsi`)。

结论:实现与自检完成,待复核。代码未提交、未推送;桌面目录只新增本轮工作记录,不含 PRD 与调试流水。
