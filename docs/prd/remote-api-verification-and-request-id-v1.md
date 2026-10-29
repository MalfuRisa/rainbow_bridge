# 远程接口摸底与请求编号兼容修复 PRD

日期：2026-10-29。状态：方案已编写，待虹桥研发执行；任何测试结果须以实施登记为准。

## 1. 目标与本次交付

用户要求：此前主要在本机验收，远程上来就出现创建版本错误。先在真实远程环境逐项核对接口是否可用，保护现有业务数据并清理测试数据，再确定修复范围。

本次实际派发的是测试与诊断实验：远程基线调查、全部接口清单与覆盖矩阵、可安全执行的真实 HTTP/浏览器验证、清理核对、已知问题的修复设计。允许编写测试代码、执行测试、修正测试自身问题；本次不修改产品业务代码、不替换远程前后端、不重启现有服务、不自动修复测试中新发现的产品问题。第7节是后续实现合同，测试完成后回填实际设计差异再决定实施。

架构师仅做只读核对与PRD；研发为用户明确指定的已有聊天“虹桥研发”：01a1333a-342c-7903-8ca8-164b82392399（local）。不向其他聊天或子代理转派。

## 2. 工作位置、环境和规则

- 仓库：D:/kylin/rainbow_bridge；编写时分支 main_v1，HEAD 05ff46baf7ffe83c945d7050e753eb7dd0beeaf5，产品工作区原先干净。开始前重新核对并保护后续用户改动，不采用旧独立工作区为入口。
- 应用入口：http://10.60.226.43:31880/rainbow-bridge/；API前缀 /rainbow-bridge/api/v1。用户要求直接利用远程组件，不在本地安装 PG/MinIO/Redis。
- 用户已确认 root 是服务器SSH账号，凭据仅通过任务交接提供、不写入本文件；SSH端口尚未核实。用户另已提供网页管理员登录信息并交接研发，网页账号与SSH账号分别使用，不尝试以SSH密码登录网页，不执行初始化、密码重置或修改认证配置。按正常登录获取独立测试会话，继续管理接口验证；凭据不得落入本文件或证据。
- 先读根 AGENTS.md；涉及浏览器/前端时读 vue/AGENTS.md。研发应使用 $engineering-quality-preserving-refactor，读取 C:/Users/MalfuRisa/.codex/skills/engineering-quality-preserving-refactor/SKILL.md。测试按请求客户端、造数清理、断言取证分工，复用小方法、提前返回，不引入Python和新大型框架。
- 参考 docs/docs/api/{index,management,runtime,release-compare,scope-identity,transfer-export,backup}.md、idl/biz/*.proto、实际路由/处理器、tests/e2e/。文档可能有旧语义（例如index中的历史版本阻止删范围说明），不能盲信，按具体实现与最新专项合同核对并记录差异。
- 禁止手工修改 biz/model/*.pb.go、biz/router/。本任务无须重新生成这些文件。

## 3. 先核实真实部署，不把配置当证据

记录远程版本接口、前端资源指纹、可见部署镜像/二进制标识，以及源码对应关系。若获准使用主机账号，只读定位实际服务与必要配置：数据库driver、存储type、Redis是否开启、快照目录是否持久/共享、后端副本数量。仅记录必要的非敏感值；不要整段输出env、Secret、配置或无关凭据，不假定SSH端口、namespace或Pod名称。

无法读部署层时明确未知，不阻止独立的应用接口测试。现有证据：入口与 ReleasesPage-Cf68zFbU.js 在架构师只读检查时HTTP 200，发布页产物存在4处 crypto.randomUUID()；这些指纹可能变化，执行时重取。

已知限制独立记录：本地release_service.go对MinIO源创建发布返回503 RELEASE_STORAGE_UNSUPPORTED；整实例备份只支持SQLite+local+Redis关闭等条件。连接三件套成功不等于已支持全部发布、备份或多Pod。

## 4. 保护业务与清理合同

### 4.1 原有数据不作为写入目标

1. 先登录独立测试会话，读取现有环境/渠道的身份、冻结revision、生效snapshot/version/revision，必要时记录配置元数据摘要，作为观察基线。证据脱敏，不批量导出真实业务内容或全实例资源。
2. 每轮生成唯一run_id，创建独立环境，例如 qa_20261029_<短随机串>；只在本轮新建的环境与渠道进行CRUD、冻结、发布、生效、导入和迁移。身份保存environment_id/scope_id，禁止仅按名称定位写入目标。
3. 维护创建清单：对象类型、实际返回ID、父范围身份、归属run_id、清理依赖。创建前把意图写入清单，响应不明时查询核对，不能盲重试制造额外对象。关键对象创建后立即登记；中断恢复使用清单核对归属。
4. HTTP客户端在每次管理写入前验证目标属于本轮清单；传入expected_environment_id/expected_scope_id、expected_edit_revision和相关revision。转移接口归档只含本轮造数，迁移源与目标也必须都属于本轮。禁止写default或用户截图的研发环境/农行范围。
5. 不通过全库恢复来清理；不操作现有锁、生效指针、资源、备份、密码；不执行压力测试、崩溃注入、共享Redis清库、清桶或删共享目录。

### 4.2 清理顺序与完成判据

- 测试成功、失败和中断均执行finally清理；开始下一轮前核对上轮清单。
- 按当前实现解除本轮冻结、清理本轮可清理的对象与范围。非生效版本删除只能作用于本轮；生效版本不要强删，按scope删除合同清active并逻辑删除范围。资源若无独立删除API，核查范围清理实现；不擅自直接删数据库或MinIO对象。
- 结束后用API复查测试环境/渠道/配置/资源不可见、旧scope pin不能误读新范围，本轮仍可访问的资源必须解释并登记。服务器物理文件只能在确认是本轮独占且不会被历史引用时，按已有正式清理机制处理，不能凭名称前缀盲删。
- 对比原有范围的身份、生效snapshot与冻结状态；若有外部用户并发操作，不能机械回滚基线，记录时间和证据，区分本轮请求的副作用。发现本轮影响原有业务立即停止写入并报告。
- 正常审计记录、逻辑删除记录、发布编号历史可能依法定产品行为保留，本任务不抹除。交付必须区分“可见业务测试数据已清理”“历史保留”“物理资源待清理”，不能宣称数据库零痕迹。无法保证清理的用例先不造数，记为blocked/未执行。
- 退出只注销本轮会话、清理本轮本地临时凭据与下载；不使其他用户会话失效。清理失败本次不得标记完整交付，提供精确对象清单与后续处理建议。

## 5. 逐接口覆盖矩阵与实验

先从IDL与实际注册路由生成 method+path 全量清单，包含健康检查、程序版本、所有管理和运行时入口，以及通配资源路径。核对手工注册入口与文档，不能只测自己熟悉的几个接口。

每条记录：方法/路径、部署是否存在、场景、预期合同、实际HTTP与业务code/关键字段、证据位置、状态、清理结果。状态至少区分通过、产品缺陷、部署能力限制、阻塞、未执行。HTTP 200不必然成功，503受限不能写成正向可用；“限制响应符合合同”与“功能可用”分别报告。

| 模块 | 安全范围内执行的主要场景 |
| --- | --- |
| 健康/版本/未知API | health与version、内网latest行为；未知API返回JSON 404/API_NOT_FOUND，不能回SPA页面 |
| 认证 | 登录、me、独立会话logout；匿名管理读/写、缺CSRF与错误Origin拒绝。避免反复输错密码触发全局限流；password只检查路由与合同，本实例不执行改密 |
| 环境/渠道 | 本轮create/list/detail/update/delete；身份不匹配、冻结禁止写、删除同名重建旧pin不串代；保留原有范围 |
| 配置 | 本轮create/list/detail/update/delete；alias不变、合法/非法JSON、大整数原文、两次保存中的过期edit_revision真实409；错误请求不改变数据 |
| 资源 | 本轮上传小PNG和小文本、list及匿名下载，核对字节/Content-Type/BasePath；asset://引用的真实存取；先确认清理路径再上传 |
| 冻结 | status/lock/unlock/history；备注可选、revision冲突、锁住后配置与资源写入受限；仅操作本轮scope |
| 发布 | create/list/detail/active/activate/delete/history/compare；自动编号、创建不自动生效、固定历史与编辑区独立、请求幂等与冲突、比较方向/active_revision；MinIO拒绝独立报告，不绕过能力限制 |
| 运行时 | overview，release默认active/fixed/raw，旧config/static，新静态包version+expected_snapshot_id，发布资源匿名读取；未发布404不回编辑区，旧scope pin拒绝，新旧版本内容/字节一致 |
| 转移 | export-preview、editor/active/version导出、export-tree、import-preview、import/import-selective、migrate；仅本轮源目标，导入不自动发布；无active对应受限不能伪造成功 |
| 备份 | capabilities优先；list/status/download仅在本轮可安全对象与配置允许时验证；BACKUP_UNSUPPORTED准确记录。create/delete/restore预览/execute/cancel按作用与能力逐项登记：涉及全实例内容、maintenance或恢复的，不在本实例执行 |

上述矩阵不要求在共享实例执行所有破坏性路径。整库恢复、密码修改、停机/重启、破坏性备份/迁移等必须列“未执行及原因”，不得把源码检查或Mock写成远程通过。后续确需此类正向验收时，另规划远程独立数据库、桶/前缀、Redis隔离与实例；本任务不自行搭建或修改现有部署。

主路径先跑：新范围→编辑配置/资源→冻结→创建候选→审阅→生效→匿名runtime与资源读取→历史版本读取→清理。若MinIO阻断发布，后续发布依赖项记blocked，继续其他安全模块；不得借用用户已有scope的版本来完成写入路径。

浏览器与HTTP分开：通过真实内网HTTP URL，在本轮scope复现创建候选错误。记录isSecureContext、typeof globalThis.crypto?.randomUUID、typeof globalThis.crypto?.getRandomValues，以及相关Network请求，确认冻结请求与创建请求是否分别发生。浏览器只做本轮业务操作，不用改变浏览器安全策略或模拟randomUUID来声称部署已修复。直接HTTP可以由测试客户端生成UUID用于验证后端，这只隔离前后端故障，不等于页面已通过。

## 6. 执行顺序与代码组织

1. 读规则、核对仓库/远程基线与凭据归属，生成接口清单和清理预案。
2. 公共接口/鉴权只读摸底；准备独立测试会话，确认测试对象可清理后造数。
3. 先复现HTTP页面request_id错误并取Network证据，再用真实HTTP测试各模块。结果按模块持续保存，单个失败不掩盖其他可独立执行项。
4. 有界执行，不做负载压测；下载大小/超时按现有合同设置，避免无界拉取。优先复用Node/Playwright或Go测试能力，不直接套用历史SQLite独立启动器。
5. 全程清理本轮对象，核对原有业务不变，汇总缺陷与下一步修复清单。

建议测试目录 tests/e2e/remote-api/，辅助脚本沿用仓库现有习惯。客户端负责URL/Cookie/CSRF，fixture负责身份/清单/清理，case负责业务断言，report负责脱敏矩阵；私有文件组织由研发决定。证据放 .local-qa/remote-api/<run_id>/（按仓库忽略规则，不入库），至少有coverage、issues、created-objects、cleanup、browser证据。不要把密码、Cookie、CSRF或Authorization写进脚本参数、仓库、PRD和报告。

## 7. 已知randomUUID问题的后续修复合同（本次只规划）

根因证据：vue/src/composables/useReleaseReview.ts:134/150直接生成request_id；prepare先确认冻结，再构造意图save，再execute调用create。HTTP内网IP不是localhost安全上下文，randomUUID不可用时抛TypeError，生成意图失败，create未发送。既有冻结不自动回退。

其他直接调用：useBackups.ts、CreateReleaseDialog.vue、ActivateReleaseDialog.vue、DeleteReleaseDialog.vue；实现前重新全量rg排查。

设计：在前端lib内新增单一职责的请求ID方法（如request-id.ts）；优先检测并调用globalThis.crypto.randomUUID，缺失则用getRandomValues生成16字节，设置UUID v4版本位和variant位，输出规范UUID。两个安全随机API均不可用时给出可理解错误，不用Math.random或时间戳伪造UUID。不引入全局crypto补丁、不降低Cookie/CSRF/Origin保护、不强制用户通过浏览器设置放行HTTP，也不借此改动部署HTTPS。

所有相关入口复用同一方法。创建操作如需冻结，应在本次新意图开始时先成功获得请求ID，避免编号生成失败还新增冻结副作用；同一意图落盘后的超时/重试/确认必须复用原request_id与原payload，新操作才生成新ID。不改变active_revision、expected_freeze_revision、scope身份、编号和冲突重新审阅语义。私有辅助方法保持简短，禁止在各组件复制fallback。

未来实现验收：HTTP内网浏览器无randomUUID但有getRandomValues可生成合法v4 UUID并发出请求；HTTPS/localhost原路径可用；两API都缺失时错误明确且不新增锁版/创建副作用；未确认请求重试ID不变，新操作ID不同；创建、生效、删除、备份调用方类型检查与构建通过。用真实HTTP浏览器验证修复，脚本Node中的crypto通过不能替代。远程MinIO后端503若仍存在，清楚报告前端已过/后端受限，不能写完整发布成功。

MinIO发布适配、PG/Redis并发缺陷、多Pod快照共享、三件套备份恢复不自动并入本次修复。测试新发现的产品问题先给最小复现、影响与优先级，回填本PRD，再由架构师与用户确定下一批。

## 8. 交付与实施登记

在本文件追加实施登记，最终总结放此处，不为每次失败新建报告。报告至少包含：远程真实版本/组件的已知与未知、接口总数及各状态统计、每条用例的证据、浏览器与后端故障区别、缺陷最小复现、测试数据清理与保留历史清单、原业务范围核对、实际测试命令与结果、修复建议和未执行项。

不得推送、打tag、发Release、替换远程产物、改共享基础设施或清库清桶。交付不等于所有接口可用；完成标准是覆盖清单完整、实际执行与未执行诚实区分、缺陷有证据、清理有复核、原业务数据无本轮修改。因凭据或能力受限时先回传已完成证据与具体阻塞，不编造测试通过。

### 派发登记

- 对象：虹桥研发（local / 01a1333a-342c-7903-8ca8-164b82392399）。
- 状态：2026-10-29 已通过 send_message_to_thread 发送，工具确认送达；开始与完成状态另按研发实际回传核实。本轮尚无研发执行结果。


## 9. 研发实施登记（2026-10-29）

后续任务（2026-10-29用户新授权）：测试报告保持原事实；前端修复见[内网HTTP请求编号兼容修复](remote-request-id-frontend-v1.md)，后端缓存修复及MinIO发布支持见[后端修复PRD](remote-release-cache-backend-v1.md)。分别已派给虹桥前端/虹桥研发；后续实现与验证登记在各自任务PRD，不把原报告改成“修复已通过”。

状态：本轮远程接口摸底、隔离测试、浏览器诊断、清理及报告已完成；不是“全部功能可用”。没有修改产品代码，没有提交推送、发布、替换服务、重启或改远程配置。

### 9.1 真实部署与证据位置

- 本地基线：main_v1 / 05ff46baf7ffe83c945d7050e753eb7dd0beeaf5。开始时仅本PRD未跟踪；本轮新增 tests/e2e/remote-api/ 测试脚本，产品文件未改。
- 远程 /api/v1/version 实际返回 v5.0.3、git_commit=c49194a066f448a0730e485fca18f7f60e6e0e0b、build_time=2026-10-28T10:23:08Z。远程提交在当前本地对象库不存在，不能声称和本地HEAD完全一致。
- SSH先在默认候选端口22取得OpenSSH握手，再使用已授权SSH账号只读查询。实际K8s namespace为 rainbow-bridge，deployment为 rainbow-bridge-app，副本1，镜像 ghcr.io/yi-nology/rainbow_bridge-app:v5.0.3，运行imageID为 sha256:9b47ea0a51952522ffb37e021ac1adf89ee94b3de2fd2afece3fec802a4b8cab。
- 只筛选实际配置中的非敏感字段确认：PostgreSQL、MinIO、Redis开启。未输出DSN、密码、Cookie、CSRF、Secret或完整环境变量。数据库驱动具体小版本、MinIO服务版本和PG实例参数未核实。
- 应用工作目录 /app；未设置 RAINBOW_RELEASE_DIR；默认快照目录 /app/data/release-snapshots 当前不存在；应用容器没有业务数据持久卷挂载，仅配置和系统serviceaccount挂载，/app/data位于容器overlay。此为发布支持后必须解决的部署风险，本轮没有创建成功快照，也没有用重启验证持久性。
- 前端实际加载 ReleasesPage-Cf68zFbU.js，HTTP200，SHA-256=98d0d71d58530892ddd90831192e7ff8d0f4e4d534a5761893d99acff35f52f4，含4处 crypto.randomUUID 调用。
- 最终公共摸底证据：.local-qa/remote-api/qa_20261029_cb57e760/。最终管理与诊断证据：.local-qa/remote-api/qa_20261029_17f0351f/，包括 coverage.json/md、summary-final.json、cases.json、issues.json、browser.json/png、transfer-readback.json、deployment.json、created-objects.json、cleanup.json、baseline-before/after.json、all-runs-cleanup.json。凭据只注入执行进程，未写入脚本或证据。

### 9.2 接口统计与执行边界

从IDL、实际注册代码及手工入口归一化得到 **60条业务/健康注册接口**，另有 **2条静态GET/HEAD通配入口**。初轮脚本把asset的IDL花括号路径和实际通配注册重复计为61，已修正归一化，最终60条，未发现真实缺失注册。

| 接口主状态 | 数量 | 含义 |
| --- | ---: | --- |
| 通过 | 37 | 本轮指定的安全业务或只读场景通过，仍以逐项证据为准 |
| 产品缺陷 | 1 | 配置普通列表在写入/导入后遗漏已存在内容；前端request_id错误另在下节登记，不重复算接口 |
| 部署能力限制 | 5 | release/create及4个backup读取入口实际受限；503限制响应符合合同，不表示正向功能可用 |
| 阻塞 | 10 | 版本详情/生效/删除/比较、发布依赖runtime及资源下载的正向路径没有本轮可用版本或可清理资源；负向验证仍有记录 |
| 未执行 | 7 | 改密、成功资源上传、5个备份/恢复写入口；有共享实例副作用或无正式物理清理路径 |

公共摸底执行61次安全HTTP观测（含未知API和随机不存在scope），47条管理读取/写入匿名请求均401/403。真实登录后最终管理脚本执行61条声明断言，通过61条；这包含“503限制响应正确”和“缺版本404正确”，不能据此写成61个功能成功。额外回读诊断发现1个列表缺陷，真实浏览器另确认request_id缺陷。静态首页GET和HEAD均200。

实际覆盖：环境/渠道CRUD、身份冲突与同名重建旧pin410；配置CRUD、不可变alias、有效JSON/非法JSON拒绝、大整数原文及过期edit_revision409；缺CSRF与错误Origin403；浏览器锁版和HTTP解锁、冻结写保护、过期revision409；editor导出/预览、归档预览、归档导入、选择性导入、JSON导入、迁移。导入计数分别断言2/2，JSON导入1，迁移返回total=2/succeeded=2；随后回读发现列表不一致，见D2。

runtime未发布返回404 RELEASE_NOT_ACTIVE，不回退编辑区；未知GET/POST API真实404 API_NOT_FOUND，Content-Type为JSON，不返回SPA。非法JSON仍有历史HTTP200+业务500的返回形式，本轮仅确认拒绝写入，不声称已统一错误协议。

### 9.3 缺陷、限制与最小复现

**D1 / P1：HTTP内网发布页面在冻结后调用randomUUID失败。**

在本轮新scope登录打开“版本发布→发布配置→锁版并创建候选版本”。浏览器 isSecureContext=false、typeof crypto.randomUUID=undefined、getRandomValues=function。Network明确记录 POST /freeze/lock ->200，之后没有 POST /release/create；页面显示 crypto.randomUUID is not a function，回读freeze/status为locked。错误被页面捕获，pageerror为空不代表页面成功。截图已人工查看。该冻结由finally解锁后删除本轮范围，未触碰其他范围。

**D2 / P1：范围缓存读取与写后失效键不一致，配置列表遗漏真实存在的配置。**

在本轮target渠道执行归档导入（total2）、JSON导入（total1）、migrate（total2/succeeded2）；普通GET config/list返回HTTP200/code200但无列表项，同scope的GET config/detail返回TITLE和正确scope_id，带type=text的列表返回total1及TITLE。最终两轮均复现，证据 transfer-readback.json。此证据证明列表与详情不一致，不证明数据库未写成功。

本地调用链定位：logic_config.go 的 ListConfigs读取 GenerateScopedConfigListPrefix(scopeID)，键前缀为 rainbow_bridge:config:list:scope:<scopeID>；config_cache.go 的 invalidateConfigCaches仍删除旧的 environment/pipeline前缀及旧详情键，未失效scope列表键。空列表被缓存后，创建/导入/迁移未能清掉旧缓存是强关联根因；本轮没有读/删真实Redis键，没有通过清Redis掩盖问题。远程提交与本地精确源码对应尚未确认，修复前应在对应远程源码进一步核对。建议下一批补scope缓存键统一失效及真实Redis回读验收，不在本轮修产品。

**L1 / 发布部署能力限制：MinIO下独立HTTP创建返回503。**

用本轮scope实际身份、当前冻结revision和测试客户端生成的合法UUID直接POST release/create，实际503/code503/RELEASE_STORAGE_UNSUPPORTED。此请求绕过的只是前端UUID生成，不是后端能力限制；不能据此称页面已成功或发布链可用。create拒绝后，本轮版本列表仍为空。后续activate/delete/compare验证缺版本404，正向自动编号、请求幂等重放、比较active_revision、版本切换、历史固定内容、静态包字节一致性均因无本轮版本阻塞，没有借用原有用户版本。

**L2 / 备份能力限制：** capabilities实际supported=false，reasons为database_driver_not_supported、storage_not_local、redis_enabled、backup_disabled。list/status/download/restore-preview读取真实503 BACKUP_UNSUPPORTED。create/delete/restore-preview上传/execute/cancel及改密未做正向执行，不试跑维护或整库恢复。

**R1 / P1部署风险：** 目前快照目录无持久/共享卷，仅1个应用副本；未来即使适配MinIO源发布，也必须明确快照落点和持久性，不能仅修UUID后宣称完整发布可用。本轮只读核实，不修改部署。

### 9.4 清理与业务保护

所有管理测试使用唯一qa_20261029_*环境，写入传真实environment_id/scope_id及对应edit/freeze revision；未对default环境、原有研发/农行等范围写入。每次结束finally解除本轮锁、删除本轮范围、注销本轮独立会话。前后基线包含4个原有scope的environment_id/scope_id、freeze state/revision、active snapshot/version/revision，逐轮一致。只比较必要元数据，没有批量导出真实业务配置；此结论限定于已观测元数据和本轮请求目标，不等于全数据库逐字节一致。

执行中为了修正测试字段、路由归一化与补充列表回读，共6轮隔离管理实验，所有轮次清理成功且前后原scope基线一致。第1轮archive字段误写为file，400属于测试自身错误；后续改用archive返回200/total2，不作为产品缺陷。所有轮次IDs与清理结论见all-runs-cleanup.json，各轮created-objects.json保留已取得ID。最终轮普通列表遗漏时，额外使用详情/type列表取得目标配置ID后登记。

成功上传小PNG/文本和资源匿名成功下载未执行：scope删除合同仅逻辑删资源，不即时删除物理文件；没有独立asset删除API和已确认的物理正式清理流程，按§4先不造物理数据。所有轮次均未上传业务资源、未创建成功版本；没有待清理物理资源或快照。导出归档只含本轮配置，以内存处理，不将真实业务归档保存到本地。

删除后本轮环境不在list可见；最终轮config/list、asset/list、release/list携旧scope均410 SCOPE_DELETED；同名target重建的新scope与旧scope不同，旧pin不读新代。原有业务元数据未见本轮影响。审计、freeze历史、范围与配置逻辑删除行按产品合同保留；不宣称数据库零痕迹，未直接删库、删桶或抹审计。SSH会话与本轮网页会话均已退出。

### 9.5 测试代码与后续修复设计

新增测试脚本：public-probe.mjs（路由/匿名/静态指纹/浏览器能力）、managed-probe.mjs（独立真实登录/造数身份/案例/回读/finally清理）、summarize.mjs（状态分级/完整矩阵）。均通过node --check。执行入口为node tests/e2e/remote-api/public-probe.mjs；管理脚本仅从RB_REMOTE_USER/RB_REMOTE_PASSWORD进程环境读取网页凭据，未放入仓库或命令行参数。RB_PUBLIC_RUN/RB_MANAGED_RUN选择证据轮次生成汇总。未调用旧SQLite启动器，也未在本地安装PG、MinIO或Redis。

第7节UUID修复设计经实测成立：统一request-id helper，randomUUID可用时优先，否则使用getRandomValues构造规范v4 UUID，两个API都不可用时明确报错；新意图先取得ID再冻结，同一意图重试保留原ID与payload。全量rg当前发现7处直接调用，分布在useReleaseReview（2）、useBackups（2）、Create/Activate/DeleteReleaseDialog（各1），后续一起替换。此轮未实现该设计，也未实现D2或MinIO发布适配。

### 9.6 全量注册接口矩阵

以下证据路径均相对于 .local-qa/remote-api/。通过只指列明场景，限制、阻塞和未执行均保留真实含义。

| 方法 | 注册路径 | 结论 | 本轮实际与未执行边界 | 证据 |
| --- | --- | --- | --- | --- |
| GET | `/api/v1/asset/file/*filepath` | 阻塞 | 未造物理资源/快照，仅验证不存在对象错误；匿名成功下载未测。  | qa_20261029_cb57e760/http-observations.json |
| GET | `/api/v1/asset/list` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 owned scope read:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/asset/upload` | 未执行 | 资源物理文件无正式即时删除路径；不上传以避免遗留。  | qa_20261029_cb57e760/http-observations.json |
| POST | `/api/v1/auth/login` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 real browser login:HTTP200/code-/- | qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/auth/logout` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 own session logout:HTTP200/code-/- | qa_20261029_17f0351f/cases.json |
| GET | `/api/v1/auth/me` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 session me:HTTP200/code0/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/auth/password` | 未执行 | 改密或全实例备份/恢复可能影响共享实例，不执行正向写入。  |  |
| GET | `/api/v1/backup/capabilities` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 backup capabilities:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/backup/create` | 未执行 | 改密或全实例备份/恢复可能影响共享实例，不执行正向写入。  | qa_20261029_cb57e760/http-observations.json |
| POST | `/api/v1/backup/delete` | 未执行 | 改密或全实例备份/恢复可能影响共享实例，不执行正向写入。  | qa_20261029_cb57e760/http-observations.json |
| GET | `/api/v1/backup/download` | 部署能力限制 | 真实503 BACKUP_UNSUPPORTED；限制响应符合合同，功能不可用。 safe backup read / absent test identifier:HTTP503/code503/BACKUP_UNSUPPORTED | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| GET | `/api/v1/backup/list` | 部署能力限制 | 真实503 BACKUP_UNSUPPORTED；限制响应符合合同，功能不可用。 safe backup read / absent test identifier:HTTP503/code503/BACKUP_UNSUPPORTED | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/backup/restore/cancel` | 未执行 | 改密或全实例备份/恢复可能影响共享实例，不执行正向写入。  | qa_20261029_cb57e760/http-observations.json |
| POST | `/api/v1/backup/restore/execute` | 未执行 | 改密或全实例备份/恢复可能影响共享实例，不执行正向写入。  | qa_20261029_cb57e760/http-observations.json |
| GET | `/api/v1/backup/restore/preview` | 部署能力限制 | 真实503 BACKUP_UNSUPPORTED；限制响应符合合同，功能不可用。 safe backup read / absent test identifier:HTTP503/code503/BACKUP_UNSUPPORTED | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/backup/restore/preview` | 未执行 | 改密或全实例备份/恢复可能影响共享实例，不执行正向写入。  | qa_20261029_cb57e760/http-observations.json |
| GET | `/api/v1/backup/status` | 部署能力限制 | 真实503 BACKUP_UNSUPPORTED；限制响应符合合同，功能不可用。 safe backup read / absent test identifier:HTTP503/code503/BACKUP_UNSUPPORTED | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/config/create` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 create owned configuration:HTTP200/code200/-；JSON validation true:HTTP200/code200/-；JSON validation false:HTTP200/code500/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/config/delete` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 delete owned config:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| GET | `/api/v1/config/detail` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 fresh config detail:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| GET | `/api/v1/config/list` | 产品缺陷 | 导入后普通列表仍为空，而相同scope详情和type=text列表能读取；疑似scope缓存失效遗漏。 config list:HTTP200/code200/-；transfer target contents reconciled:HTTP200/code200/-；transfer target contents reconciled:HTTP200/code200/-；old scope pin rejected after recreation:HTTP410/code410/SCOPE_DELETED | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json、qa_20261029_17f0351f/transfer-readback.json |
| POST | `/api/v1/config/update` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 missing CSRF rejected:HTTP403/code403/AUTH_CSRF_INVALID；wrong Origin rejected:HTTP403/code403/AUTH_ORIGIN_DENIED；update config, alias immutable:HTTP200/code200/-；stale edit rejected:HTTP409/code409/CONFIG_EDIT_CONFLICT；frozen config update rejected:HTTP409/code409/SCOPE_FROZEN | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/environment/create` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 create owned environment:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/environment/delete` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 cleanup owned environment:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| GET | `/api/v1/environment/detail` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 owned scope read:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| GET | `/api/v1/environment/list` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 owned scope read:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/environment/update` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 update owned environment:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| GET | `/api/v1/freeze/history` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 owned frozen scope read:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/freeze/lock` | 通过 | 真实浏览器POST冻结200；后端冻结状态locked已复查。  | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/browser.json |
| GET | `/api/v1/freeze/status` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 freeze status after browser failure:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/freeze/unlock` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 unlock stale revision rejected:HTTP409/code409/FREEZE_REVISION_CONFLICT；unlock owned scope:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/pipeline/create` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 create owned target channel:HTTP200/code200/-；recreate same own channel key:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/pipeline/delete` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 delete own target channel:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| GET | `/api/v1/pipeline/detail` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 owned scope read:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| GET | `/api/v1/pipeline/list` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 owned scope read:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/pipeline/update` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 update owned channel:HTTP200/code200/-；identity mismatch rejects:HTTP409/code409/SCOPE_IDENTITY_CONFLICT | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/release/activate` | 阻塞 | 无本轮发布版本；负向缺版本/无active检查通过，正向发布依赖受MinIO阻断。 no-version negative path:HTTP404/code404/RELEASE_VERSION_NOT_FOUND | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| GET | `/api/v1/release/active` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 owned frozen scope read:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| GET | `/api/v1/release/asset/*filepath` | 阻塞 | 未造物理资源/快照，仅验证不存在对象错误；匿名成功下载未测。  | qa_20261029_cb57e760/http-observations.json |
| POST | `/api/v1/release/compare` | 阻塞 | 无本轮发布版本；负向缺版本/无active检查通过，正向发布依赖受MinIO阻断。 no-version negative path:HTTP404/code404/RELEASE_VERSION_NOT_FOUND | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/release/create` | 部署能力限制 | 真实503 RELEASE_STORAGE_UNSUPPORTED；另有浏览器randomUUID缺陷。 independent HTTP release create:HTTP503/code503/RELEASE_STORAGE_UNSUPPORTED | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/release/delete` | 阻塞 | 无本轮发布版本；负向缺版本/无active检查通过，正向发布依赖受MinIO阻断。 no-version negative path:HTTP404/code404/RELEASE_VERSION_NOT_FOUND | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| GET | `/api/v1/release/detail` | 阻塞 | 无本轮发布版本；负向缺版本/无active检查通过，正向发布依赖受MinIO阻断。 owned frozen scope read:HTTP400/code400/RELEASE_INVALID_REQUEST | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| GET | `/api/v1/release/history` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 owned frozen scope read:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| GET | `/api/v1/release/list` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 owned frozen scope read:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| GET | `/api/v1/runtime/config` | 阻塞 | 无本轮发布版本；负向缺版本/无active检查通过，正向发布依赖受MinIO阻断。 owned scope without active:HTTP404/code404/RELEASE_NOT_ACTIVE | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| GET | `/api/v1/runtime/overview` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。  | qa_20261029_cb57e760/http-observations.json |
| GET | `/api/v1/runtime/release` | 阻塞 | 无本轮发布版本；负向缺版本/无active检查通过，正向发布依赖受MinIO阻断。 owned scope without active:HTTP404/code404/RELEASE_NOT_ACTIVE | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| GET | `/api/v1/runtime/release/static` | 阻塞 | 无本轮发布版本；负向缺版本/无active检查通过，正向发布依赖受MinIO阻断。 no published package:HTTP400/code400/RELEASE_INVALID_REQUEST | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| GET | `/api/v1/runtime/static` | 阻塞 | 无本轮发布版本；负向缺版本/无active检查通过，正向发布依赖受MinIO阻断。 no published package:HTTP404/code404/RELEASE_NOT_ACTIVE | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/transfer/export` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 editor export isolated objects only:HTTP200/code-/-；export no published source active:HTTP404/code404/RELEASE_NOT_ACTIVE；export no published source version:HTTP404/code404/RELEASE_VERSION_NOT_FOUND | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/transfer/export-preview` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 editor export preview:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| GET | `/api/v1/transfer/export-tree` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 export tree metadata:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/transfer/import` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 archive import only own target:HTTP200/code200/-；JSON import owned target:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/transfer/import-preview` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 import preview only own archive:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/transfer/import-selective` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 selective import only own original scope:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| POST | `/api/v1/transfer/migrate` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。 migrate only owned source and target:HTTP200/code200/- | qa_20261029_cb57e760/http-observations.json、qa_20261029_17f0351f/cases.json |
| GET | `/api/v1/version` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。  | qa_20261029_cb57e760/http-observations.json |
| GET | `/api/v1/version/latest` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。  | qa_20261029_cb57e760/http-observations.json |
| GET | `/ping` | 通过 | 本轮指定安全场景通过；不表示所有边界全覆盖。  | qa_20261029_cb57e760/http-observations.json |

静态补充：GET /*filepath、HEAD /*filepath（main.go），本轮首页均HTTP200/text-html；只验证首页加载，不枚举用户资源。
