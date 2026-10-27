# 虹桥演示登录：后端实施 PRD

日期：2026-10-20
状态：已完成本地整合；实际验证与演示入口见 [整合结果](../admin-login-integration.md)。
仓库：`D:/kylin/rainbow_bridge`
后端工作树：`D:/kylin/rainbow_bridge-admin-login`，已有分支 `codex/admin-login`。
前端配套：[演示登录前端 PRD](D:/kylin/rainbow_bridge/docs/prd/admin-login-frontend-v1.md)。

## 1. 本轮目标与范围调整

用户已明确：这套登录只为本机演示提供完整体验，后续正式鉴权交给外部 IAM。原 PRD 中较完整的本地账号和会话要求收敛为：**一个管理员，登录一次进入管理页面，刷新后仍登录，退出后不能再操作；锁版和发布不再反复输入管理密钥。**

后端和前端可以并行开发。后端补齐管理接口的会话鉴权及测试；前端按本文件 §4 的固定契约开发页面和请求接入，接口未接完时使用测试 Mock。两边就绪后才合并联调。

本版替代原长版 PRD 的实施清单及全量验收要求。已经完成的代码和有价值的测试保留，不为收敛需求拆掉密码哈希、服务端会话、CSRF、撤销、登录限流及脱敏。**停止增加**账号管理、角色权限平台、密码找回页面、改密页面、多端会话管理、SSO/OIDC、IAM SDK、多数据库和部署形态专项适配。本轮也不引入“演示模式免鉴权”开关。

## 2. 现状及交付终点

已核实：主工作区和后端工作树均基于已提交版本 `f6184b4`，主工作区另有未提交的光谱 UI、首页生效版本和版本静态导出改动。后端工作树已经实现账号/会话表、Argon2id、四个 auth 接口、密码恢复命令、登录限流；已有登录与改密竞争、双进程撤销及日志脱敏测试。研发报告相关测试通过；本版不把这些报告视为管理接口已接通。

**仍需完成**：管理路由保护、服务入口可信身份、旧密钥替换、自动续期、精确来源 CORS、相关脚本适配和端到端联调。目前四个 auth 接口存在，不能据此声称配置、资源、锁版与发布已经全部受登录保护。

演示完成后应能走通：打开配置页 → 登录 → 编辑 → 锁版 → 创建版本 → 切换生效版本 → 首页/runtime 读取 → 退出 → 管理请求拒绝。业务应用读取默认或指定版本的方式保持。

前端交付记录还提到“创建快照时重命名文件返回 500”，完整发布写入链路未验收。这是**待复现的演示阻塞项**，不能直接认定根因或沿用其他批次通过结论。

## 3. 并行开发边界

| 工作 | 后端负责 | 前端负责 |
| --- | --- | --- |
| 账号、会话、Cookie、来源/CSRF 校验 | 是 | 只按契约消费 |
| 管理 API 保护及可信 actor | 是 | 不靠隐藏按钮代替服务端鉴权 |
| 登录页、导航身份、退出入口、路由守卫 | 不修改 | 是 |
| Vue 请求层、冻结/发布的密钥框替换 | 不修改 | 是 |
| `script/test-local.ps1`、`tests/e2e/local/support/net.ts` 等公共测试基础 | 是 | 不并行修改；页面专项可用独立 helper |
| 新页面专项测试 | 不修改前端文件 | 是，放独立 auth 页面规格 |
| 快照重命名 500 | 复现并在直接影响的后端范围内修复 | 提供操作条件、原始状态及错误；不修 Go |
| 最终合并 | 后端担当集成执行者，取得双方稳定文件清单后操作独立集成工作树 | 交付 UI 基线及本轮增量，处理 Vue 冲突 |

后端只在已有独立工作树中实施，不修改主工作区 Vue，不切换/reset/stash 主工作区，不动现有 `18080` 和 `3000` 预览及其数据库。隔离服务继续使用 `127.0.0.1:18081`、独立 SQLite/上传/快照目录、Cookie `rb_admin_auth_dev`。

前端规划分支为 `codex/admin-login-ui`，另建工作树，实际目录以创建工具返回值为准；本文件不表示已经创建。前端 dev server 用 `127.0.0.1:3001`，通过 Vite 代理访问 `18081`。后端隔离配置精确允许 `http://127.0.0.1:18081` 与 `http://127.0.0.1:3001`，保留浏览器真实 Origin；不允许任意来源。不同端口不会隔离 Cookie，页面与 HTTP 测试使用不同浏览器上下文/Cookie jar。

`18081` 实例由后端维护；重建测试库或重启前不要打断已开始的前端联调。自动化回归另起空闲端口和临时目录，不复用联调库。两边不直接编辑对方工作树。

## 4. 固定接口契约

### 4.1 三个演示接口

所有路径相对 BasePath；部署前缀为 `/rainbow-bridge` 时，实际登录地址是 `/rainbow-bridge/api/v1/auth/login`。前端只给现有 URL 构造器传 `/api/v1/auth/login`，不得重复拼前缀。

| 方法与路径 | 请求 | 成功结果 |
| --- | --- | --- |
| `POST /api/v1/auth/login` | JSON `{username,password}`；浏览器提供合法 Origin | 200，设置 HttpOnly 会话 Cookie，返回下方身份结构 |
| `GET /api/v1/auth/me` | 有效 Cookie | 200，同下方身份结构；无有效会话为 401 |
| `POST /api/v1/auth/logout` | JSON `{}`；Cookie、合法 Origin；有效会话还需 CSRF | 200，撤销当前会话并清 Cookie，返回 `data.logged_out=true`；已失效时重复退出同样成功 |

登录/me 的成功响应固定为：

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

不在 JSON 返回会话凭证，`user.id` 为字符串，时间为 UTC RFC3339；CSRF 与会话凭证不同。Cookie 与来源配置沿用已实现规则，本机 HTTP 显式允许回环开发；Cookie Path 与 BasePath 一致，退出删除属性一致。接口返回 JSON 和真实 HTTP 状态，鉴权及管理响应 `Cache-Control: no-store`。

普通管理 POST 自动携带 `X-CSRF-Token`，包括上传、导入预览和导出。用户不手动填写；CSRF 校验失败不产生业务副作用。已有同会话稳定 CSRF、默认空闲 2 小时/绝对 8 小时继续使用，不新增“记住我”、永久会话或超时设置界面。me 和合法管理请求自动续空闲期限，但不超过绝对期限；公开 runtime 不续期。

### 4.2 前端需要区分的错误

| HTTP / error_type | 前端约定 |
| --- | --- |
| 401 `AUTH_INVALID_CREDENTIALS` | 登录表单提示“账号或密码错误”，不进入全局重登循环 |
| 401 `AUTH_REQUIRED` | 管理/me 请求需要重新登录，不自动重放写操作 |
| 403 `AUTH_FORBIDDEN` | 账号不可用，清前端管理状态并提示 |
| 403 `AUTH_CSRF_INVALID` / `AUTH_ORIGIN_DENIED` | 展示失败，不自动重试原业务操作 |
| 429 `AUTH_RATE_LIMITED` | 读取 `Retry-After`，提示稍后再试 |
| 503 `AUTH_UNAVAILABLE` / `AUTH_BUSY` | 显示暂不可用和重试入口，不当成密码错误 |
| 400/413/415 `AUTH_INVALID_REQUEST` | 展示输入、体积或格式错误 |

错误体示例：`{"code":401,"error_type":"AUTH_REQUIRED","msg":"请先登录"}`。HTTP 200 不能搭配认证失败，API 不返回 HTML 登录页或重定向。

### 4.3 已实现但不纳入演示界面的能力

已有 `/api/v1/auth/password` 及本机恢复命令保留，不新增页面，不把它们列为本轮演示路径。密码接口继续由自身会话/CSRF规则保护，不作为公开白名单整段放行。当前改密响应实际字段为 `reauth_required`，原文档曾写 `reauthentication_required`，存在差异；本轮前端不调用该接口，不为此扩大接入范围。正式启用改密功能前再统一该字段和接口测试，当前交付中明确注明。

管理员初始化继续复用现有 `RAINBOW_ADMIN_USERNAME`、`RAINBOW_ADMIN_INITIAL_PASSWORD` 及已有密码规则，不再设计另一套演示账号。首次凭据由演示负责人设置，不提供固定内置密码；已存在账号不因启动环境变化重置。密码不写进源码、PRD、YAML、测试输出或提交。

## 5. 后端剩余实施任务

### B1. 接通管理鉴权

Hertz 中间件验证数据库会话，并创建服务端可信管理员上下文。旧 `X-User-Id`、自报 role、旧 Bearer 密钥不能授予管理权限。管理生产服务入口使用统一身份检查；启动种子和内部 DAO 保持清楚的内部调用路径。不能把 runtime 依赖的共享读取方法一并设为必须登录，导致公开业务接口间接 401。

| 路由范围（除 ping 外均带 `/api/v1/`） | 访问要求 |
| --- | --- |
| environment/pipeline/config 的 list、detail、create、update、delete | 登录 |
| asset/list、asset/upload | 登录 |
| freeze/status、history、lock、unlock | 登录 |
| release/list、detail、active、history、create、activate | 登录 |
| transfer/export-tree、export、import-preview、import、import-selective、migrate | 登录 |
| GET runtime/overview、runtime/config、runtime/static、runtime/release、runtime/release/static（实际已注册者） | 保持公开业务读取 |
| GET asset/file/合法资源、release/asset/合法资源 | 保持公开下载，原路径及完整性校验保留 |
| GET /ping、version、version/latest；前端页面与静态文件 | 保持公开 |
| auth/login、me、logout、password | 按各自接口契约，不能整体豁免 auth/* |

白名单按方法和实际注册路由精确列举，禁止使用“放行所有 GET”“runtime/* 都公开”等宽泛规则。未知 API 保持 404 JSON。未授权在读取上传表单/落盘/事务副作用前拒绝。后台读取不是公开 runtime 的别名。

冻结/发布服务移除对旧管理密钥的依赖，actor 使用当前 username；保留锁版状态、原因、revision、写闸门、request_id 和快照不可变规则。`admin_configured` 保留字段，含义改为“存在可用管理员账号”；数据库失败不能伪装可用。

### B2. CORS 与会话续期

管理/auth 仅对精确可信 Origin 返回允许来源与凭据，并加 `Vary: Origin`；正确允许 `X-CSRF-Token`。公开业务接口保持原 CORS 使用边界。已有版本 ZIP 的 `Content-Disposition`、`X-Rainbow-Snapshot-Id`、`X-Rainbow-Version` 暴露头必须保留。

普通管理请求使用已有原子会话续期逻辑，不让撤销/过期会话复活。保留已有登录并发保护和脱敏修复，不再添加演示无关的认证框架。

### B3. 测试适配及演示链路阻塞

后端统一维护 `script/test-local.ps1`、公共网络 helper 及已有需要登录的测试调用：独立 Cookie jar 登录 → 读取 CSRF → 管理请求自动附加。未登录/错误凭据测试必须有明确的匿名请求通道，不能被“自动登录 helper”掩盖；公开 runtime 测试也必须实际匿名执行。浏览器页面测试需要在自己的 browser context 中登录，Node fetch 的 Cookie 不会自动出现在浏览器。

先用独立库和一个资源构造“冻结→创建快照”，复现前端记录的重命名 500；记录实际路径、原始错误、是否仅特定平台/夹具触发。确认属于直接链路缺陷后做最小修复并补回归；若无法复现，不猜测为已修，最终集成实例重新验证。不得以统一吞错、删除快照、忽略缺资源或回退编辑区来通过演示。

### B4. 为外部 IAM 保持一个替换入口

只保留一个清楚的“HTTP 请求验证 → 可信身份上下文”边界，以及业务服务读取身份的辅助方法。冻结、发布、配置服务不解析 Cookie、密码或未来 IAM token。以后接入 IAM 时替换该入口及登录流程即可；本轮不设计 provider 注册、策略引擎、角色映射配置或 OIDC 协议。

## 6. 够用的验收标准

保留已有测试，新增验证只围绕本轮改动。无需补齐原长版 A01–A22 的所有子项，也不要求 MySQL/PostgreSQL、K8s、MinIO、集群、真实 IAM 或移动端验证。

| 编号 | 实际操作 | 通过标准 |
| --- | --- | --- |
| D-B01 | 空库初始化、登录、me、同库重启 | 登录/me 契约正确；刷新恢复所需信息完整；重启不重置账号 |
| D-B02 | 枚举所有管理路由，无 Cookie/伪造 X-User-Id/旧密钥 | 一律拒绝，DB/资源/快照/历史无副作用；生产服务入口也不能直接绕过 |
| D-B03 | 有效 Cookie，缺/错 CSRF或非法 Origin；合法组合 | 非法拒绝，合法管理请求成功；含上传、导出等不同请求形式 |
| D-B04 | 正常退出及重复退出，随后用原 Cookie 请求管理接口 | 退出幂等，原 Cookie 失效；已有撤销、到期规则保持 |
| D-B05 | 控制时钟模拟管理活动与到期 | 活动滑动空闲期限；绝对期限不变；不靠页面轮询保活 |
| D-B06 | 登录后编辑→冻结→创建→切换→匿名 runtime/资源读取 | 锁版仍挡修改，发布成功，匿名默认/指定版本及资源正确，actor 为用户名 |
| D-B07 | 前端来自 3001 经代理调用18081，根路径与/rainbow-bridge路径构造检查 | Cookie/Origin/CSRF 正常，无双前缀；ZIP 暴露头保留 |
| D-B08 | 密码错误及认证内部错误，采集日志/响应 | 无密码、会话凭证、摘要、CSRF或SQL内部细节；复用已有脱敏测试 |
| D-B09 | 原冻结/发布/主套件适配后运行 | 业务断言保持；原密钥只作为负例出现；测试不污染18080 |

执行 Go 测试与 vet，命令工作目录为后端独立工作树：

```powershell
go test -mod=readonly -tags=dev -count=1 ./...
go vet -mod=readonly -tags=dev ./...
```

随后跑受影响的 Freeze、Release、Full 套件。若部分旧页面规格仍依赖密钥输入，后端先交付 API 验证结果，页面规格由前端在集成阶段适配后补跑；不能删除业务断言来宣称全绿。每项报告实际运行范围，不要求为已有、未改行为重复造测试。

## 7. 集成顺序与交付

1. 后端继续 B1–B3，前端同时写登录页和请求接入；不因为尚未启动联调而停止独立工作。
2. 前端交付原光谱 UI 稳定基线及登录增量；后端交付后端/公共测试增量和接口验证结果。仅文档任务不自动执行提交、推送或发消息。
3. 实施时建立独立集成工作树，规划分支 `codex/admin-login-integration`。有稳定提交时按明确提交合并；只有工作区改动时用审核后的路径清单和增量补丁，不能整包复制/覆盖。
4. 集成还需保留主工作区已有首页生效版本、runtime/release/static 和 ZIP 相关改动。旧后端基线没有这些新增内容，单跑后端分支通过不能代替合并后验收。
5. 后端负责 Go/CORS/生成路由冲突，前端负责 Vue 冲突；合并后由后端维护隔离实例，前端执行页面验收，走通完整演示链。现有18080实例待后续切换指令再更新。

交付摘要只说明：做了什么、怎么演示、实际测试、剩余问题、双方文件归属。测试凭据、Cookie jar、临时库、二进制、缓存及 `script/_add_login_pause_hook.mjs` 一类临时编辑脚本不提交。主工作区用户配置/启动脚本及其他批次改动原样保护。

研发必须使用 `$engineering-quality-preserving-refactor`，读取 `C:/Users/MalfuRisa/.codex/skills/engineering-quality-preserving-refactor/SKILL.md`；遵守 AGENTS.md，生成模型/路由经 hz 生成，不手改。按职责拆分会话、请求校验、服务身份辅助逻辑，不为这次演示做全栈重构。

完成后再在桌面产出目录写最终工作记录；本轮不新增阶段修复报告。本文原长版要求已由本版明确收敛，不能继续按旧清单无限扩展账号功能。

## 8. 下一步协作安排（2026-10-20）

本节补充执行顺序，不扩大演示登录范围。当前实测：18080、3000、3001监听；18081未监听。前端工作树已存在：`C:/Users/MalfuRisa/.codex/worktrees/admin-login-ui/rainbow_bridge`，分支 `codex/admin-login-ui`；已有登录页面、请求接入及Mock规格，尚未真实联调。

### 第一步：后端先提供稳定联调实例，同时前端继续Mock

后端负责启动并保持 `http://127.0.0.1:18081/rainbow-bridge/`。使用独立配置、固定联调数据库/资源目录、Cookie `rb_admin_auth_dev`，精确允许18081与3001的Origin。重启沿用同库和首次初始化账号，不每轮生成新账号覆盖旧库；自动回归另用临时目录和空闲端口。

不能把 `test-local.ps1` 当作常驻联调服务：它在退出的finally中执行 `Stop-AllInstances`，用例跑完服务便停止。提供一个最小独立启动入口或使用现有等效启动方式，记录地址、PID、配置路径、日志路径和停止方式，不建设进程管理平台。

联调凭据沿用 `RB_QA_ADMIN_USERNAME`、`RB_QA_ADMIN_PASSWORD` 作为测试进程输入；首次空库初始化将它们在内存映射为服务端 `RAINBOW_ADMIN_USERNAME`、`RAINBOW_ADMIN_INITIAL_PASSWORD`。变量名不是变量值，也不会自动跨Codex进程共享。由持有凭据的后端启动进程在运行真实页面测试时，将变量传给前端工作树的测试子进程；不发密码到聊天，不放入Vite变量/网页包、报告、命令参数或日志。

实例交接前先实际证明：ping成功；登录200；同一Cookie jar调用me为200；未登录管理请求401；带Cookie/CSRF的最小管理请求成功；公开runtime可匿名访问。然后只交接非敏感连接信息及测试变量名。18081保持运行，重建库须先结束正在进行的联调。

### 第二步：先修测试登录入口，再判断Cookie问题

已发现 `support/pages.ts` 的登录准备只挂在 `gotoPage()`，但 `release-page.spec.ts` 和多处 `release-resilience.spec.ts` 直接调用 `page.goto()`；`freeze-import.spec.ts` 也有直接导航。这些调用不会执行该helper。先在明确需要管理员的页面测试fixture/beforeEach中完成登录，并覆盖直接导航；匿名/登录页测试必须使用独立、不自动登录的fixture。

每个用例在导航前先用与浏览器共享Cookie的请求上下文调用me并断言200，再打开管理页面。不以“context里存在同名Cookie”判定登录成功；同主机不同端口的多个实例不得共用一个Cookie上下文。登录返回Set-Cookie后优先让浏览器上下文自然接收，避免一边自动接收一边手工重复注入；若确需storageState，先验证它对应当前实例且会话有效，不盲目更换Cookie API。

诊断只保留：目标URL、Origin、Cookie名称/Path/Secure/SameSite、是否携带Cookie、响应状态及error_type；不记录Cookie值、CSRF或密码。依次比较：直接18081登录/me → 浏览器上下文me → 3001代理后的me/管理请求。由第一处失败确定是实例、上下文、代理还是业务处理问题。

### 第三步：后端收尾自己负责的缺口

- 把 `publicRoutes` 当前的 `HasPrefix` 规则收紧：version不能连带豁免其他version前缀路由，runtime只列实际公开接口，auth逐项按方法处理。不把当前实现称为“精确匹配已完成”。
- 完成管理/auth精确来源CORS与实际路由枚举验证，保留公开runtime和ZIP头。
- 后端分支新增的 `vue/src/lib/api/client.ts` CSRF补丁仅为临时验证，最终以独立前端分支的统一实现为准。比对后只保留后端/公共测试改动，不能用git整文件还原或覆盖前端版本。安装造成的yarn.lock漂移逐项确认，非必要不带入。
- 先跑一个“登录→列表→退出→401”的页面冒烟，再跑发布完整链路；最后跑受影响的Freeze/Release/Full。不要在基础登录失败时反复整套运行。快照重命名500按真实集成实例复现，仍存在则做直接范围内最小修复。

### 第四步：两边就绪后集成

前端继续使用自己的3001与Vue实现，后端提供稳定18081及公共测试登录fixture。前端提供带真实模式的页面测试入口，后端在持有凭据的进程中启动它；前端不必等待拿到密码才能继续编码。

真实页面验证通过后，再到独立集成工作树合并UI基线、登录前端、登录后端以及主工作区已有首页生效版本/ZIP改动。Go/CORS/测试公共层由后端处理，Vue由前端处理。最终演示链跑通前不更新18080，不把HTTP或Mock单独通过称为完整交付。
