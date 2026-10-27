# 备注选填与非生效版本删除：前端接入 PRD

日期：2026-10-20。状态：前端实现与本机隔离验收完成，具体结果见第8节；尚未替换现有演示实例。

## 1. 做什么，做到哪里结束

用户已确认：锁版、解锁、创建版本、切换生效版本的原因改为选填；支持逻辑删除非生效版本。

沿用光谱工作台、现有登录会话和发布流程。本轮只做：四项操作不填备注即可确认；非生效版本有删除入口；删除后刷新列表；操作记录正确显示删除与空备注。保持一次登录连续操作，不增加密码框、版本号手抄确认、审批、回收站、恢复、物理清理、批量删除、IAM或页面重设计。

必要性：备注选填减少重复输入；删除入口清理测试版本；生效版本保护、固定版本调用提醒和原请求确认分别防止影响正在使用的版本、误判删除影响和网络重试时改变目标。

## 2. 工作位置与现状

实际整合工作树：`C:/Users/MalfuRisa/.codex/worktrees/admin-login-integration/rainbow_bridge`，当前分支 `codex/admin-login-integration`。后端方案与复核结果见同目录 [release-cleanup-backend-v1.md](release-cleanup-backend-v1.md)，以后端本文对应接口为准，不以旧演示登录PRD里的18081实例作为本需求已上线的证明。

该工作树包含尚未提交的后端、固定演示密码和启动器改动。前端实施直接在此工作树限定前端路径，不切换分支、不重置或提交他人改动。另建工作树时必须先确认基线包含当前登录、光谱UI、首页生效读取及新删除接口；只从 `731dd9c` 新建会缺少本轮未提交后端，不能误以为接口已具备。

`D:/kylin/rainbow_bridge` 的文档副本便于查阅，其旧前端工作区不是本轮实现基线。此处不覆盖Vue文件、不合并、不推送、不操作现有演示数据。需要后续提交或实例替换时按用户届时授权执行。

已核对的待改位置：

| 文件 | 现状 → 本轮目标 |
| --- | --- |
| `vue/src/components/Config/FreezeTransitionDialog.vue` | 拒绝空原因 → 锁版/解锁备注选填 |
| `vue/src/components/release/CreateReleaseDialog.vue` | 原因必填 → 备注选填，保留首次请求确认语义 |
| `vue/src/components/release/ActivateReleaseDialog.vue` | 原因必填 → 备注选填，保留revision与幂等约束 |
| `vue/src/views/ReleasesPage.vue` | 无删除操作，仍写“本版不提供删除” → 非生效项入口与准确说明 |
| `vue/src/components/release/DeleteReleaseDialog.vue`（新增） | 单一删除确认流程，不塞进创建/切换弹窗 |
| `vue/src/lib/api/release.ts` | 缺删除类型和请求 → 新增payload/result与API适配 |
| `vue/src/stores/release.ts` | 已有范围+批次隔离 → 删除成功复用同一刷新机制 |
| `ReleaseHistoryDialog.vue`、`FreezeHistoryDialog.vue` | 删除会被误显示成创建，空备注文案不统一 → 明确事件类型与备注展示 |
| `ReleaseDetailDialog.vue`、`Config/FreezePanel.vue` | 空说明、410和迟到响应需要处理 → 显示真实状态，避免旧详情残留 |

使用 `$engineering-quality-preserving-refactor`，先读取 `C:/Users/MalfuRisa/.codex/skills/engineering-quality-preserving-refactor/SKILL.md`。API负责契约、store负责状态、弹窗负责表单与提交；复用现有组件和小方法，不把所有逻辑堆到ReleasesPage。涉及样式只沿用现有光谱规范。

## 3. 四项操作：备注选填

1. 标签统一“备注（选填）”，移除必填星号、非空校验、`required`及因空备注禁用确认按钮的条件。保留版本号、冻结、revision、登录等其他校验。
2. 提交仍用后端字段 `reason`，统一发送trim后的字符串；空白发送 `""`，不自动填“测试”“默认原因”。
3. 上限为trim后500个Unicode码点，中文和一个非BMP字符均各计1；例如 `Array.from(value.trim()).length`。不得用UTF-16的`.length`或原生`maxlength=500`错误拒绝500个表情。501个字符显示“备注最多500字”，不发请求；保留中文输入法正常输入行为。
4. 列表说明、详情备注、冻结历史、发布历史以及相关重试提示：空值或缺失值显示“未填写备注”，有内容按文本显示，避免拼出`undefined`或空引号。
5. 版本状态栏不能因为`changed_reason`为空就声称“从未切换”。根据active/changed_at显示实际版本、最近切换时间；空备注仅影响备注文本。操作者、时间继续可见。
6. 创建/切换已有的首次payload确认机制不变，空备注也属于完整payload；不能在确认结果时重取最新revision或修改原备注。

## 4. 删除入口与弹窗

### 4.1 入口

只在已加载的非生效版本行提供“删除版本”次要操作，放在查看/切换之后，沿用现有按钮与颜色规范。当前生效项不显示删除入口；列表与active信息矛盾时按生效/未知处理，不乐观判断可删。加载、错误或未登录时不能提交删除；已成功加载且active为空表示尚无生效版本，列表内版本允许删除。

删除不依赖编辑区是否锁定，不要求解锁后再删。列表说明改为：“版本内容不可修改、不可覆盖；非生效版本可以删除，版本号不能重复使用。”布局和主操作层级保持现状。

### 4.2 普通确认

弹窗标题“删除版本 <版本号>”，展示环境、渠道、版本号，避免同名版本认错范围。打开时捕获环境key、渠道key、version、snapshot_id；UUID由程序处理，不要求用户输入。

说明文案：“删除后，固定使用此版本的应用将无法继续读取。底层文件暂时保留，不会释放磁盘空间，版本号也不能重新使用。”不宣传可恢复，因为没有恢复接口。

只包含“备注（选填）”、取消、删除版本；遵循第3节备注规则。提交中显示“正在删除…”，禁止双击和重复写请求；取消未提交弹窗不调用接口。保留键盘焦点、Esc和按钮可访问名称，不新增交互步骤。

成功200且响应目标匹配：提示“版本 <版本号> 已删除”，关闭对应弹窗，刷新原范围的列表和active，重新计算分页。页面不通过本地推测修改head revision或冻结状态。最后一个非生效版本被删且确实无其它版本时显示既有空态；刷新失败显示“版本已删除，列表刷新失败，请刷新”，不能改口成删除失败并重新执行删除。

## 5. API、鉴权和重试

### 5.1 删除请求

`POST /api/v1/release/delete`。复用 `apiClient` 自动处理BasePath、Cookie、CSRF、401及可信Origin，不另写fetch鉴权或读取Cookie。

```ts
interface ReleaseDeletePayload {
  environment_key: string
  pipeline_key: string
  version: string
  expected_snapshot_id: string
  request_id: string
  reason: string
}
interface ReleaseDeleteResult {
  request_id: string
  snapshot_id: string
  version: string
  deleted: boolean
  deleted_at: string
  deleted_by: string
  reason: string
  replayed: boolean
}
```

首次提交生成`crypto.randomUUID()`，保存整份不可变payload；请求中的snapshot_id取打开时选定项。响应信封为`{code:200,msg:"OK",data:...}`，`data`没有`operation`嵌套，不能照搬activate解析。成功需要`deleted===true`且request_id、snapshot_id、version与本次目标一致；200HTML、缺字段或目标不一致不得提示成功。

### 5.2 原请求确认

网络中断、超时或5xx后，结果可能尚未确认，保留首次payload，提供“确认结果”。再次点击原样发送，不能生成新request_id、更新snapshot_id、拼入最新范围或重取备注。收到`replayed:true`按确认成功处理。

备注或目标改变后，不能把新内容当“确认结果”发送；明确作为新操作，生成新request_id。原弹窗内确认保留记录；关闭不会撤回已经发出的请求，再次打开时先刷新状态再发起操作。不增加跨页面持久任务中心。

### 5.3 错误反馈

| 响应 | 反馈与状态 |
| --- | --- |
| 401 `AUTH_REQUIRED`/会话过期 | 走现有会话流程，清理旧操作状态；不循环重试 |
| 403 CSRF/来源错误 | 显示操作失败并允许刷新会话；不伪装删除成功，不绕过鉴权 |
| 400参数/备注错误 | 表单内提示，保留有效输入 |
| 409 `RELEASE_DELETE_ACTIVE` | “该版本已生效，请先切换到其他版本”，刷新列表/active，停止本次删除 |
| 409 `RELEASE_SNAPSHOT_MISMATCH` | “目标版本信息已变化，请刷新后重新选择”，停止原确认 |
| 409 `RELEASE_REQUEST_CONFLICT` | “本次请求信息冲突，请重新发起操作”，不自动改ID重发 |
| 410 `RELEASE_VERSION_DELETED` | “该版本已被删除”，退出目标操作并刷新，不提示“本次删除成功” |
| 404版本/范围不存在 | 提示目标不存在，刷新或清空失效范围，不继续使用旧目标 |
| 网络/5xx | 保留首次payload供确认结果，提示一次，避免全局与弹窗重复toast |

同一页面的详情、切换以及创建结果确认也应识别410：清除已删除目标内容或操作状态，刷新原范围；创建同名版本409时说明“版本号已使用，已删版本号也不能复用”。普通409不能统一翻译为“已生效”。

## 6. 状态隔离与操作记录

- 删除成功/确认成功调用现有store的新批次刷新路径，不能仅`splice`一行后继续拼接旧分页。已在途的旧列表、旧分页和旧active响应不得把已删除项加回来。
- 请求目标使用打开时范围快照。范围或登录身份变化，关闭/作废相关弹窗；A→B→A的旧请求不能关闭新弹窗、覆盖新列表、改掉新表单或提前结束新加载态。旧范围成功结果只影响原范围，不能刷新当前B范围成A。
- 详情与历史查询也按打开批次+范围+目标校验响应；删除成功、410、关闭重开或切范围时清理旧内容并作废在途响应。不得在410后继续显示旧资源和操作按钮。
- `ReleaseHistoryDialog`显式映射 `create→创建版本`、`activate→切换生效版本`、`delete→删除版本`，未知事件显示“未知操作”，不能把delete或未知类型当create。
- 删除事件展示目标版本、操作者、时间和备注，不显示“已切换生效”或head revision递增。历史分页仍保留创建和切换事件。
- 不为删除自动解锁、切换版本、删除编辑区资源、清空配置或请求磁盘清理。

## 7. 测试与真实联调

### 7.1 页面测试标准

沿用既有Playwright与登录Mock/真实登录辅助，不引入第二套会话逻辑。新增专项规格，例如 `tests/e2e/local/release/release-cleanup.spec.ts`，用例加既有 `@release` 标记，确保被Release套件实际选中；范围独立，避免污染既有qa_prod/main与韧性规格。旧规格中依赖“原因必填”标签的选择器和断言同步按新契约调整，不删除原主链验证。可用page.route构造响应乱序/网络丢失，真实链路不得Mock删除成功。

| 编号 | 触发与预期 |
| --- | --- |
| U01 | 锁版、解锁、创建、切换分别空备注和纯空白确认，真实请求reason为空；原有其他条件仍拦截 |
| U02 | 中文500/501、表情500/501校验与后端一致，501不发写请求；备注纯文本显示 |
| U03 | 非生效行有删除，生效行无删除；active为空且已成功加载仍可删；未登录/加载失败不能提交 |
| U04 | 取消零请求；双击只发一个操作；成功后目标消失、生效与冻结状态不变，最后一项删除后正确空态 |
| U05 | 首次已提交但响应丢失→确认结果，两个payload逐字段相同、request_id相同，replayed成功且只一条delete事件 |
| U06 | 四类409/410分别按error_type反馈；冲突不自动换ID重发；403/401不假成功、不跳过CSRF |
| U07 | 扣住旧列表/分页，删除并刷新后释放旧响应，已删项不复活；旧请求结束不提前关闭新加载态 |
| U08 | 同名版本A/B范围、A→B→A、旧删除响应迟到，当前范围/弹窗/表单不串；请求始终指向原snapshot_id |
| U09 | 详情已打开后被另一客户端删除，后续410清空旧内容；放回旧详情响应也不恢复旧内容 |
| U10 | 历史显示删除类型、操作者、时间；空备注统一文案；无“删除导致切换”错误描述 |
| U11 | 删除成功但随后的列表刷新失败，只报告刷新失败，不重发删除；200缺字段/目标错配/HTML不报成功 |
| U12 | 创建/切换首次请求原因从空变有或反向变化，按新操作处理；确认原请求仍保留首次备注和revision |

### 7.2 真实环境要求

本轮后端复核已停止其临时18083实例；18083不是常驻联调地址。现有18080/18081也不能仅凭能访问就认定包含新删除接口。

真实验收启动**当前整合工作树的最新源码**，使用独立数据库/资源/Cookie和可用端口，记录实际地址。优先复用 `script/test-local.ps1 -Suite Release` 的隔离编排；需要Vite时使用已有 `RAINBOW_API_TARGET`，同源代理并按实例配置精确可信来源，端口占用则明确报错，不复用未知进程。不改现有演示数据，不建立匿名后门。

本机演示凭据为admin/admin；测试通过启动进程给用例注入 `RB_QA_ADMIN_USERNAME`、`RB_QA_ADMIN_PASSWORD`。实例首次初始化变量是 `RAINBOW_ADMIN_USERNAME`、`RAINBOW_ADMIN_INITIAL_PASSWORD`，两组不跨进程自动共享。用真实登录获取Cookie与CSRF，不手填token、不复制旧Cookie。

真实流程：UI登录→建独立范围和配置→不填备注锁版→创建v1并生效→创建v2→删除非生效v2→列表只留v1→历史显示删除→匿名固定读v2/资源410→默认读v1正常→尝试复用v2被拒。另用第二个客户端在弹窗打开后切换目标为生效，确认前端正确处理409。

运行前端类型检查与构建（包括 `/rainbow-bridge/`、`/` 两种BASE_PATH），重跑受影响的Freeze、Release及既有登录韧性规格。完整真实流程至少在带前缀实例上通过，根路径完成构建与API地址冒烟；分别说明Mock、浏览器真实请求和未执行项，不写“页面可用”代替执行结果。截图/trace/HAR不得归档登录密码、Cookie或CSRF。

## 8. 实施顺序与交付

1. 核对工作树，新增删除API类型与适配，修改四项操作的备注校验与展示。
2. 新增删除弹窗和非生效入口，接现有store批次刷新、原请求确认及错误映射。
3. 修正历史事件、详情410与跨范围响应处理，移除“不提供删除”等旧文案。
4. 完成U01—U12、类型检查、两种BasePath构建与真实联调，按本PRD §8登记最终结果。

交付只在本节记录最终行为、实际验证命令/结果、改动文件、未验证边界和实例是否保留；不另写逐轮修复报告。不得将Mock通过当真实接口通过，不提交缓存/依赖/数据库/构建产物或本地凭据。不自动提交、推送、向上游提PR或替换现有演示实例。

### 8.1 最终行为与实现边界

2026-10-20：已在上述整合工作树完成实现，沿用“虹桥·光谱工作台”，以 refactoring-ui 为主要设计检查依据，distinctive-ai-design 为辅助。后续前端约定已写入 `vue/AGENTS.md`；按电脑网页端检查1280px、1440px及深浅主题，本轮不增加移动端重构。

- 锁版、解锁、创建、切换均支持空备注，trim后按Unicode码点限制500字。空备注统一显示“未填写备注”，不隐藏实际生效时间和操作者。
- 非生效版本提供弱化的行末删除入口。单一模态弹窗固定打开时目标，明确客户端影响、文件保留及版本号不可复用；取消无写请求，提交防重复。
- 新增删除API契约验证，网络/5xx后原样确认整份不可变payload；备注变更作为新操作。409、410、404、401、403分别处理，不伪造成功，也不绕过Cookie/CSRF。
- 删除成功开启新批次刷新列表和active，作废旧分页；刷新失败明确提示。详情410清空旧内容；关闭重开、范围变化、A→B→A和身份变化均作废旧完成处理。历史显式区分创建、切换、删除与未知事件。

职责保持为：API校验响应，store管理范围和刷新批次，弹窗处理表单与提交；`operation-note.ts`复用备注规则，`useDialogAttempt.ts`复用提交失效保护，`useDialogHistory.ts`复用历史分页隔离。列表删除资格每次状态变化只扫描一次O(n)，各行判定O(1)，避免逐行重复扫描。批次保护使用常量级计数，无新增后台任务队列、持久化凭据或依赖。

主要改动文件：

| 位置 | 内容 |
| --- | --- |
| `vue/src/lib/api/release.ts`、`stores/release.ts` | 删除契约、active可信条件、范围/批次与删除刷新 |
| `vue/src/components/release/DeleteReleaseDialog.vue`、`views/ReleasesPage.vue` | 新入口、单次确认弹窗、冻结目标与错误反馈 |
| `CreateReleaseDialog.vue`、`ActivateReleaseDialog.vue`、`Config/FreezeTransitionDialog.vue` | 备注选填、确认语义、410与旧请求保护 |
| `ReleaseDetailDialog.vue`、`ReleaseHistoryDialog.vue`、`Config/FreezeHistoryDialog.vue`、`Config/FreezePanel.vue` | 详情清理、历史事件、统一空备注展示 |
| `vue/src/composables/useDialogAttempt.ts`、`useDialogHistory.ts`、`lib/operation-note.ts` | 共享规则与响应隔离 |
| `vue/src/components/ui/dialog/DialogContent.vue`、`styles/spectrum-release.css` | 可选遮罩样式、目标信息层级、桌面行操作布局 |
| `tests/e2e/local/release/release-cleanup*.spec.ts`、`cleanup-fixture.ts`、`playwright.cleanup.config.ts` | 32项专项Mock与真实浏览器链路 |
| `tests/e2e/local/release/release-api.spec.ts`、`home-active-release.spec.ts`、`local/support/env.ts`、`playwright.local.config.ts` | 更新旧必填断言，允许隔离环境显式指定快照与下载目录 |

### 8.2 实际验证结果

证据根目录：`C:/Users/MalfuRisa/.codex/visualizations/2026/10/20/01a157e4-c1ae-7071-810a-a8a7c4489e8a/release-cleanup`。以下命令均从整合工作树执行，表中相对证据路径基于该目录。

| 验证 | 实际命令与结果 | 证据 |
| --- | --- | --- |
| 类型检查 | `cd vue; npx vue-tsc -b --force`通过 | `tests/cleanup-ui-final4/frontend-typecheck.log`及manifest |
| 两种部署路径 | 分别设置`BASE_PATH=/rainbow-bridge/`与`/`，执行`npx vite build --outDir <隔离输出目录> --emptyOutDir`；均通过 | `tests/cleanup-ui-final4/build-frontend-web-prefix.log`、`build-frontend-web-root.log` |
| 专项Mock | `cd tests/e2e; npx playwright test --config=playwright.cleanup.config.ts`：32/32通过，覆盖U01—U12；历史额外覆盖`constructor`等未知事件 | 同批次完整Release结果包含全部32项 |
| 完整Release | 隔离启动器副本`run-final-check.ps1 -Suite Release -RunId cleanup-ui-final4 -AttachmentRoot <证据根>/tests`：47/47通过，无重试、无排除失败项 | `tests/cleanup-ui-final4/pw-prefix-main.console.log`、`pw-prefix-main.json` |
| Freeze | `script/test-local.ps1 -Suite Freeze -RunId cleanup-ui-freeze-final -SkipBuild -AttachmentRoot <证据根>/tests`，复用最终构建、全新数据库：4/4通过 | `tests/cleanup-ui-freeze-final/pw-prefix-main.console.log` |
| 登录韧性 | 设置`RAINBOW_AUTH_UI_URL=http://127.0.0.1:3002/rainbow-bridge/`，运行`npx playwright test --config=playwright.auth.config.ts auth/login.spec.ts auth/requests.spec.ts`：22/22通过 | 本轮命令终端输出；该组使用既有登录Mock |
| 最新真实链路补验 | `run-isolated.ps1 -Suite Release -RunId cleanup-ui-verified -SkipBuild -AttachmentRoot <证据根>/tests`，只选择真实UI专项：1/1通过 | `tests/cleanup-ui-verified/pw-prefix-main.console.log` |
| 根路径API冒烟 | 11890根路径实例的首页、JS/CSS、ping、version均200；无会话POST `/api/v1/release/delete`返回401 | `tests/cleanup-ui-final4/root-api-smoke.json`、`ready-root-smoke.json` |
| 桌面视觉 | 实际渲染检查1280/1440、深浅主题；删除目标清晰、主次按钮明确、背景遮罩与焦点分隔可见 | `visual/delete-1280.png`、`delete-1440.png`、`delete-dark.png` |

完整Release同时覆盖首页生效读取/静态导出8项、发布API契约1项、页面主链1项、原发布韧性4项及本轮33项专项，合计47项。真实浏览器通过页面登录获取会话，创建独立范围及文字/图片配置，以空备注创建/切换、空白备注锁版/解锁；删除前后active和冻结状态一致。首次删除由真实后端提交，仅丢弃首个响应，再由页面原样确认，两个payload逐字段相同、只有一条delete事件；未Mock删除成功。固定JSON/raw/静态包及资源读410，默认读v1正常；已删版本号复用409；第二个客户端将打开中的目标切为生效后，页面正确收到并显示删除409。

### 8.3 隔离环境、限制与收尾

实际完整验收地址：`http://127.0.0.1:11880/rainbow-bridge/`，独立B实例11881，根路径冒烟11890；最新真实补验使用11882/11883。临时预览使用3002，不复用现有3000、18080或18081。所有隔离实例及本轮预览均已停止，临时数据与证据留作复核；现有演示实例和数据未改动。

本机默认系统临时目录出现两类环境问题：后端快照目录rename报`The handle is invalid`；Chromium下载即使普通文本也返回`canceled`。本轮将`RAINBOW_RELEASE_DIR`明确指向工作树下独立的绝对目录，并设置`RB_QA_DOWNLOADS_DIR`为独立下载目录后通过；测试端用`RB_QA_A_RELEASE_DIR`/`RB_QA_B_RELEASE_DIR`对应真实资源路径。未修改后端存储或浏览器安全策略。曾尝试延后释放Blob URL，但隔离验证未支持该归因，已全部撤回，`useActiveRelease.ts`与原基线一致。

启动器副本保存在证据根目录，只固定RepoRoot、设置上述独立快照目录并增加根路径冒烟；仍实际执行`@release`全部规格，保留失败退出码，仓库的`script/test-local.ps1`未修改。最终运行时额外设置：

```powershell
$env:RB_QA_A_RELEASE_DIR = Join-Path (Get-Location) '.dev-integration/cleanup-frontend-storage-cleanup-ui-final4-a'
$env:RB_QA_B_RELEASE_DIR = Join-Path (Get-Location) '.dev-integration/cleanup-frontend-storage-cleanup-ui-final4-b'
$env:RB_QA_DOWNLOADS_DIR = Join-Path (Get-Location) '.dev-integration/cleanup-browser-downloads-final4'
```

未验证生产部署、MySQL/PostgreSQL、其他浏览器及移动端，不将本机SQLite/Chromium结果写成生产验收。登录密码、Cookie、CSRF不归档为trace/HAR或截图内容。未修改生成代码或后端业务，核对前后已跟踪后端diff一致；未提交、推送、创建PR或部署。文档结果同步回`D:/kylin/rainbow_bridge/docs/prd/release-cleanup-frontend-v1.md`，该旧工作区Vue文件未覆盖。
