# 虹桥演示登录：前端接入 PRD

日期：2026-10-20
状态：已完成本地整合；实际验证与演示入口见 [整合结果](../admin-login-integration.md)。
现有 UI 工作区：`D:/kylin/rainbow_bridge`，当前分支 `codex/config-freeze`。
规划分支：`codex/admin-login-ui`，使用独立工作树。
后端配套：[演示登录后端 PRD](D:/kylin/rainbow_bridge-admin-login/docs/prd/admin-login-session-v1.md)。

## 1. 做什么，做到哪里结束

用户已明确：登录只是让虹桥演示更完整，正式鉴权将交给外部 IAM。本轮只做一套常见、易懂的体验：**打开管理页面需要登录；输入一次账号密码后连续操作；刷新仍保持；点退出后管理页面需要重新登录。**

保留已经完成的光谱工作台 UI。增加登录页、统一登录状态和导航退出入口，自动携带后端会话及 CSRF，删除锁版/解锁/创建版本/切换版本时反复输入管理令牌的步骤。

不做注册、角色配置、权限管理后台、改密页面、找回密码、个人中心、记住我、多端会话列表、移动端专项适配或 IAM 协议接入。已有后端改密接口本轮不调用。后端已经完成的安全基础不拆掉，但不因此扩大页面数量。

前端和后端同时开发，各自通过本地测试后再合并联调。前端不因管理鉴权尚未接完而停工，也不能把 Mock 页面成功当作真实后端已验收。

## 2. 基线和工作区保护

### 2.1 现有 UI 不是 Git HEAD

已核实主工作区仍基于 `f6184b4`，光谱 UI 改动尚未提交，包含首页、配置、环境、资源、迁移、导入导出、版本发布及共享组件/样式。新建工作树不会自动带上这些改动。

实施时优先复用用户后续提供的光谱 UI 稳定提交作为新分支基线；如果仍未提交，从已提交基线创建独立工作树，再**按路径清单转入现有 UI 增量**：

- 跟踪文件用限定路径的差异补丁，未跟踪的光谱 Vue 源文件逐项核对后复制；记录来源 SHA、路径及接入前基线。
- 范围限于实际需要的 `vue/` 源码及既有前端页面测试；包含 `SpectrumLayout`、光谱样式、路由和首页版本读取所需的前端文件。
- 不复制整个脏工作区，不带入 `node_modules`、`dist`、构建缓存、Go 文件、生成文件、真实配置、数据库、资源或临时截图。
- 后续交付明确区分“原光谱 UI 基线”和“本轮登录增量”，不能把旧 UI 全算成本轮登录改动，也不能覆盖已有视觉改造。

前端工作树实际路径以创建工具返回值为准。本文件不创建工作树、执行提交或推送。主工作区保持原样，不在里面切分支、reset、stash 或整理他人文件。

### 2.2 实例与文件归属

| 项目 | 前端安排 |
| --- | --- |
| 开发服务 | 新工作树内 `127.0.0.1:3001`，严格占用该端口，冲突时明确另选并记录 |
| 真实后端 | 后端负责 `127.0.0.1:18081`，独立库/资源/Cookie，前端不修改后端目录或重建其库 |
| 现有实例 | 不停止/替换18080及3000，不操作现有演示数据 |
| API访问 | 经前端 Vite 代理访问18081，浏览器保持同源请求；不硬编码登录Cookie或管理密钥 |
| 前端文件 | `vue/`、新增前端登录页面规格及自己的页面测试辅助文件 |
| 后端/公共测试 | `biz/`、IDL、Go、`script/test-local.ps1`、`tests/e2e/local/support/net.ts` 由后端维护 |

当前 Vite 代理固定指向8080，前端负责在**自己的分支**增加可覆盖的代理目标，例如进程变量 `RAINBOW_API_TARGET=http://127.0.0.1:18081`，默认值维持原行为，来源配置不进业务界面。代理不删除/伪造浏览器 Origin；后端隔离配置需允许 `http://127.0.0.1:3001`，由后端维护。

Cookie不按端口隔离，测试用独立浏览器上下文；不能认为换端口就不会碰到其他实例Cookie。前端可提供私有测试 helper，不并行修改后端负责的公共网络层。

## 3. 页面与交互

### 3.1 登录页

新增应用路由 `/login`，部署后为 `/rainbow-bridge/login`，不在源码硬拼部署前缀。页面沿用现有虹桥标识、光谱颜色、字体、间距、按钮及输入框，电脑网页端优先。

只有账号、密码、一个“登录”主按钮和错误提示。用户名初始可显示 `admin`，可编辑；密码初始为空，不预填演示密码。账号输入使用 `autocomplete=username`，密码使用 `autocomplete=current-password`，Enter可提交，提交中禁重复点击。不要增加注册、忘记密码或“记住我”入口。

登录失败在表单内显示“账号或密码错误”；限流显示稍后重试；网络/503提示服务暂不可用并允许重试。不得打印输入到控制台，错误发生时不把密码塞入通用 `ApiError.context`。

登录成功回到原本要访问的管理页面，缺省 `/config`。只接受应用内已知路由及其合法查询参数，保留 `env`/`pipeline`；拒绝绝对URL、`//`、脚本URL或跨站地址。不要因为登录成功自动保存表单、解锁或发布。

### 3.2 工作台身份入口

在现有 `SpectrumLayout.vue` 的导航区域增加紧凑的账号名和“退出”入口，匿名时显示“登录”。不新建个人中心，不重新设计导航。保留桌面布局、已有导航、滚动恢复和可访问焦点样式。

退出成功后清当前管理数据和认证状态，回到登录页；退出请求失败时显示失败和重试，不能一边服务端仍有效、一边声称已退出。点击退出不解除锁版、不切换生效版本。

### 3.3 页面访问范围

| 路由 | 行为 |
| --- | --- |
| `/` | 可匿名查看生效配置概览、业务读取接入说明；不触发受保护的编辑区/管理数据请求 |
| `/login` | 可匿名访问；已确认登录的用户可进入管理页，初始化完成前避免误跳转 |
| `/config`、`/environments`、`/resources`、`/migration`、`/import-export`、`/releases` | 登录后进入，支持直接打开URL与刷新 |

项目首页如果某个组件依赖 `freeze/status` 或管理用列表，匿名时隐藏该管理部分或提供登录入口，不能为了让首页显示而要求后端放开管理 API。环境/渠道等公开概览使用既有 `runtime/overview`；首页生效版本继续使用 `runtime/release`，保持“运行时是已生效版本”的现有语义。

### 3.4 删除重复输密钥步骤

重点核对：

- `vue/src/components/Config/FreezeTransitionDialog.vue` 及 `FreezePanel.vue`；
- `vue/src/components/release/CreateReleaseDialog.vue`、`ActivateReleaseDialog.vue`；
- `vue/src/stores/freeze.ts`、`release.ts`；
- `vue/src/lib/api/freeze.ts`、`release.ts`。

删除管理令牌输入、必填校验、逐次 Bearer 头和依赖 token 参数的调用链。保留操作原因、冻结/生效 revision、request_id、确认按钮、失败反馈和原幂等确认流程。`admin_configured` 若保留兼容读取，不再显示“配置 RAINBOW_FREEZE_ADMIN_TOKEN”的提示；未登录由统一身份状态处理。

“确认结果”仍重发首次业务 payload，不生成新的 request_id，不更换目标/原因。网络失败与401分开处理：401要求重新登录，不能通用拦截器登录后自动重放原写操作。原有发布重试状态可以保留在当前页面内存，但不新增跨页面草稿恢复或持久化系统。

## 4. 接口契约与请求接入

### 4.1 本轮只用三个 auth 接口

以下路径交给既有URL构造器，BasePath只拼一次。auth成功 `code=0`，既有业务响应的code语义不改。

| 请求 | 请求字段 | 响应/用途 |
| --- | --- | --- |
| `POST /api/v1/auth/login` | JSON `{username,password}` | 200，浏览器接收会话Cookie，读取user、期限、csrf_token |
| `GET /api/v1/auth/me` | Cookie | 200恢复身份；401 `AUTH_REQUIRED` 表示未登录 |
| `POST /api/v1/auth/logout` | JSON `{}`，Cookie及有效会话的CSRF | 200，`data.logged_out=true` |

login/me响应：

```json
{
  "code": 0,
  "msg": "success",
  "data": {
    "user": {"id": "1", "username": "admin", "role": "admin"},
    "session": {
      "expires_at": "2026-10-20T16:00:00Z",
      "idle_expires_at": "2026-10-20T10:00:00Z"
    },
    "csrf_token": "<会话内稳定的CSRF值>"
  }
}
```

错误体固定含 `code`、`error_type`、`msg`，使用真实HTTP状态。前端不读取HttpOnly Cookie、不把会话凭证当作JSON字段、不新建本地token登录机制。后端保留的改密接口不在本轮页面契约内，不消费其现有字段差异。

### 4.2 统一请求处理

新增独立 auth API 模块与 auth store，修改既有 API client 的统一请求入口：

1. 所有本系统请求继续 `credentials: 'include'`。
2. 本系统管理POST自动带内存中的 `X-CSRF-Token`，包括FormData上传、导入、预览和导出。FormData不手填Content-Type边界。
3. 检查绕过 apiClient 的 `fetch`、Blob/ZIP下载及上传入口；它们也需在同一个小辅助方法中复用认证头和错误处理，不能只让普通JSON请求可用。
4. 只给本系统可信API地址附CSRF，不给外部资源URL/第三方链接附加。公开runtime的GET不需要CSRF，也不依赖登录。
5. 登录/改密等敏感请求体不进入通用错误上下文、Toast或console。密码不放localStorage/sessionStorage；CSRF和user只放内存，通过me恢复。
6. 避免 `apiClient → auth store → auth API → apiClient` 的循环依赖；用一个职责明确的认证状态/请求辅助边界解决，不引入请求插件平台或IAM适配框架。

### 4.3 错误处理

| 响应 | 必须行为 |
| --- | --- |
| 登录401 `AUTH_INVALID_CREDENTIALS` | 表单内提示；不触发全局清状态/跳转循环 |
| 管理/me401 `AUTH_REQUIRED` | 清管理身份与数据，一次提示“请重新登录”，进入登录页；不自动重发原操作 |
| 403 `AUTH_FORBIDDEN` | 提示账号不可用，清管理状态，不不断重试登录 |
| 403 `AUTH_CSRF_INVALID` | 提示校验失败，可显式刷新me后由用户重试；不自动重放请求 |
| 403 `AUTH_ORIGIN_DENIED` | 显示来源配置错误，不让用户反复输密码 |
| 429 | 读取Retry-After，短暂禁重复提交或提示等待 |
| 503/网络失败 | 显示服务暂不可用及重试入口；恢复身份失败不能误认为确定未登录 |

多个请求同时401只触发一次跳转/提示。鉴权初始化最多合并为一个在途me请求；不写定时me心跳无限保活。过期以服务端响应为准，不在页面新增倒计时和续期弹窗。

auth store至少区分尚未确认、已登录、未登录和恢复失败；身份确认前管理路由显示简短加载态。退出或切换登录批次后，晚到的旧me/管理响应不得重新恢复旧身份和敏感数据，可用一个递增批次编号处理，不建设复杂会话状态机。

## 5. 与后端并行的实施顺序

### F1. 准备UI基线和最小页面

按§2建立独立工作树并完整保留光谱UI，先实现登录页和导航身份入口。沿用现有Vue3、Pinia、Radix Vue、Tailwind4、`spectrum.css`语义颜色及已有深色模式，不加组件库，不重新探索视觉方案。桌面1440宽度检查表单、导航、键盘操作和错误状态。

### F2. auth store、路由和请求层

接三个auth接口，完成初始化、CSRF、401及退出清理。后端已实现的登录/me/logout可以用于早期契约验证；若隔离实例尚未提供3001来源配置，先用Playwright路由Mock按§4验证前端行为，不修改Go或原实例配置。

### F3. 密钥框替换和业务入口核对

移除冻结/发布密钥链路；检查环境、资源、导入导出和迁移的请求适配；保持JSON大整数原始处理、11种配置类型、asset引用、版本读取和幂等确认。不要用删请求/关功能来消除401。

### F4. 联调与集成

后端完成管理保护后，用独立浏览器上下文连接3001→18081，真实验证§6。前端提供UI基线和本轮路径清单，后端负责在独立集成工作树合并；前端处理Vue冲突，保留首页生效版本及新ZIP导出接入。

前端不编辑 `test-local.ps1`、公共 `support/net.ts` 或后端生成路由。已有页面规格涉及旧“令牌输入”选择器时由前端更新页面行为断言，后端负责公共实例/登录夹具；共享规格的具体文件在集成时由单一执行者修改，不在同一文件上同时写。

前端此前发现的快照重命名500，向后端提供原始响应和最小操作步骤；正式联调时确认创建、切换、切回真实成功。只做Mock时不能写成版本发布验收通过。

## 6. 演示验收标准

| 编号 | 操作 | 预期 |
| --- | --- | --- |
| D-F01 | 直接打开/config，正确/错误密码登录 | 未登录去登录页；错误就地提示；正确进入原管理页面，无明文日志 |
| D-F02 | 登录后刷新、直接打开带env/pipeline的管理URL | 自动me恢复，不闪错误页面，范围保留，不再要求输管理密钥 |
| D-F03 | 编辑、上传、导入预览、导出等不同请求形式 | 自动带Cookie/CSRF，正常成功，文件下载错误不伪装成功 |
| D-F04 | 编辑→冻结→创建版本→切换/切回→首页查看 | 原锁版规则保持，弹窗无密钥框，当前生效配置与版本一致 |
| D-F05 | 点击退出，再进管理页/用旧Cookie发请求 | 页面清管理状态，真实后端拒绝旧会话；退出不解锁配置 |
| D-F06 | 模拟401、503、错误Origin/CSRF、多个同时401 | 按§4.3处理，不重复Toast/循环跳转，不自动重放写操作 |
| D-F07 | 扣住me/管理响应，退出后再放回 | 旧响应不能恢复旧身份或管理列表 |
| D-F08 | 匿名首页与默认/指定版本的业务读取 | 首页可用，管理内容不越权请求；runtime和资源继续可读 |
| D-F09 | 查看原始请求与错误日志、构建和桌面截图 | BasePath不重复，无密码/CSRF泄露，光谱UI与桌面布局保留 |

F01/F02/F06/F07可先用Mock，但交付标明“页面行为Mock通过”。F03/F04/F05/F08必须在真实鉴权后端和最终集成代码上运行，不能以Mock替代。使用测试专用账号/独立数据，密码从进程环境注入；Playwright trace可能记录登录body，认证阶段关闭trace/视频/HAR敏感录制，不归档含秘密的原始网络记录。

至少执行 `cd vue` 后 `npm run build`，以及新增登录页面Playwright规格。集成后补跑受影响的冻结/发布页面流程和原光谱页面回归；复用未受影响证据时注明时点。无需做移动端、IAM或多人权限验收。

## 7. 交付与后续IAM边界

交付说明给出实际文件、UI基线、接口接入结果、Mock/真实测试的区别、未验证项及阻塞。不要再写多份中途修复报告。最终联调完成后再按用户习惯更新桌面最终工作记录。

本轮只在集中auth API、auth store、路由守卫及请求辅助方法中处理登录，业务弹窗只读取“是否已登录”，不理解密码或IAM协议。以后由外部IAM替换登录入口和身份获取即可，不要求现在设计通用认证provider。

实施前使用 `$engineering-quality-preserving-refactor`，读取 `C:/Users/MalfuRisa/.codex/skills/engineering-quality-preserving-refactor/SKILL.md`，遵守AGENTS.md；UI遵循已有光谱设计，按职责拆分，避免把登录网络、路由、表单及错误逻辑全部堆在App.vue。

本文件只定义工作。当前没有自动创建工作树、发送聊天消息、提交、推送、更新18080实例或向原上游提PR。

## 8. 下一步协作安排（2026-10-20）

前端工作树已建立于 `C:/Users/MalfuRisa/.codex/worktrees/admin-login-ui/rainbow_bridge`，当前已有登录页面、会话恢复、CSRF、密钥框替换和Mock规格。本节安排后续联调，不重新开始UI改造。

1. **当前继续独立工作。** 完成构建及已有Mock用例，逐项说明通过/失败，不等待18081才继续。保留匿名首页、多个401只提示一次、退出后旧响应不能恢复状态等现有断言，不添加IAM、找回密码或角色页面。
2. **真实实例由后端负责。** 规划地址 `http://127.0.0.1:18081/rainbow-bridge/`，前端地址 `http://127.0.0.1:3001/rainbow-bridge/`，Vite `RAINBOW_API_TARGET=http://127.0.0.1:18081`。核实时18081未监听，不能宣称已可用；后端完成ping/登录/me/管理请求检查后交接。
3. **约定测试变量而不传密码。** 真实测试读取 `RB_QA_ADMIN_USERNAME`、`RB_QA_ADMIN_PASSWORD`；前端测试URL沿用 `RAINBOW_AUTH_UI_URL`。变量只用于Node/Playwright测试进程，不进入Vite前端包。不同Codex进程不会自动共享环境；由持有凭据的后端启动进程在前端工作树运行真实测试子进程并注入变量，前端提供准确测试入口和非敏感结果。禁止归档含登录请求体的trace/HAR。
4. **用真实登录走通最小页面链。** 新增或明确单独的真实联调规格，不拦截/Mock auth和管理接口：UI登录→进入配置管理→刷新恢复→退出→再次进入需登录；随后补编辑/上传/冻结/发布/切换/首页读取。不要要求人工注入Cookie作为真实登录演示路径。原后端旧页面回归可用认证fixture，它与UI真实登录规格是两种测试。
5. **共同定位时只看脱敏事实。** 若401，先确认实际请求的主机/端口/前缀、Cookie是否携带、me是否成功和error_type；若403再检查Origin/CSRF。后端旧测试只在gotoPage中自动登录，而部分规格直接page.goto，已发现这个未覆盖入口；它不证明前端新登录实现失效。
6. **避免重复实现。** 前端的auth store/统一请求层为Vue最终版本；后端工作树临时添加的client.ts CSRF缓存不直接覆盖进来。公共net.ts、页面认证fixture和实例启动由后端维护，前端负责自己的登录规格与页面选择器。
7. **联调结果决定合并。** 先最小登录链，再完整发布链，最后补受影响回归。快照重命名500提供精确操作和原始错误，由后端复现修复。全部就绪后在独立集成工作树合并，不直接切换18080，不把Mock通过写成真实验收。

交付只更新本文件或已有结果摘要，不新写一份阶段报告。当前本节是计划，尚未启动联调实例、发送任务、修改业务代码或执行合并。

## 9. 前端实施进展与真实测试入口（2026-10-20）

实际工作树：C:/Users/MalfuRisa/.codex/worktrees/admin-login-ui/rainbow_bridge，分支 codex/admin-login-ui。光谱UI来源 SHA 为 f6184b497b27aeb2323b86cef8011f60b376a956 加主工作区按路径转入的49个前端增量文件；基线补丁、文件清单和SHA-256记录在本次任务产物 admin-login-ui/spectrum-baseline.patch、spectrum-baseline.json。本轮登录增量另行列出，不包含Go、IDL、生成文件或旧实例数据。

当前已完成登录页、导航账号/退出、me恢复、管理路由守卫、内存会话/CSRF、JSON/FormData/ZIP统一请求处理、退出清管理数据与迟到响应保护；冻结/发布密钥输入及Bearer参数链已移除，原因、revision及原request_id确认流程保留。页面继续使用光谱UI，未新增IAM或移动端专项页面。

最终Mock结果：22项通过。最初15项登录/恢复/并发401/退出迟到响应/匿名首页断言全部保留；新增上传与归档CSRF、发布原payload确认（503与网络断开）、写请求401不重放、主动恢复失败、导出错误不误报成功，以及匿名首页公开概览先完成、me随后401/503时仍可用的竞态断言。首页公开概览已与管理缓存分开，退出清理不会误清匿名读取所需的数据。

最终构建：npm run build（vue-tsc与Vite）通过；/rainbow-bridge/与/两种BasePath构建通过；git diff --check通过。1440桌面登录页已在实际浏览器查看，账号/退出导航已用Mock页面截图检查。上述Mock与构建不代表真实发布链已通过。

独立真实规格为 tests/e2e/auth/real.spec.ts，辅助文件为同目录 real-helper.ts，不使用page.route、storageState或人工安装登录Cookie；旧Cookie只用于退出后的负例请求。配置 playwright.auth.config.ts 对认证阶段关闭trace、视频、HAR及自动截图。缺少测试凭据时明确失败，不静默跳过或退回Mock。

后端凭据持有进程先注入 RB_QA_ADMIN_USERNAME、RB_QA_ADMIN_PASSWORD，再在前端工作树执行：

~~~powershell
Set-Location C:/Users/MalfuRisa/.codex/worktrees/admin-login-ui/rainbow_bridge/tests/e2e
$env:RAINBOW_AUTH_REAL = '1'
$env:RAINBOW_AUTH_UI_URL = 'http://127.0.0.1:3001/rainbow-bridge/'
npx playwright test --config playwright.auth.config.ts --grep '最小真实登录链'
# 最小链通过且集成代码已保留生效版本/raw与ZIP接口后：
npx playwright test --config playwright.auth.config.ts --grep '完整真实发布链'
~~~

第一条真实规格走UI登录、直接范围URL、刷新me恢复、UI退出、原Cookie被拒绝及再次进入需登录；第二条使用新建qa_ui_*专用范围，走UI新增/编辑、图片上传、ZIP导出及导入预览、冻结/解锁、两版本创建/切换/切回、匿名runtime及资源和首页读取。不会更改18080实例或现有演示范围。

Mock入口无需凭据：清除测试进程中的 RAINBOW_AUTH_REAL 后执行 npx playwright test --config playwright.auth.config.ts。凭据只注入Node测试进程，不能进入Vite环境或前端构建；本轮不接收或归档明文密码。

代码交付分为原光谱基线49个文件与本轮登录增量44个文件（比较时忽略CRLF/LF格式差异）。登录增量集中在auth API/store/会话辅助、路由与请求层、登录页/导航身份入口，以及冻结/发布弹窗；ConfigDialog的类型编辑与原文处理没有改写。增量清单与仅登录补丁位于本次任务产物：

- C:/Users/MalfuRisa/.codex/visualizations/2026/10/20/01a157e4-c1ae-7071-810a-a8a7c4489e8a/admin-login-ui/login-paths.txt
- 同目录login-only.patch（反向check通过，仅供审核/集成，不曾应用到后端工作树）。
- 同目录mock-tests.log、build-prefix.log、build-root.log、login-desktop.png、workspace-account-desktop.png；账号导航截图为Mock数据。

联调现状：18081已监听；经3001真实代理匿名调用auth/me返回401 AUTH_REQUIRED，runtime/overview返回200。当前前端进程未持有RB_QA_ADMIN_*凭据，真实规格仅完成发现检查（2条），未执行登录或发布。因此D-F03/F04/F05/F08的真实端到端验收、原Freeze/Release及光谱回归仍需凭据持有进程在最终集成代码上补跑。未修改后端临时client.ts、公共网络helper或实例启动脚本，也未合并、提交、推送或切换18080。
