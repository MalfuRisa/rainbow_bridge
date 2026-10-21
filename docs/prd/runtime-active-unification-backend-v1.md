# 运行时旧接口统一读取生效版本：后端 PRD

日期：2026-10-21。状态：旧接口收敛与前后端整合已实现，最终验证结果见第10节。

## 1. 目标与范围

业务应用使用默认运行时接口时，应该拿到所选环境、渠道当前生效的已发布配置。编辑区修改和未生效版本不应绕过发布流程影响应用。

用户已确认本轮范围：

1. `GET /api/v1/runtime/config` 从读取编辑区改为读取当前生效快照。
2. `GET /api/v1/runtime/static` 从导出编辑区改为导出当前生效快照，配置和资源来自同一快照。
3. 保持现有 `/api/v1/runtime/release` 的默认生效读取、指定版本读取，以及 `/api/v1/runtime/release/static` 的固定版本导出契约。
4. **编辑区预览鉴权不改动**。不新增预览接口，不调整现有配置管理、预览、原始资源、runtime概览或其它接口的访问权限。旧运行时读取与导出继续按现有公开读取策略工作，本轮改变的是数据来源。

不增加自动发布、编辑区兜底、SSE推送、缓存刷新协议、版本删除策略或数据库迁移；不扩展登录/IAM，不要求应用实时接收推送。切换后新请求读取新生效版本，在途请求和应用已有缓存不被追溯改变。

## 2. 工作位置与已核实的现状

当前仓库：`D:/kylin/rainbow_bridge`。当前分支：`main_v1`，核验HEAD：`2b3e27d175fae4fe27480859522929c5cca96889`，开始本次文档任务时工作区干净。此前整合工作树已不在原路径，不能继续使用旧PRD里的工作树地址。研发开始前重新核对分支、状态和AGENTS.md；有更新时以最新已整合代码为基线，保护用户现有改动，不reset/stash或覆盖其它批次文件。本PRD不要求新建工作树或提交分支。

| 位置 | 当前行为 | 本轮目标 |
| --- | --- | --- |
| `biz/handler/runtime/runtime_service.go:GetConfig` | 从请求头取范围，调用`GetRuntimeConfig`，转换旧JSON；部分错误HTTP200 | 参数入口与旧成功字段保留，改为生效快照投影，错误使用真实HTTP状态 |
| `biz/service/runtime_service.go:GetRuntimeConfig` | 查询环境/渠道和`logic.ListConfigs`，装饰编辑区资源URL | 不再读取编辑区配置；复用发布读取与资源渲染规则 |
| 同文件`ExportStaticPackage`、`writeRuntimeConfigArchive` | 从编辑区查配置和原始上传资源，内存构包 | 旧HTTP导出链路改走快照导出，不再落到编辑区构包器 |
| `biz/service/release_runtime.go:RuntimeRelease` | 默认解析一次active，固定读取快照，校验删除状态和完整性 | 作为共同业务规则来源，避免复制另一套选择/校验逻辑 |
| `biz/service/release_export.go:ReleaseStaticExport` | 由version+expected_snapshot_id固定快照，生成临时ZIP | 保持既有公开契约；复用其构包规则支持旧入口的active导出 |
| `idl/biz/runtime.proto` | 旧配置仅接受范围头，旧静态仅接受范围query | 保留参数来源，增加响应元数据/错误类型的契约描述，必要时生成模型 |
| `biz/middleware/admin_session.go`、`cors.go` | 两个旧GET公开；已暴露版本与快照响应头 | 保持鉴权和CORS策略，不扩大或收紧访问权限 |

首页已通过`useActiveRelease`调用发布读取接口，本轮无需改首页数据源。`vue/src/lib/api/runtime.ts`仍提供旧入口适配；页面与API说明仍存在“旧接口读取编辑区”文字，属于后续前端文案同步点。

本PRD替代旧`home-active-release-v1.md`中“保留旧接口编辑区行为”的决定。那是此前兼容策略，本轮明确改变该语义，不能再据此保留编辑区兜底。

## 3. 统一的选择与一致性规则

- 范围必须明确指定，不默认为default，不串用其它环境或渠道。
- 两个旧接口均只跟随当前生效版本，既不读最新创建版本，也不读编辑区。固定版本使用既有`/runtime/release?version=...`与固定静态导出接口。
- 每个请求先取得一次active选择结果，固定snapshot_id、version、head revision及对应版本行，随后读取、完整性检查和渲染均基于这一选择，不在读取文件、填充元数据或组装JSON时再次解析active。
- 快照中的配置、环境/渠道历史名称、描述、资源清单和字节是结果来源；不得用编辑区当前名称、配置值或原始文件补齐。沿用发布读取中对范围是否存在的检查。
- 没有生效版本、缺文件、摘要错误、active指向墓碑等场景明确失败。即使编辑区数据完整也不能顶替。
- 两次独立请求之间可能发生切换：一次配置读取与随后一次旧静态下载不保证同版本。需要导出刚展示的版本时，客户端取本次version/snapshot_id，使用既有固定导出接口；不能宣传两个独立旧请求天然绑定。
- 资源使用快照地址，已经返回的旧版本配置继续引用原快照，不会因后续切换转向新资源。版本删除限制沿用已验收规则。

## 4. 旧配置接口契约

### 4.1 请求

```http
GET /rainbow-bridge/api/v1/runtime/config
x-environment: default
x-pipeline: default
```

路径随BasePath配置变化；根路径部署没有`/rainbow-bridge`。继续只从`x-environment`、`x-pipeline`取范围并trim；不能改为必须传query或登录Cookie。

不新增固定版本或raw参数。如果请求显式带`version`、`expected_snapshot_id`或`content_format`，返回400 `RELEASE_INVALID_REQUEST`，提示使用发布读取/固定导出接口，避免静默忽略版本选择造成误读。其它无关query不作为模式开关。

### 4.2 成功响应

HTTP200，`Cache-Control:no-store`，保留旧响应必需字段和content类型，新增快照元数据；示例：

```json
{
  "code": 200,
  "msg": "OK",
  "error": "",
  "data": {
    "snapshot_id": "<本次快照UUID>",
    "version": "2",
    "source_freeze_revision": "9",
    "active_revision": "3",
    "payload_sha256": "<摘要>",
    "selection_mode": "active",
    "environment": {
      "environment_key": "default",
      "environment_name": "默认环境",
      "pipeline_key": "default",
      "pipeline_name": "默认渠道"
    },
    "configs": [
      {
        "resource_key": "<配置key>",
        "alias": "TITLE",
        "name": "标题",
        "environment_key": "default",
        "pipeline_key": "default",
        "content": "已发布标题",
        "type": "text",
        "remark": "",
        "is_perm": false,
        "description": ""
      }
    ]
  }
}
```

具体约束：

- 保留`data.configs`、`data.environment`，以及每项原有`resource_key/alias/name/environment_key/pipeline_key/content/type/remark/is_perm/description`；项级范围键从已选快照范围填充。
- 空版本成功输出`configs:[]`，不省略空字符串或false等旧必需字段；“空快照”与“没有生效版本”是两种状态。
- object/keyvalue沿用新版本接口的JSON解码规则，content为原生JSON值；其它类型为字符串。不将所有值统一改成字符串或因适配旧字段做float64转换。大整数、长小数和指数的原数字字面量不丢精度；保留数组、布尔、null的真实类型。
- 图片、文件、结构化嵌套和富文本的受管引用复用快照渲染规则，指向`<BasePath>/api/v1/release/asset/<snapshot_id>/<asset_id>`，保留query/fragment；不得泄漏内部标记、编辑区下载路由或双前缀。
- `source_freeze_revision`、`active_revision`以十进制字符串输出；version保持原字符串，不解析成数字。输出`X-Rainbow-Snapshot-Id`、`X-Rainbow-Version`，与正文一致，复用既有CORS暴露项。

不能简单调用release handler再转发或HTTP302跳转：旧JSON有项级范围键等兼容字段，需要薄适配层；业务查询仍共用服务层实现。

## 5. 旧静态导出契约

### 5.1 请求与选版

```http
GET /rainbow-bridge/api/v1/runtime/static?environment_key=default&pipeline_key=default
```

保留现有范围query名称及来源，trim后必填；不改成必须携带范围请求头，不增加version或snapshot_id必填参数。与旧配置一致，显式带`version/expected_snapshot_id/content_format`返回400，固定导出走既有新接口。

在本次请求内解析一次active并捕获该快照的导出计划，再按该计划构包。不能先读active得到version，再调用会重新查询active的导出方法；也不能使用`GetRuntimeConfig`得到在线URL后扫描原始上传目录来补包。

切换发生在本次快照捕获后：整个包仍来自原快照，新请求导出新版。非生效快照在计划固定后被逻辑删除：本次在途构包可完成，下一次新固定读取/导出遵守410。不得为了在途请求反复检查live状态而破坏既有语义。

### 5.2 成功ZIP

直接返回HTTP200 ZIP，不跳转、不返回下载地址JSON。使用既有发布静态包格式`rainbow-release-static-v1`，与同一快照的`/runtime/release/static`导出在内容语义上保持一致：

```text
config.json
manifest.json
assets/<asset_id>.bin
```

- `config.json`保留既有发布静态包的`code/msg/data`结构，包含所选快照元数据、范围和配置。包内`selection_mode:"fixed"`表示构成包的快照已固定，不代表客户端通过旧URL指定了版本。
- config中的受管资源引用为`./assets/<asset_id>.bin`，以解压根为基准，保留后缀；不残留平台快照URL、编辑区URL或内部asset标记。明确允许的普通外部超链接沿用现有发布规则。
- manifest的版本、snapshot_id、payload摘要和各文件字节/大小/SHA与本快照一致；仅包含本快照资源，每份只写一次。
- HTTP响应使用`application/zip`、附件文件名、`Cache-Control:no-store`、`X-Rainbow-Snapshot-Id`和`X-Rainbow-Version`。文件名沿用发布导出返回值，不使用未经安全处理的用户范围拼接路径。
- 使用既有临时ZIP构建、单项/总量限额、超时和完整性校验；失败前不开始成功响应，不把JSON错误或半成品作为ZIP。传输结束/失败/取消后的清理方式与当前发布下载保持一致并验证，不提前删除仍在发送的文件。

这是**有意的静态包格式变更**：旧包的原始文件名和带部署前缀资源目录不再兼容；包内每项不额外承诺旧版项级范围字段。已有解析方应按`config.json`和`manifest.json`读取新布局。本轮不保留“编辑区格式兼容”开关、不再维护两套资源构包器。静态包仍是业务离线数据包，不作为迁移、导入或备份格式；这些能力继续使用`transfer/*`管理接口。

## 6. 错误与兼容边界

旧配置和旧静态都改用真实HTTP错误状态，统一输出JSON错误信封`{code,msg,error,error_type}`和`Cache-Control:no-store`。不再HTTP200加正文code400/500。

| 条件 | HTTP / error_type |
| --- | --- |
| 缺少范围、保留选择参数非法使用 | 400 `RELEASE_INVALID_REQUEST` |
| 环境/渠道不存在 | 404 `RELEASE_SCOPE_NOT_FOUND` |
| 范围存在但没有生效版本 | 404 `RELEASE_NOT_ACTIVE` |
| active指向缺失/已删除版本、payload/清单不一致、快照资源缺失或损坏 | 503 `RELEASE_INCOMPLETE`，遵循现有在线/导出各自完整性检查强度 |
| 构包超出限额 | 413 `RELEASE_LIMIT_EXCEEDED` |
| 导出超时/取消 | 503 `RELEASE_EXPORT_TIMEOUT`，沿用现有导出约定 |
| 其它DB或存储错误 | 复用现有发布错误映射，500/503；不得降级读取编辑区 |

沿用在线读取现有“payload/清单摘要及文件存在/长度检查，资源下载完整SHA检查”的强度，静态构包仍逐文件校验SHA；不把本轮扩成每次在线读取全量文件哈希。

明确的兼容取舍：保留URL、范围入参位置、旧配置成功数据字段和content形态；改变读取来源、旧错误HTTP状态、静态包布局，并新增元数据。依赖“改编辑区即生效”“没有发布也能导出”“原始目录ZIP路径”或“只处理HTTP200”的调用方必须调整。不自动给已有编辑区建版本、激活版本或修改业务数据；无生效版本就提示先发布。

这两个GET不推进冻结/发布revision，不追加审计写事件，不续管理会话、不获取全局写协调锁；底层快照读/临时构包目录的既有行为不变。不为本轮新增结果缓存，浏览器缓存由no-store约束，应用本地缓存由调用方管理。

## 7. 职责拆分与实施细则

研发使用 `$engineering-quality-preserving-refactor`，开始实施前读取 `C:/Users/MalfuRisa/.codex/skills/engineering-quality-preserving-refactor/SKILL.md`，并遵守仓库AGENTS生成规则。

| 职责 | 建议落点 | 不变量 |
| --- | --- | --- |
| 发布快照选择/校验 | `biz/service/release_runtime.go`或同包小文件 | active只解析一次；默认/固定模式、墓碑与异常状态语义保留 |
| 旧JSON投影 | runtime handler内独立适配文件，必要时服务小适配 | 保留旧字段；不重新查编辑区；无损JSON |
| 默认生效导出编排 | release导出同包小方法 + runtime薄入口 | 捕获同一计划后复用既有ZIP构建器，不绕过限额和资源校验 |
| 固定导出编排 | 原`ReleaseStaticExport`入口 | 仍要求version+expected_snapshot_id，冲突/删除语义不变 |
| HTTP错误、附件响应 | runtime handler与既有错误转换小方法 | 真正状态码、no-store、元数据一致，临时文件所有权清晰 |
| IDL/文档 | `idl/biz/runtime.proto`与对应接口说明 | 只追加字段编号，不手改pb.go/router，不影响其它接口 |

允许提取“已选快照/已准备导出计划”共用小方法，使在线适配和两种导出入口共用选择及校验。避免通用策略框架，避免为兼容路由复制完整RuntimeRelease或ZIP实现；不能从service依赖handler。

旧`GetRuntimeConfig`不能继续成为可误用的编辑区默认读取方法，应改为快照适配或移除并迁移全部调用。旧`ExportStaticPackage`若保留也必须改为快照来源；允许改为临时包结果，更新所有调用，避免为维持私有签名把大ZIP全部读回内存。旧`writeRuntimeConfigArchive`若无生产调用应删除并将测试迁移到快照构包；若仍有独立用途必须明确其边界，两个公开runtime路由不得调用它。不要保留一个名称叫runtime、实际读编辑区的隐藏旁路。

不改变其它导入/导出、配置管理和编辑区预览实现。通用资源打包工具若还被transfer使用，不能随旧runtime清理一并删除。

## 8. 调用方、测试和文档迁移

已发现仓库内调用位置：

- `tests/e2e/local/support/imports.ts:runtimeConfig/runtimeStatic`，被` t03-config-types`、`t11-roundtrip`、`t16-missing-source`、`t-root-replay`使用。
- `tests/e2e/local/release/release-page.spec.ts`、`home-active-release.spec.ts`仍含旧接口读取编辑区的契约断言。
- `script/release-recovery.ps1`在缺快照文件时同时检查新旧runtime，需改为都明确失败，不允许旧接口回退。
- `biz/service/config_archive_test.go:TestStaticArchiveDescriptionAndContent`及`optimization_test.go`部分直接测试旧构包器，需要迁移到快照构包以保留描述/空值/精度验证。
- `vue/src/lib/api/runtime.ts`旧方法参数结构无需改变；`RuntimeConfig.vue`、`ApiDocs.vue`含过时兼容提示，`spectrum-ui.spec.ts`可能依赖旧文字。

调整原则：

1. 验证运行时发布行为的测试，应明确建立独立范围、配置、锁版、创建、激活，再调用旧runtime检查。不能在公共读helper里偷偷发布或切换，以免污染数据和让断言失去意义。
2. 本意只检查编辑区的导入/迁移/类型结果，用已鉴权的管理读取断言；同时保留独立的发布后旧runtime验证。管理读取的鉴权不变。
3. `t16`等原始文件缺失测试要区分“源文件缺失影响transfer”和“快照文件缺失影响runtime”。发布后删除原始上传文件不应破坏快照导出；删快照资源才应得到503。不能把两种用例一起删除以求通过。
4. 原始资源ZIP往返、根路径部署、数字精度、资源字节与描述清空断言继续覆盖。逐项更新旧语义断言，禁止直接skip、删用例或将所有读操作替换为管理接口逃避验证。
5. 本次后端交付允许同步API文档/README中的接口说明及测试夹具；不改Vue组件布局/请求流程。文档注明前端应移除“旧接口读编辑区”的文字，并区分可复用旧URL与固定版本新URL；实际Vue文案作为前端后续接入项单独标明。旧提示未更新不代表后端允许保留旧语义。

历史PRD作为历史记录不批量重写；更新当前对外说明，并在最终交付指出本PRD取代的旧决定。

## 9. 实施顺序与验收

1. 核对基线及调用方，建立旧格式字段与新快照语义的契约测试。
2. 提取最小共用快照选择/投影，替换旧配置查询；实现真实HTTP错误和元数据。
3. 旧静态导出复用快照计划与构包，清理不再需要的编辑区runtime链路。
4. 更新IDL并用hz生成，仅保留相关生成变化；迁移测试夹具和对外后端文档。
5. 完成下表的服务/handler测试及真实隔离实例回归，核对编辑区预览鉴权和原管理功能没有变化。

| 编号 | 场景与具体预期 |
| --- | --- |
| R01 | 编辑区有配置但未发布：旧config和static都404 NOT_ACTIVE；不能200返回编辑区或空成功包 |
| R02 | v1生效后改编辑区、新建未生效v2：旧config正文/资源和旧ZIP均保持v1；切换v2后新请求读/导出v2，切回v1恢复v1 |
| R03 | 对照新release接口：同范围、同快照的配置值/描述/范围元数据/顺序/资源一致；旧字段保留，空版本configs为[]，false/空字符串不省略 |
| R04 | object/keyvalue包含9007199254740993、负大整数、长小数、指数、数组、布尔、null：旧JSON和ZIP保类型、保精度；其它类型继续字符串 |
| R05 | 图片/文件/嵌套JSON/富文本各支持的媒体位置、query+fragment：旧在线资源指向快照；解ZIP所有资源可定位且SHA一致，无编辑区路由、双前缀或内部标记 |
| R06 | 发布后修改/删除编辑区配置、原始上传文件：旧config/ZIP不变；修改环境显示名不改快照历史名称 |
| R07 | 缺范围400、范围不存在404、未生效404、active异常/墓碑/缺快照文件503；都是JSON与真实HTTP+no-store，不返回假ZIP或回退编辑区 |
| R08 | 旧static保留query范围，旧config保留范围头；根路径和/rainbow-bridge均可用；误传version/expected_snapshot_id/content_format得到400 |
| R09 | 确定性暂停已选v1的旧config读取/构包，然后切到v2：本次正文/头/ZIP全是v1，下次全是v2；不能混版本或重新取active |
| R10 | 构包计划固定后目标成为非生效并被删除：在途包可完成；随后固定读取该版本410；默认旧接口只跟随当前active，不找回已删版本 |
| R11 | 没有Cookie可读已发布配置/导出；现有管理与编辑区预览鉴权断言不变。不得为测试把runtime改成管理接口 |
| R12 | 构包达到原限额边界、超限、取消、文件损坏、磁盘错误：复用既有错误，无半成品与临时文件残留，不占写协调大锁 |
| R13 | 既有指定版本读取、raw/json、固定导出snapshot不匹配409、删除410、缺文件503继续通过，不能因旧入口适配改变新接口契约 |
| R14 | 新旧runtime同时做备份恢复检查：缺快照文件都失败，恢复文件后免重启恢复；原transfer缺源文件和Windows往返验证仍成立 |

验证层级：

- Go服务层测试选择、精度和并发暂停；handler测试真实HTTP状态、旧字段和下载头，测试不限于私有helper。
- 实际执行`go test -mod=readonly -tags=dev -count=1 ./...`、`go vet -mod=readonly -tags=dev ./...`、后端构建。
- 使用独立数据库、资源目录、Cookie和空闲端口启动最终源码；通过真实HTTP完成“未发布→v1生效→编辑区变化→v2未生效→切换→切回→下载解包→缺文件与恢复”。匿名读取使用真实无会话请求。
- 重跑受影响Full、Release、Freeze套件和恢复脚本。现有页面文字的旧断言按第8节处理，不将后端测试通过冒充前端文案已上线。分别记录主阶段、重启、恢复和根路径的实际执行数，不只看顶层退出码。
- 不使用现有演示实例改数据，不停止18080/18081等未知占用者，不复制用户库充当测试库。测试凭据只通过进程环境/已忽略本地文件传入，不归档Cookie、CSRF或登录请求体。

MySQL/PostgreSQL未有真实实例时仅声明SQLite验证，不能把编译通过写成多数据库已验收。

## 10. 最终结果

本轮旧config和static都已接到生效快照。旧config保留成功JSON字段并增加快照元数据，旧static复用版本ZIP格式。未生效404，错误使用真实HTTP状态；参数入口和编辑区预览鉴权保持现状。旧编辑区runtime服务/构包器已移除。

整合补齐：默认静态导出增加完整超时边界，与固定导出共用基于已选版本的计划构建；不重新解析active。正式测试覆盖构包暂停期间切换并删除旧版本，在途包仍完整导出旧快照、下一次导出跟随新版本，以及超时返回503。

旧测试夹具按新语义迁移：在明确的用例步骤锁版、发布、切换后读取runtime，再恢复编辑区可写；公共读取helper不偷偷发布。静态包按manifest中的资源身份和原始字节核对SHA，不再按原始URL文件名猜测。发布页、首页、根路径和恢复断言同步，保留编辑区源文件缺失对transfer的错误断言，发布快照不随源文件消失而损坏。

HTTP契约测试直接调用旧runtime handler，检查旧字段、精度、快照响应头、真实404/400及ZIP资源。恢复脚本改用真实登录会话与CSRF，旧config在快照资源缺失时同样503，补回文件后免重启恢复。

IDL的旧runtime响应仍由显式JSON适配层输出，未同步增加生成响应字段；线上字段与本PRD第4节一致。新增的生效时间字段在release IDL中生成，前端用真实生效时间展示，不能用加载时间冒充。

本次最新前后端的最终验证、提交与推送记录见 [main_v1整合结果](../main-v1-latest-integration.md)。不把早期阶段日志当最终证据，不另留“静态导出尚未完成”等过时结论。

边界：本地SQLite验证；MySQL/PostgreSQL无真实实例。旧静态ZIP布局与旧错误HTTP状态有意改变，不承诺依赖旧目录的解析器无需调整。未新增配置推送机制，应用已缓存配置仍由应用刷新。