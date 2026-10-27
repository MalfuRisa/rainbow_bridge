# 业务实时接口验证 PRD（ZCode 执行）

日期：2026-10-26。状态：测试方案，尚未执行；不代表接口已通过本轮验收。

## 1. 用大白话说明本次任务

模拟两个独立业务网站 A、B，证明它们无需进入虹桥管理页面，就能通过接口取到各自正确的生效配置和图片；切换版本后默认请求读取新版本，指定旧版仍读取旧内容，失败时不会偷偷返回编辑区或别的业务数据。

本次主要验证**运行时读取与资源交付**，不是导入导出功能测试，也不是前端管理 UI 验收。Postman 可作为人工复现入口，真实 HTTP 自动化是可重复执行的依据；两者验证同一个合同。

交付终点：完成测试脚本、真实执行、证据及简明结果；发现产品缺陷时提供最小复现，不借本任务自动修改业务行为、接口设计或前端。

## 2. 执行边界和现有事实

仓库 `D:/kylin/rainbow_bridge`，编写时 `main_v1`、已提交 HEAD `c9aefe5`。工作区另有自动编号、任意版本比较、备份及前端改动，测试必须构建实际当前依赖，不能从旧 HEAD 构建后声称测试了新代码。

ZCode 先读取 AGENTS.md，以及本机可用的 `$engineering-quality-preserving-refactor`：`C:/Users/MalfuRisa/.codex/skills/engineering-quality-preserving-refactor/SKILL.md`。依此组织测试代码：实例管理、管理造数、匿名业务请求、断言和取证分职责，不能写成一个巨型脚本；不引入 Python 运行时。

参考合同：[业务运行时 API](../docs/api/runtime.md)、[范围身份](../docs/api/scope-identity.md)、[管理接口](../docs/api/management.md)、[最新自动编号 PRD](release-numbering-compare-retention-backend-v1.md)。文档和实现不一致时记录具体差异，不能仅按返回值修改预期，使测试变绿。

特别注意：现有 `tests/e2e/local/support/runtime.ts` 仍包含手填版本、旧鉴权/身份假设，不能不检查就复用。新建版本必须不提交 version，保存返回的实际 `meta.version/snapshot_id`；所有相关管理写入携带当前 expected_scope_id/expected_environment_id，编辑更新还保留 expected_edit_revision。

本轮允许新增/修改测试、夹具和必要的测试使用说明；允许修正自己的测试脚本错误。产品源码只读，确认的产品缺陷留待用户取舍。不得提交/推送、部署、替换现有二进制、停止用户进程或执行用户备份恢复。

## 3. 隔离环境和执行方式

### 3.1 实例

- 独立 SQLite、local 资源、Redis 关闭、备份关闭；关闭与本轮无关的外网请求，不依赖 Google Fonts/版本检查等外部服务。
- 自建临时工作目录，如 `.local-qa/runtime-business/<run_id>/instance`；数据库、上传、快照与日志全部位于其下。记录实际路径但不把用户真实配置复制进去。
- 首选端口 18140，浏览器业务演示源用 18142/18143，根路径补测可用 18141；启动前确认空闲，占用则选择同段其他空闲端口或清楚报阻塞，不能杀占用进程。
- 主套件 `base_path=/rainbow-bridge`；根路径另起独立实例、独立库补跑关键场景。URL helper 在唯一位置拼部署前缀，请求函数接受无前缀逻辑路径；返回的资源 URL 使用 URL 解析，不再次拼 BasePath。
- 后端 dev 构建即可，不依赖当前管理 UI 构建或前端窗口完工。测试业务页面由独立本地静态服务提供。
- 为本轮二进制设置可识别的构建标识，记录 /version 实际响应、源码 HEAD、dirty 状态及所构建相关改动摘要。版本接口只能证明实例身份，不能代替业务合同检查。
- 进程必须记录 PID、可执行绝对路径、启动时间；finally 只停止本轮拥有且身份仍匹配的进程。Windows 后台进程使用隐藏窗口，所有移动/删除均验证绝对路径位于本轮目录。

### 3.2 凭据与客户端分离

启动脚本为临时实例生成随机演示管理员，仅通过子进程环境传入：实例 `RAINBOW_ADMIN_USERNAME/RAINBOW_ADMIN_INITIAL_PASSWORD`，测试造数进程 `RB_QA_ADMIN_USERNAME/RB_QA_ADMIN_PASSWORD`。不把值写进 YAML、命令参数、证据、Postman 环境文件或聊天。

准备数据的管理员客户端走真实登录 Cookie + Origin + CSRF。业务 A、B 各自使用**全新无 Cookie jar 的匿名客户端**：不携带管理员 Cookie、Authorization 或 CSRF，不复用管理请求 helper 自动登录能力。浏览器 fetch 明确 `credentials:'omit'`。

当前 runtime 与发布资源下载按公开读取设计，这是本轮合同；环境/渠道 Header 用于选数据，不是访问权限。B 客户端主动提交 A 的范围头时，按当前设计会读 A，不能把它编造成权限漏洞。本轮的“隔离”指服务准确响应所指定范围，不串读缓存或数据。

### 3.3 推荐交付入口

建议 `script/test-runtime-business.ps1` + `tests/e2e/runtime-business/`，复用现有 Playwright/Node 依赖，用真实 HTTP；浏览器项用 Playwright Chromium。具体私有文件拆分由执行者决定，不强制新框架。

命令提供一个清晰全量入口，失败非零退出，环境准备失败记 blocked，绝不跳过后打印全绿。测试资源/结果目录不入库；复现脚本、用例和 Postman collection 可审查入库，但本任务不替用户 commit。

## 4. 接口与参数位置

| 入口 | 请求参数 | 核心断言 |
| --- | --- | --- |
| GET runtime/release | Header x-environment/x-pipeline；可选 query version、content_format、scope_id | 默认 active，指定 version 为 fixed；内容、范围、版本与资源一致 |
| GET runtime/config | 同样的范围 Header；不支持 version/raw 等选择参数 | 跟随 active，不读取编辑区；与新接口按共同字段一致 |
| GET release/asset/{snapshot_id}/{asset_id} | 使用运行时内容实际返回的 URL | 无需业务范围 Header/管理员凭据，可读正确字节和 Content-Type |
| GET runtime/overview | 无范围参数 | 返回环境/渠道及身份，供接入选择，不当作生效配置接口 |
| GET runtime/release/static | 范围 Header + version/expected_snapshot_id | 辅助验证固定业务静态包，不能冒充 transfer 迁移包 |
| GET runtime/static | query environment_key/pipeline_key | 辅助验证本次 active 静态包，不接受版本选择参数 |

所有路径示例都在 `/api/v1/` 下，实际请求需带实例 BasePath。主要 GET 不发送 JSON body。

```http
GET {{api_root}}/api/v1/runtime/release
x-environment: {{environment_a}}
x-pipeline: default
```

```http
GET {{api_root}}/api/v1/runtime/release?version={{version_a1}}&content_format=raw
x-environment: {{environment_a}}
x-pipeline: default
```

`api_root` 为含部署前缀的绝对地址、不含尾斜杠，例如 `http://127.0.0.1:18140/rainbow-bridge`。图片取自本次响应的 banner 配置，不把上述 URL 当作图片 URL。

JSON 响应检查 HTTP 状态、Content-Type、code/data、实际内容和 Cache-Control。按端点自身合同检查响应头，不能要求 runtime/release 必须拥有它没有声明的 X-Rainbow-* 头；旧 config 与静态包已有版本头时须与正文/清单一致。payload_sha256 是内部快照 payload 摘要，不是完整 HTTP 响应的 SHA。

## 5. 可辨认的测试数据与预期来源

使用本轮随机后缀生成环境名，避免重跑撞历史数据。业务映射：

| 范围 | 用途 | 同名配置 |
| --- | --- | --- |
| qa_biz_a_<run>/default | 业务 A | greeting、banner 等 |
| qa_biz_b_<run>/default | 业务 B | 故意同名 greeting、banner，内容不同 |
| qa_biz_a_<run>/beta | A 的另一个渠道 | 同名配置，第三套内容 |
| qa_empty_<run>/default | 空范围/无 active 边界 | 先无 active，再创建空快照 |

业务 A 的阶段数据：

| 状态 | 文本 marker | 图片 | 是否可被默认 runtime 读取 |
| --- | --- | --- | --- |
| A 第一个快照 A1 | A_RELEASE_ONE | 有效 red.png，固定 SHA_A1 | 激活后可读 |
| A 第二个快照 A2 | A_RELEASE_TWO | 有效 blue.png，固定 SHA_A2 | 仅创建未激活时不可成为默认；指定版本可读 |
| A 后续编辑区草稿 A3 | A_EDITOR_ONLY | 第三张有效图片，SHA_A3 | 不发布就不可从 runtime 读到 |
| B 第一个快照 B1 | B_RELEASE_ONE | 第四张有效图片，SHA_B1 | 仅 B 范围可作为正确默认结果 |
| beta 第一个快照 | A_BETA_ONE | 第五张图片 | 仅 beta 范围 |

图片必须是可解码的小 PNG，不能把文本伪装 .png；可用本机 Go image/png 或已有可信夹具生成不同像素。另放一个确定字节的 .txt 文件供 file 配置验证。准备时先计算本地文件 SHA256、长度和 MIME，再上传并核对返回元数据。

至少覆盖现有 11 类型：text、textarea、number、decimal、boolean、color、object、keyvalue、image、file、richtext。A1/A2 每项有独立预期值；结构化和富文本含受管引用。object/keyvalue 包含数字 `9007199254740993` 和 `9007199254740992` 的对照，number/decimal/boolean 类型按当前接口仍返回字符串的合同检查。

预期由本地固定夹具及造数动作建立，不从被测 runtime 自己取一次再当正确答案。管理 create 响应仅用于记录真实 version/snapshot/scope；不要假设环境已有 v1 或在客户端计算下一版本。

## 6. 阶段编排

1. 构建/启动/确认身份 → 管理员登录 → 创建范围并收集 environment_id/scope_id → 上传资源和配置。
2. 先对未设置 active 的 A/空范围读，验证不回退编辑区。
3. 锁 A → create 不传 version 得到 A1 → 激活；建立 B1/beta 并各自激活。
4. 解锁 A 修改为 A2 → 锁版/create 得到 A2但不激活 → 解锁编辑成 A3，形成“生效 A1、未生效 A2、草稿 A3”同时存在的关键状态。
5. 匿名客户端执行默认/指定/范围/类型/图片读取。
6. active compare 确认后切 A2 → 验证默认 A2、固定 A1不变 → 回退 A1再验证；并行读取时重复 A1/A2切换。
7. 停止本轮实例并重启，同库读取验证持久化；匿名业务读取不需要重新管理员登录。需要后续管理动作时重新登录获取当前会话。
8. 分阶段做资源故障、版本删除、scope 删除重建等破坏性边界；只操作本轮临时数据，并恢复被移动/改写的夹具文件。
9. 根路径补测、Postman/小业务浏览器演示 → 汇总证据 → 清理本轮进程。

各阶段显式预置状态，不依赖用例偶然执行顺序；同一 scope 的写入步骤顺序执行，不让测试互相污染 active。

## 7. 必须执行的验证清单

### 7.1 业务取数主链路

| ID | 输入/操作 | 硬性预期 |
| --- | --- | --- |
| RT01 | A/B 匿名请求默认 release | HTTP200，selection_mode=active；各自 scope/version/snapshot/marker 与夹具一致，无对方配置/资源 |
| RT02 | A1 active、A2未生效、A3草稿 | 默认只读 A1；固定 A2 读 A2；任何 runtime 不漏 A3 |
| RT03 | 指定 A1 固定读取，随后切 A2再回退 | fixed 始终 A1；fixed 省略 active_revision/activated_at；active 返回与指针匹配的 revision/时间 |
| RT04 | 成功切 A2 后再发新默认请求 | 新请求读 A2；回退成功后新请求读 A1，不靠重启；已在途旧请求允许返回完整旧版 |
| RT05 | A/default、B/default、A/beta 交错请求各至少20次 | 同 alias/版本名不造成缓存串范围；按本次 Headers 精确匹配预期 |
| RT06 | 默认无version vs ?version= | 前者 active；后者400，不能静默当默认 |
| RT07 | 各11类型，默认 json 和 raw | 字符串/JSON形状正确；raw content 为字符串；零/false配置内容不丢失 |
| RT08 | 大整数对照 | 原始 HTTP 文本保留两数；raw 的 content 原文保真。普通 JS JSON.parse 导致的数值舍入单独标为客户端限制，不误判服务端 |
| RT09 | 图片、文件、富文本/JSON受管引用 | 资源 URL 属于对应 snapshot，均可匿名下载，字节 SHA/长度/MIME匹配原始夹具；无编辑区路由泄漏 |
| RT10 | 从 runtime 返回 URL 新建请求，不附任何 Cookie/范围头 | PNG能解码、file字节正确；不能把HTML/错误JSON/仅URL存在当作图片通过 |
| RT11 | 旧 runtime/config 与新默认 release | scope、version、snapshot和共同配置内容一致；旧端点不读 A3；不要求它承诺未声明字段 |
| RT12 | 未设置 active；随后激活空快照 | 未设置时404 RELEASE_NOT_ACTIVE，不返回编辑区；空快照生效后200且configs:[] |
| RT13 | 单独 runtime/overview | 有正确范围/身份列表；不误把 overview200当配置交付成功 |

### 7.2 参数、公开读取与资源异常

| ID | 输入/操作 | 硬性预期 |
| --- | --- | --- |
| RT14 | 缺一个/两个范围头、空白范围头 | 400实际HTTP+JSON错误；不能偷偷默认default |
| RT15 | 只在GET JSON body或不受支持query写环境/渠道，Header缺失 | 不当作有效范围头，400；同名不同大小写的HTTP头正常等价 |
| RT16 | 不存在环境/渠道、固定版本、资源ID | 404及对应业务错误；不返回另一个范围或200 SPA HTML |
| RT17 | content_format=json/raw/空/非法 | 前两者成功；空和非法400；旧config显式带version/content_format/expected_snapshot_id按合同400 |
| RT18 | 无Cookie的runtime对照无Cookie管理列表/写请求 | runtime200，管理401/403，无副作用；业务读取不依赖管理员登录，也不因此开放管理接口 |
| RT19 | 资源故障：只移动临时快照文件、再放回 | 相关runtime/下载明确503而非回退编辑区；不相关B仍可读；放回后免重启恢复读取 |
| RT20 | 资源同长度改字节，再恢复原文件 | 下载SHA核验503；runtime元数据只校验存在/长度，允许它仍200，不能因此误报；恢复后下载字节正确 |
| RT21 | 非生效A1逻辑删除 | 新fixed读取和旧A1资源URL均410；active其他版本保持正常；已下载本地文件不要求远程撤回 |
| RT22 | 本轮scope解锁→删除→同key重建 | 新scope为空且ID不同；旧pin/旧snapshot/file新请求410/合同身份冲突，不能映射新代 |
| RT23 | scope_id可选固定身份，各实际声明支持的入口 | 正确pin成功，非法/旧/错配身份按合同拒绝；逐个核对旧config/static声明与实现，若忽略pin按实际缺陷记录，不能用不传pin掩盖 |
| RT24 | 未知API GET | 实际404 JSON，不能因前缀双拼/SPA兜底读到200HTML后当成业务成功 |

RT16/RT23 的具体 scope error_type 需按当前明确合同核对（SCOPE_* 或 RELEASE_SCOPE_NOT_FOUND）；若文档承诺与实际错误分类不一致，另记兼容缺陷，不通过随意放宽所有4xx断言消除失败。缺陷证据保留原HTTP状态和error_type。

资源 URL 安全处理：绝对 URL 原样解析，根相对 URL 只补 origin，不能再补 BasePath；相对 URL 按标准 URL resolve。获取前核对 origin 是本轮服务，禁止为测试自动访问外部超链接。query/fragment 的保留与HTTP实际发送的fragment不在请求中应分别检查。

### 7.3 并发、重启和 BasePath

| ID | 操作 | 硬性预期 |
| --- | --- | --- |
| RT25 | 5个匿名读客户端各40次；同时管理员顺序A1/A2切换10次 | 每个成功响应只允许完整A1或A2，version/snapshot/内容/图片路径一致；不要求在途响应全部是最新，不把临时请求失败藏掉 |
| RT26 | 本轮实例重启后不登录直接读 | active/fixed、资源字节保持；重启前后版本/快照和范围身份正确 |
| RT27 | 独立根路径实例（无/rainbow-bridge前缀） | 默认/固定、图片、旧config、缺头/未知路由五条关键链路均正确；日志明确实际URL，无双前缀 |

这是有界并发正确性测试，不宣称性能压测、K8s双Pod或高可用验收。记录RT25请求总量、状态分布与耗时，不拿“无报错日志”替代内容检查。

### 7.4 浏览器业务调用与静态包辅助检查

| ID | 操作 | 硬性预期 |
| --- | --- | --- |
| RT28 | 独立本地业务页面A/B，跨源fetch runtime带x-environment/x-pipeline，credentials:omit | Chromium真实预检/GET可用，页面显示对应marker；不能仅Postman成功就宣称浏览器可接入 |
| RT29 | 页面将banner实际URL赋给普通img.src | 不增加管理员或范围头也可加载，naturalWidth/Height>0；截图能看出A/B图片不同 |
| RT30 | 固定静态包及旧active静态包各一次 | ZIP MIME/结构正确，manifest和config.json范围/版本一致，包内相对资源字节匹配；固定snapshot不符409，不把JSON错误保存ZIP |

跨源业务页只做极简展示夹具（范围、实际版本、文本和图片），不改虹桥Vue或设计一套新产品UI。RT28 的实际预检须允许GET和两个自定义头；公开无凭据来源配置与管理可信来源分开。先使用明确支持该调用的测试CORS配置，记录它；若缺省配置另有表现，可作为额外观察，不擅自开放演示实例来源。

静态包检查只是运行时交付的辅助一致性证明，不扩展到 transfer导入、全量备份/恢复或全部ZIP安全专项。

## 8. Postman 与导师演示材料

交付可导入的 `runtime-business.postman_collection.json` 和无秘密的环境模板。至少包含：

1. 业务A默认配置、A指定旧版、A raw模式、业务B默认配置。
2. A/beta渠道读取。
3. 用上一配置响应取到的图片绝对URL下载（不能自动给URL叠前缀）。
4. 旧config对照、缺Header错误、无active错误。

环境变量包含 api_root、environment_a、environment_b、pipeline_a、version_a1/2、snapshot_a1/2、image_url等非秘密测试信息。默认Auth为No Auth，collection脚本也不能悄悄附Cookie。Postman本身可能有Cookie jar，演示步骤明确清空/禁用jar，以证明匿名读取；collection JSON不能携带管理员凭据。

A1/A2变量必须由真实造数结果填入；若运行结束已停止隔离实例，模板注明需要重跑测试或使用显式演示保留模式，不能给导师一个声称可用但已停机的地址。可选 -KeepRunning 用于人工Postman演示，打印停止方法和owned进程信息，不能默认遗留后台进程。

给出3分钟演示顺序：读A与B → A指定旧版 → 管理动作切A版本 → 再发A默认请求看变化 → 查看图片。切版由独立管理员步骤完成，业务collection不混入管理员鉴权。

不强制安装Postman/Newman或调用外网服务。自动化真实HTTP执行是本轮主要证据；collection可验证JSON格式并人工按说明使用，未真正运行Postman则诚实标注“已提供，未在Postman执行”。

## 9. 取证与问题处理

每次运行保留 `summary.json`、用例结果、请求/响应证据、原始资源SHA清单、造数所得scope/version/snapshot映射、实例日志及业务截图。截图只作为辅助，不能替代字节和JSON断言。

证据中不保存管理员密码、登录响应的会话/CSRF、Set-Cookie/Cookie/Authorization；管理请求需脱敏，原始响应与trace写出前先检查。runtime夹具内容是专用非敏感数据，可以保留。

每个RT项只记 pass/fail/blocked/not_run，附阶段、请求方法/URL/非秘密Headers、预期、实际HTTP/JSON或字节摘要、证据路径。矩阵项可有多个子断言，分别报告“场景项”和“实际请求次数”，不能混用计数。

问题分三类：

- 夹具错误（旧手填version、缺scope、错误Cookie携带、双前缀、客户端大整数舍入等）：修测试并重跑受影响项，保留必要误判说明，不报产品漏洞。
- 产品缺陷：保留脱离运行器的最小重放步骤和实际影响，不改预期、不自动修源码；其他独立项继续完成。
- 环境阻塞：缺工具/端口/实际新合同，说明缺什么；不能以blocked项缺失却宣布全验收通过。

失败样例尽量用本轮固定夹具，不复制或打包用户真实库、业务备份或本机凭据。构建/依赖必要变动单独说明，不顺手更新go.mod/锁文件或生成文件。

## 10. 最终交付与结论格式

1. 一条可重跑命令及前置依赖、隔离路径、端口、清理/KeepRunning说明。
2. 已执行的RT01–RT30矩阵与总结果、真实请求次数，明确未执行/环境限制。
3. Postman collection、环境模板、3分钟演示说明，A/B业务截图。
4. 若有缺陷，按影响排序，说明“业务会拿错什么/拿不到什么”，给最小复现及建议归属；不代替用户取舍修复。
5. 大白话结论必须分别回答：默认生效读取是否正确、指定版本是否固定、图片能否真实获取、A/B有无串范围、切版无需重启是否成立、浏览器是否可用、重启/并发/根路径是否通过。

本轮证据统一在仓库隔离产物目录，实施与结果追加本PRD；不为每次调试另写桌面报告。只有全部必测项通过才称“本轮单实例运行时接口验证通过”；仍不能表述为K8s、多数据库、热恢复或权限隔离均已验收。

当前交付只是测试PRD，由用户安排ZCode执行；本聊天未启动测试、未派发或修改业务代码。

## 11. ZCode 执行结果（2026-10-26）

### 11.1 结论

本轮实际执行了 RT01–RT30，共 **30/30 已执行，28 项通过、2 项失败、0 项 blocked**。失败项均为产品缺陷，已保留证据和最小复现，未修改业务代码。

因此**不能表述为“本轮单实例运行时接口验证通过”**。主链路（A/B 默认读取、固定版本、图片字节、切版/回退、重启、并发、根路径、浏览器、静态包）通过；两个缺陷分别影响旧接口的 scope 固定语义和未知 API 的 JSON 错误合同。

- 运行命令：`powershell -NoProfile -ExecutionPolicy Bypass -File script/test-runtime-business.ps1`
- 实际运行目录：`D:/kylin/rainbow_bridge/.local-qa/runtime-business/20261026-154450-71530d`
- 源码 HEAD：`c9aefe52adfa8a8ee7b6f76be57885cf3eed176b`；构建时工作区有 215 个 changed paths，构建标识 `qa-runtime-business-20261026-154450-71530d`
- 端口：18140（`/rainbow-bridge`）、18141（根路径）、18142/18143（A/B 业务页）
- Node/Playwright 控制的 HTTP 请求：**564**；Chromium 另实际发起 2 次页面加载、2 次预检、2 次 runtime GET、4 次图片 GET
- 证据：`artifacts/summary.json`、`artifacts/evidence/RTxx.json`、`artifacts/requests.jsonl`、`artifacts/fixtures.json`、A/B 截图、实例日志

### 11.2 RT01–RT30 矩阵

| ID | 结果 | 请求数 | 实际检查 |
| --- | --- | --- | --- |
| RT01 | pass | 2 | A/B 默认 release 均 active，scope/version/snapshot/marker 与夹具一致，无对方 marker |
| RT02 | pass | 4 | A1 active、A2 未激活、A3 仅编辑区；默认只读 A1，fixed 可读 A2，任何 runtime 不漏 A3 |
| RT03 | pass | 10 | fixed A1 在切 A2 前后均固定；fixed 省略 active_revision/activated_at；active revision 前进 |
| RT04 | pass | 6 | 切 A2 后新默认读 A2，回退 A1 后新默认读 A1；同一进程 generation=1 |
| RT05 | pass | 60 | 20 轮 × A/default、B/default、A/beta 交错，无缓存串范围 |
| RT06 | pass | 2 | 省略 version 为 active；显式 `?version=` 为 400 |
| RT07 | pass | 2 | 11 类型 json/raw；text/textarea/number/decimal/boolean/color 保持字符串；object/keyvalue 形状正确，raw 保留大整数 |
| RT08 | pass | 2 | 默认与 raw 的原始 HTTP 文本均保留 `9007199254740993`/`9007199254740992` |
| RT09 | pass | 4 | 图片、文件、富文本、object/keyvalue 引用均属 A1 snapshot，匿名下载 SHA/长度/MIME 匹配；无编辑区路由泄漏 |
| RT10 | pass | 3 | 仅用 URL 新请求无 Cookie/范围头，PNG 8×8、文本字节精确 |
| RT11 | pass | 3 | 旧 config 与新 active 的 scope/version/snapshot/共同配置一致；响应版本头一致；overview 仅身份列表 |
| RT12 | pass | 1 | 未激活 A/空范围 404 RELEASE_NOT_ACTIVE，不读编辑区；空快照激活后 configs:[] |
| RT13 | pass | 1 | overview HTTP 200，返回范围/身份列表，不含 snapshot/configs |
| RT14 | pass | 5 | 缺一个/两个头、空白头均 400，不回落 default |
| RT15 | pass | 3 | 只放 GET body 或 query 不生效；大小写不同的范围头等价 |
| RT16 | pass | 3 | 不存在范围/版本/资源分别 404，未串范围 |
| RT17 | pass | 7 | json/raw 成功；空/非法 content_format 400；旧 config 的 version/content_format/expected_snapshot_id 均 400 |
| RT18 | pass | 5 | 匿名 runtime 200；匿名管理列表/写 401；写无副作用 |
| RT19 | pass | 6 | 移动 A1 资源文件后 runtime/下载 503 RELEASE_INCOMPLETE；B 仍 200；放回免重启恢复 |
| RT20 | pass | 4 | 同长度改字节：runtime 元数据 200，下载 503；恢复后字节 SHA 正确 |
| RT21 | pass | 6 | 删除非 active A2 后 fixed A2 和旧 A2 资源 URL 均 410；A1 正常 |
| RT22 | pass | 21 | 删除并同 key 重建 scope 得到新 ID；新代无 active；旧 pin/旧资源不映射新代 |
| RT23 | **fail（产品缺陷）** | 6 | 新 runtime/static 的 scope_id pin 正确；旧 runtime/config 与 runtime/static 声明了 query `scope_id` 却忽略错误 pin |
| RT24 | **fail（产品缺陷）** | 2 | 未知 API 和双前缀路径均为 404，但 Content-Type 为 `text/plain`、正文 `Not Found`，不是文档约定的 JSON 错误信封 |
| RT25 | pass | 241 | 5 客户端 ×40 次读（200 次）并发 10 轮 A1/A2 切换（20 次写）；200 次读全部完整，A1=144、A2=56、错误=0；耗时约 1.46s |
| RT26 | pass | 7 | 重启后不登录，默认 A1、fixed A2、图片字节均保持；generation=2 |
| RT27 | pass | 30 | 根路径实例：默认 v1、fixed v2、图片无重复前缀、旧 config、缺头 400、未知路由 404 均正确 |
| RT28 | pass | 1 个 Node 预检 + Chromium 请求 | 公共 CORS 预检 204 且 ACAO=`*`；Chromium A/B 页面 `credentials:'omit'` 读取成功，marker/scope/version 正确 |
| RT29 | pass | 0（复用 RT28 证据） | A/B `img.src` 自然尺寸 8×8，图片 SHA 分别匹配 red/yellow 夹具，截图不同 |
| RT30 | pass | 3 | fixed A2 ZIP 与 legacy active A1 ZIP 的 MIME/结构/manifest/config/资源 SHA 正确；snapshot 不符 409 且不是 ZIP |

### 11.3 缺陷（按影响排序）

#### D1：旧 runtime config/static 声明 `scope_id` 却静默忽略

- 影响：调用方以为固定了某个旧代 scope，实际仍拿到当前 live scope 的配置/静态包。若同名 scope 已删除重建，可能把旧接入方的固定请求映射到新代内容。
- 证据：`artifacts/evidence/RT23.json`；IDL `RuntimeConfigRequest.scope_id` 和 `StaticPackageRequest.scope_id` 声明为 query 参数，handler 未读取。
- 最小复现（A 的 Header + B 的 scope_id）：
  ```bash
  curl -i 'http://127.0.0.1:<port>/rainbow-bridge/api/v1/runtime/config?scope_id=<B-scope-id>' \
    -H 'x-environment: <A-env>' -H 'x-pipeline: default'
  # 实际 200，返回 A 当前配置
  curl -i 'http://127.0.0.1:<port>/rainbow-bridge/api/v1/runtime/static?environment_key=<A-env>&pipeline_key=default&scope_id=<B-scope-id>'
  # 实际 200 ZIP，返回 A 当前静态包
  ```
- 建议归属：runtime handler/legacy adapter；要么把 query `scope_id` 传入 `ValidateReadScope`/`RuntimeReleaseWithScope`，要么删掉 IDL 声明并明确不支持。不要用不传 pin 掩盖。

#### D2：未知 API GET 返回纯文本 404，不是 JSON

- 影响：按运行时文档解析 `{code,msg,error,error_type}` 的客户端会拿到 `text/plain` 的 `Not Found`；不会误读配置，但错误分类和排障会退化。
- 证据：`artifacts/evidence/RT24.json`；base 实例和根路径实例表现一致；双前缀路径也是 404 纯文本。
- 最小复现：
  ```bash
  curl -i 'http://127.0.0.1:<port>/rainbow-bridge/api/v1/runtime/does-not-exist'
  # HTTP 404, Content-Type: text/plain, body: Not Found
  ```
- 建议归属：未匹配路由的 404 处理；返回 JSON 错误信封，同时保持真正未知页面不被 SPA 兜底为 200。

### 11.4 大白话结论

- **默认生效读取正确吗？** 正确。A/B 各自读到自己的 active 版本、配置和图片，A1/A2/A3 三层状态隔离成立。
- **指定版本固定吗？** 正确。fixed 在切换前后都读同一 snapshot，且不返回 active_revision/activated_at。
- **图片能真实获取吗？** 能。匿名 URL 请求得到可解码 PNG，SHA、长度、MIME 与本地夹具一致；文本文件字节也一致。
- **A/B 有串范围吗？** 没有。60 次交错读取和 200 次并发读取都按本次 Header/scope 精确匹配，没有发现缓存串范围。
- **切版无需重启吗？** 成立。切 A2、回退 A1 后新请求立即变化；重启后 active/fixed 和图片仍保持。
- **浏览器可用吗？** 可用。Chromium 从两个独立业务页跨源读取，预检 204，A/B marker 不同，图片 naturalWidth/Height 为 8。
- **重启/并发/根路径通过吗？** 通过。200 次并发读全部完整，重启后身份与字节保持，根路径实例五条关键链路通过。

### 11.5 测试交付与限制

- 测试代码：`tests/e2e/runtime-business/`（实例、管理造数、匿名 HTTP、断言/证据、浏览器分文件）。
- 重跑入口：`script/test-runtime-business.ps1`；`-KeepRunning` 用于人工 Postman/浏览器演示，默认自动停止且先核对构建标识。
- Postman：`tests/e2e/runtime-business/postman/runtime-business.postman_collection.json` 与模板已提供；真实运行环境写入 `artifacts/postman/runtime-business.postman_environment.json`。**已提供，未在 Postman 中实际执行**；JSON 结构已校验。
- 3 分钟演示顺序和隔离路径说明见 `tests/e2e/runtime-business/README.md`。
- 本轮只验证单实例 SQLite + local storage + Redis/备份关闭；未验证 K8s 多 Pod、MySQL/PostgreSQL、跨卷、热恢复或权限隔离。
- 未修改产品源码；两个缺陷留待用户决定是否修复。测试脚本自身发现的 URL 重复前缀、浏览器等待时序和默认 pipeline 种子属于夹具错误，已修正后重跑，不记为产品缺陷。
