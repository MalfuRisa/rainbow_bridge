# 内网HTTP请求编号兼容修复：前端PRD

日期：2026-10-29。负责人：虹桥前端（local / 01a157e4-c1ae-7071-810a-a8a7c4489e8a）。状态：UUID 修复交付完成，构建、15 项专项和真实 HTTP 客户端替换验证通过；scope 重建间歇失败已在未修改 HEAD 对照中复现，作为历史遗留项保留，详见 §6。现有远程部署尚未修复，本聊天未提交、推送或部署。

## 1. 问题、证据与终点

前置测试见 remote-api-verification-and-request-id-v1.md §9。真实远程 http://10.60.226.43:31880/rainbow-bridge/ 的浏览器isSecureContext=false，randomUUID为undefined，getRandomValues为function；创建候选时freeze/lock成功，之后页面显示TypeError，release/create未发出。对应证据 .local-qa/remote-api/qa_20261029_17f0351f/browser.json/png；架构师已只读复核browser.json。

根因是前端直接调用仅安全上下文提供的API。本任务负责：不依赖HTTPS改造即可生成合规request_id，避免编号失败新增锁版副作用，保留原请求确认与重试幂等。后端MinIO 503、Redis列表缓存不在前端修复范围，不通过延迟刷新、禁用缓存、Mock成功等办法遮掩。

用户已授权本轮实现与验证；不包含推送、发布、替换现有远程服务和改远程配置。交付到可审查源码、相关验证、待部署清单。远程未部署时必须明示“实现已验证，现有部署尚未修复”。

## 2. 工作区与协作边界

- D:/kylin/rainbow_bridge，main_v1，编写时HEAD 05ff46b。远程实际v5.0.3/c49194a，与本地不一致；不要声称本地构建等同现有镜像。开始时重新核对git status并保护测试脚本和其他聊天改动，不reset/切主目录分支/覆盖他人成果。
- 读取根AGENTS.md、vue/AGENTS.md及 $engineering-quality-preserving-refactor（C:/Users/MalfuRisa/.codex/skills/engineering-quality-preserving-refactor/SKILL.md）。只做行为修复，保留光谱UI，无需视觉重设计；若确有组件视觉改动遵循既有refactoring-ui规则。
- 前端仅改vue/src、前端专项测试和本PRD；建议测试在 tests/e2e/request-id-frontend/，不要与后端同时改 tests/e2e/remote-api/ 或后端源码。不要复制构建产物到pkg/static/web，整包集成留待统一处理。
- 后端同时执行 remote-release-cache-backend-v1.md，接口字段与错误语义本批不改。不得向其他聊天派新任务或增加代理。

## 3. 设计与实现合同

1. 在vue/src/lib/新增单一职责方法（建议request-id.ts），统一返回规范UUID v4。先检测globalThis.crypto?.randomUUID是否为函数，存在则按正确接收者调用；否则使用crypto.getRandomValues生成16字节，正确设置version=4与RFC variant位，再格式化36字符UUID。
2. 不使用Math.random、时间戳或递增序号充当UUID，不注入全局crypto补丁，不新增大型依赖，不要求用户修改浏览器安全设置。两个API均不可用时抛出可读错误，例如“当前浏览器无法生成操作编号，请更换浏览器后重试”，保留当前表单输入与可恢复状态。
3. 全量rg重查调用点。当前7处：useReleaseReview.ts两处、useBackups.ts两处、CreateReleaseDialog.vue、ActivateReleaseDialog.vue、DeleteReleaseDialog.vue各一处；全部改用共享方法，不在组件中复制fallback。备份虽在远程不支持，也须修调用兼容，不扩展备份功能。
4. 新的“锁版并创建”意图先成功获得编号，再执行可能产生写副作用的freeze/lock。合法编号仅生成一次，随后连同完整payload保存，再发送create。若状态/身份校验失败，不发create；编号失败不新增冻结、不发送create、不清空备注。原先已冻结保持原状态。
5. pending原请求的重试/确认必须重用同一request_id和原payload；不能在HTTP重试、刷新、session恢复、confirmPending或网络不明时重新生成。新意图才产生新ID，双击/并发按钮保持现有busy保护。
6. 不改变用户重新审阅确认、expected_scope_id、expected_freeze_revision、active_revision字符串、自动版本号及revision冲突语义。后端明确503仍显示准确错误，不能把前端修复当作发布成功。已有API错误映射合适则复用，不额外铺陈技术说明。

共享helper负责随机源检测与UUID格式；composable保留意图生命周期与流程；API层保持现有请求合同。用提前返回降低嵌套，不做无关重构。

## 4. 实施与验证

先补直接覆盖缺失能力与重试语义的行为用例，再实现helper和替换调用点，运行vue类型检查/生产构建与相关发布、备份专项测试，不机械重跑全部历史套件。

| 场景 | 必须观察的结果 |
| --- | --- |
| 原生randomUUID存在 | 使用原生路径，生成合法UUID，既有流程保持 |
| randomUUID缺失而getRandomValues存在 | 正确v4/variant格式，创建/生效/删除/备份意图均能获得编号 |
| 两API均缺失 | 错误可读，备注保留，无新增freeze/lock及create请求 |
| HTTP内网来源创建 | 不再TypeError，Network出现合法request_id的create；后端503可作为前端请求送达证据，但不是创建成功 |
| 网络响应不明后确认/重开弹窗 | 原request_id及完整payload保持，新意图使用新ID；不能重复分配新操作 |
| revision冲突或scope改变 | 保持原有阻断与重新审阅语义，不跨scope套用编号意图 |

真实非localhost HTTP浏览器验证必需；Node的crypto通过不能代替。若现有远程服务未更新，使用本轮独立测试页面/隔离实例验证修复产物，或浏览器仅拦截前端静态文件加载修复产物、API真实透传；后一方式须明确标注“客户端替换产物”，不能声称服务器已部署。Mock只用于能力缺失/重试等确定性分支，与真实后端证据分开。

真实远程写入只用本轮新建run_id scope，沿用原测试PRD §4的创建清单、身份校验、finally解锁和删除、原有4范围基线核对。网页登录凭据由架构师交接，不写文件；不改密。现有MinIO尚不能创建成功版本时，不上传物理资源、不借用户版本验生效，依赖项记blocked；可在独立实例完成成功路径。不影响其他会话和服务。

## 5. 交付登记

在本PRD追加：实际改动、各调用点覆盖、构建/行为/真实HTTP浏览器结果、重试ID证据、测试数据清理、未验证项、所验证前端与后端版本、待部署状态。保留代码与日志路径，不提交凭据或业务内容。用户产出记录在最终整个任务完成时再整理，不新增过程报告。

派发：2026-10-29 已发送至虹桥前端，工具确认送达。实施与验证结果登记如下。

## 6. 前端实施与交付登记（2026-10-29）

### 6.1 实际改动

本地基线为 `main_v1 / 05ff46baf7ffe83c945d7050e753eb7dd0beeaf5`，修复保留在共享工作区。只修改下列 Vue 源码、新增 `tests/e2e/request-id-frontend/` 专项及本 PRD；未修改后端、其他测试目录或 `pkg/static/web`，后端并行改动保持原状。本轮依据 engineering-quality-preserving-refactor 组织职责，未修改视觉样式或接口合同。

- `vue/src/lib/request-id.ts`：统一生成 UUID v4。原生方法按 crypto 接收者调用；不可用或调用被拒绝时使用安全随机字节，设置 v4/variant 位。没有安全随机源时抛出可读错误，不引入弱随机数、全局补丁或依赖。固定 16 字节的生成与格式化为常量工作量，未作性能优化承诺。
- 意图编排、保存与原请求重放仍属于原 composable/组件。新编号在可能产生副作用的锁版之前生成；失败保留备注和原冻结状态。组件的编号构造移入已有异常处理范围，不再让同步异常逃出按钮事件；无 pending 时不显示虚假的“确认原请求”入口。
- 既有 pending 分支原样复用 request_id 和完整 payload；没有在 confirm、刷新恢复、网络错误后重新分配编号。scope、冻结 revision、active revision、版本自动编号及 CAS 确认规则未改变。

| 原直接调用位置（共 7 处） | 实施与覆盖 |
| --- | --- |
| `useReleaseReview.ts` 创建 | `prepare` 先生成 ID，再读状态/锁版；原生与 fallback、先取得 ID、缺随机源不新增锁版、重开/刷新重放有专项覆盖 |
| `useReleaseReview.ts` 生效 | 新意图使用 helper；原生/fallback 正常生效、无随机源不发 activate 有专项覆盖 |
| `CreateReleaseDialog.vue` | 新创建调用 helper，pending 原样重发；无随机源保留备注和旧请求确认由专项/原回归覆盖 |
| `ActivateReleaseDialog.vue` | 替换 legacy 直接调用，异常由原 try/catch 处理；当前页面未使用此旧组件，未宣称独立交互实测，依靠共享 helper 验证与 Vue 类型/构建检查 |
| `DeleteReleaseDialog.vue` | 新意图使用 helper，无编号不发请求；结果不明时重发完整原 ID/snapshot 请求有专项覆盖 |
| `useBackups.ts` 新操作 | 创建/删除备份、恢复上传/执行共享该入口，fallback 与无随机源有专项覆盖；原有备份网络确认语义复用回归 |
| `useBackups.ts` 取消恢复 | 新取消意图使用 helper；缺随机源不发取消、不丢失待重启计划有专项覆盖 |

最终 `rg` 审计：`vue/src` 中直接 randomUUID 调用只剩共享 helper 的能力检测和原生调用；7 个业务调用点均已替换。`git diff --check -- vue/src` 通过（Git 另有本机 LF/CRLF 提示）。

### 6.2 构建与确定性行为验证

- 修复前先运行无随机源用例：旧代码出现 `crypto.randomUUID is not a function`，未达到可读错误/无锁版要求；随后实现修复。
- 最终 `cd vue; npm run build` 通过，包含 `vue-tsc -b` 与 Vite 生产构建；仅生成 `vue/dist`，未复制嵌入目录。构建有 Vite 插件耗时提示，不影响成功结果。
- `cd tests/e2e; npx playwright test --config=request-id-frontend/playwright.config.ts`：**15/15 通过**。证据 `.local-qa/request-id-frontend/mock-results/.last-run.json`。这是浏览器页面与 Mock API 验证，能力缺失通过测试专用 initScript 构造，不冒充远程实测。
- 覆盖原生、fallback、整个 crypto 缺失、安全字节接口抛错、原生抛错后 fallback；512 个 fallback 值均满足 v4/variant 格式且本批无重复。覆盖未锁/已锁编号失败、备注保留、发布/删除/备份/恢复入口。
- 网络结果不明测试使用请求中断构造：重新打开和刷新后，第二次发送的**整份 payload 深比较相等**，页面恢复后随机生成计数为 0；新独立意图才使用不同 UUID。该证据证明原请求重放行为，不宣称真实网络超时或服务端幂等重新验收。

相关既有回归执行：

```powershell
$env:RAINBOW_AUTH_UI_URL='http://127.0.0.1:3002/rainbow-bridge/'
$env:RAINBOW_AUTH_RESULTS_DIR='D:/kylin/rainbow_bridge/.local-qa/request-id-frontend/regression-results'
npx playwright test --config=playwright.auth.config.ts release-review.spec.ts release-numbering-fixed.spec.ts backup-ui.spec.ts requests.spec.ts scope-identity.spec.ts
```

初次结果是 **68 通过、1 失败，不能记为整套通过**。失败为 `tests/e2e/auth/release-numbering-fixed.spec.ts:155` 的“fixed比较期间同名scope重建后旧结果不能写回新范围”，在第 176 行期望“版本比较”按钮 disabled，实际仍 enabled；失败 ID `0945f1233e0135156c7b-44da828c65202f62b681`。页面快照在 `regression-results/release-numbering-fixed-fixed比较期间同名scope重建后旧结果不能写回新范围-chromium/error-context.md`。

随后整份 `release-numbering-fixed.spec.ts` 复跑 **12/12 通过**，证据 `identity-recheck/.last-run.json`；但单项 `--repeat-each=3` 又得到 **2 通过、1 失败**，同一断言，日志 `identity-focused.log` 及结果 `identity-focused/.last-run.json`。因此单次重跑不足以关闭该项。

代码核对：该用例仅执行 fixed 比较和资源/版本页往返，不进行本次修改的创建、生效、删除或备份操作；相关页面、scope 选择、fixed 比较及原用例本轮均未修改。快速导航和 overview/路由更新时序是排查方向，**具体根因尚未证实**，不能仅凭无 diff 宣称测试基础设施问题，也不能由一次整文件通过推断身份保护已全部验收。

为区分本次回归与历史问题，使用 `git archive HEAD vue` 提取未修改的 `05ff46b` 前端到 `.local-qa/request-id-frontend/head-baseline/vue`，仅用 node_modules junction 复用已有依赖，独立 Vite 端口 3003；没有覆盖共享源码、切分支或接入远程 API。将相同的原用例指向该前端运行 `--repeat-each=5`，得到 **4 通过、1 失败**，仍为第 176 行 enabled/disabled 的同一断言。证据：`identity-head-baseline.log`、`identity-head-baseline/.last-run.json` 及其 error-context.md。

对照表明该间歇失败在没有本次 UUID 修改的 HEAD 已存在，不能归为这次编号修复引入的失败。**这关闭的是本批改动归因，不是该历史用例或身份保护问题本身**；具体属于页面行为还是用例时序还没有闭环。本次不修改无关产品代码或原测试来消除失败，保留该项供后续范围内排查。初次 68/69 和其后复跑结果均如上保留，不重写成整套全绿。

### 6.3 真实非 localhost HTTP 验证

成功运行：`front_uuid_20261029_0b205eeedd35`，证据目录 `.local-qa/request-id-frontend/front_uuid_20261029_0b205eeedd35/`。

- 地址为实际 `http://10.60.226.43:31880/rainbow-bridge/`；远程后端仍为 `v5.0.3 / c49194a066f448a0730e485fca18f7f60e6e0e0b`。本地未提交源码建立在 `05ff46b`，不等同于远程镜像版本。
- **证据类型：客户端替换前端静态产物，API 真实透传。** 主 HTML 仍走远程真实传输，仅在此独立浏览器 context 替换 module 入口及匹配的本地 assets；未拦截或伪造 API，未更新服务器。未设置安全上下文放行参数，也没有修改浏览器 crypto。
- 实际浏览器 `isSecureContext=false`、`randomUUID=undefined`、`getRandomValues=function`，`pageErrors=[]`。`freeze/lock` 返回 200，随后 `release/create` 携带合法 v4 编号 `661754dd-0dff-48b8-b719-56f172716781`、本轮 scope UUID 和冻结 revision `"1"` 发往真实服务，返回 **503 / RELEASE_STORAGE_UNSUPPORTED**。
- 前端不再卡在 UUID TypeError；503 正确暴露远程 MinIO 限制，未将其处理成创建成功、未激活任何版本。成功发布/真实备份恢复仍不在这次远程证据内，正常创建/生效路径来自 Mock 行为测试。
- `summary.json` 记录所替换 57 个 JS 文件及 SHA-256、远程与本地入口；最终重新构建后核对 57 个摘要全部相同，所构建源码与本次成功 HTTP 证据产物一致。`browser.json`、`network.json`、`client-replacement-http.png` 分别保留能力、必要请求元数据和已查看的页面结果。
- 前三次整页 HTML 替换验证停在登录传输阶段（`4e79d31bc6c3`、`0ae1d4bbdd3e`、`445e345285aa`），未进入环境创建；其中诊断见 auth/me、auth/login `net::ERR_FAILED`。保留这些失败记录。改为保留真实 HTML、仅替换静态脚本后上述成功；不据此武断归因浏览器内部实现，也不把失败运行算通过。

### 6.4 数据、会话与进程清理

远程只新建本轮唯一环境 `front_uuid_20261029_0b205eeedd35` 及其默认渠道和一项纯文本配置；未上传图片/文件，未借用用户版本，未成功创建快照。创建对象身份清单见 `created-objects.json`。

`finally` 按本轮真实 environment_id/scope_id 校验归属后解锁，再删除环境，核对 environment/list 与 runtime/overview 均不再可见；旧 scope 查询为 `410 SCOPE_DELETED`。仅保留系统正常逻辑删除/审计历史，不声称物理擦除数据库记录。`cleanup.json` 确认测试会话已登出、无物理资源上传。

`baseline-before.json` 和 `baseline-after.json` 完全相同，覆盖原有 **4 个 scope**：default/default、dev/default、dev/abc、dev/icbc 的环境/范围身份、冻结状态及 revision、生效版本/快照/revision；dev/abc 原本 locked+1，保持原样。未操作其他会话，不回滚他人数据。凭据仅用进程环境注入，无持久化浏览器状态、trace、录像或凭据文件。

临时进程收尾：远程验证脚本的独立 browser/context 已在 finally 关闭；Playwright 各轮执行均已退出。中断后复核本轮 3002 无监听，HEAD 对照 3003 的 node/Vite 进程按端口与命令行校验归属后停止（PID 39092）；最终未发现本轮 remote-overlay、UUID/identity 测试或 3002/3003 Vite 的 node 进程。未停止其他本地或远程服务。隔离 HEAD 文件和本地证据保留在已忽略的 `.local-qa/request-id-frontend/`，不作为产品源码或部署产物提交。

### 6.5 剩余事项与交付边界

UUID 修复源码、专项行为与真实 HTTP fallback 请求送达已验证，本批前端实施交付完成。历史 scope 重建间歇失败已由未修改 HEAD 对照复现，仍待独立排查；不宣布相关回归全部关闭。本次没有修改既有 scope 逻辑或原测试以“跑绿”。

**现有远程部署尚未修复。** 本聊天未提交、推送、部署、替换/重启远程服务、修改远程配置或更新嵌入资源；由架构窗口统一整理本次成果。远程 MinIO 创建仍是已部署后端的独立限制，后端并行候选成果尚未通过本次远程验证，不能混用结论。

## 7. 架构验收与提交范围（2026-10-29）

UUID兼容修复按本PRD范围验收通过，准予随用户本轮授权单独提交并推送origin/main_v1。架构师只读核对共享helper的安全随机源/v4位、7个业务调用点与try/catch、新意图锁版前生成编号、pending重放保留原payload，以及15项专项、构建交付记录、真实HTTP summary/network/cleanup和4scope前后基线。

真实浏览器证据仅在独立context替换客户端静态产物、API透传，证实HTTP下合法编号及create送达；旧远程后端503不算完整发布成功。后端MinIO快照另按后端PRD验收，不混用双方的运行版本。没有更新服务器或复制嵌入资源。

相关回归初次68/69，身份用例偶发失败在未修改05ff46b HEAD基线5次中也有1次失败，identity-head-baseline.log已核对；本批不修改该页面切换逻辑，不把一次重跑通过当作缺陷关闭。此历史遗留问题仍需单独排查，当前验收只覆盖UUID修复，不宣称全部前端回归通过。没有为通过测试删除或放宽旧断言。
