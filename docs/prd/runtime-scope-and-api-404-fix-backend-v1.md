# 修复旧运行时范围固定与未知 API 错误格式：后端 PRD

日期：2026-10-26。状态：设计完成，待实施。本轮仅交付 PRD，不代表缺陷已修复。

## 1. 问题、目标与证据

实时接口验证 RT01–RT30 已实际执行，28 项通过、2 项失败。本次只修这两个确认缺陷，不重写已经通过的发布、资源和鉴权功能。

| 缺陷 | 看到什么 | 为什么需要修 |
| --- | --- | --- |
| D1 / RT23 | 旧 runtime/config、runtime/static 声明接受 scope_id，却忽略错误身份并返回200配置/ZIP | 调用方以为固定某一代范围，实际上只按名称取当前对象；同名重建后可能误读新代 |
| D2 / RT24 | 未知 API 在测试实例返回404 text/plain，正文Not Found | 业务按约定解析JSON错误时失败，不能稳定按error_type分类 |

原始运行：`D:/kylin/rainbow_bridge/.local-qa/runtime-business/20261026-154450-71530d/artifacts/summary.json`，分别见 evidence/RT23.json、RT24.json。复现与完整矩阵：[实时接口验证 PRD §11](runtime-business-api-verification-v1.md)。

本轮额外核对到D2的入口差异：`main.go` 已有 writeUnknownAPI，但它只在 setupStaticFileServer 的通配处理与 NoRoute 中调用，且尚无 error_type/no-store；`--no-frontend` 不调用该初始化。测试 instance.mjs 明确带这个参数，因此落到了 Hertz 默认纯文本404。不能笼统声称所有启动模式都没实现JSON404，也不能只修静态页面模式。

完成目标：旧接口真正兑现可选scope身份固定；纯API与完整应用模式都能对未知API返回统一JSON404；所有原通过的业务读取仍正确。

## 2. 工作范围与基线

仓库 `D:/kylin/rainbow_bridge`，编写时 `main_v1`、已提交HEAD `c9aefe5`，工作区包含后续未提交功能及测试。实施前记录真实状态，以当前已完成成果为基线，不从旧HEAD覆盖回来。

允许改：runtime手写handler/adapter、必要的release只读范围解析与active静态导出入口、main启动与HTTP兜底组织、相关测试及API文档。既有IDL已经声明scope_id，原则上无需变更协议字段。

不改Vue、不新增数据库表或备份schema、不改变自动编号/比较/CAS/副本保留、不扩大公开读取范围、不改变Cookie/CSRF、不更新用户演示实例。若用独立工作树，建议分支 `codex/runtime-scope-api404-fix`（未创建），先明确未提交依赖如何整合；不共享目录切分支、不全仓add、不还原其他窗口内容。

## 3. D1：旧接口保留原用法，真正支持scope固定

### 3.1 请求与成功兼容

| 端点 | 环境/渠道来源 | 新增生效的已有参数 |
| --- | --- | --- |
| GET `/api/v1/runtime/config` | x-environment、x-pipeline Header | query scope_id |
| GET `/api/v1/runtime/static` | query environment_key、pipeline_key | query scope_id |

省略scope_id、空串或trim后为空继续按当前有效范围读取，和现有可选读取入口保持兼容。非空值必须是合法UUID并执行身份校验；不能再静默忽略。

```http
GET /rainbow-bridge/api/v1/runtime/config?scope_id=<A-scope-uuid>
x-environment: <A-env>
x-pipeline: default
```

```http
GET /rainbow-bridge/api/v1/runtime/static?environment_key=<A-env>&pipeline_key=default&scope_id=<A-scope-uuid>
```

正确scope返回原配置结构/原业务静态ZIP。旧端点仍只跟随本次捕获的active，不支持version、expected_snapshot_id、content_format；明确提交这些保留参数仍400，不能顺手变成固定版本接口。

scope_id固定的是**范围这一代身份**，不是某个发布快照。范围未删除时切active，正确scope的旧接口仍应读新active；需要固定版本继续用runtime/release及固定静态接口。

### 3.2 错误合同与优先级

保留原“缺环境/渠道、保留参数非法”的校验；随后校验非空scope UUID，合法UUID才查身份。仅scope不正确时按下表处理，不返回ZIP附件头或配置data：

| 场景 | HTTP / error_type |
| --- | --- |
| 非空非法UUID | 400 `SCOPE_INVALID_REQUEST` |
| 合法UUID但scope从未存在 | 404 `SCOPE_NOT_FOUND` |
| scope存在且仍有效，但不是请求环境/渠道的当前代 | 409 `SCOPE_IDENTITY_CONFLICT` |
| 指定scope已经删除，包括同key重建后的旧ID | 410 `SCOPE_DELETED` |
| 正确scope但没有active | 404 `RELEASE_NOT_ACTIVE` |
| 指针、快照、清单或资源不完整 | 原503/对应release错误，不回退编辑区 |
| 数据库真实异常 | 原内部错误，不伪装成scope不存在 |

错误沿用JSON `{code,msg,error,error_type}` 与真实HTTP状态，Cache-Control:no-store。不要删除IDL字段或改成“永远不支持scope”来让RT23不再失败。

### 3.3 校验必须贯穿读取，不能只在handler查一次

当前GetConfig调用RuntimeRelease而非WithScope；ExportStatic调用的ExportActiveReleasePackage也没有scope输入。修复这两条传递链：

1. config把query传入共用scope感知读取入口，保留原adapter响应。
2. static给active导出入口增加scope感知变体/输入；已有内部无scope调用可保留wrapper，复用现有捕获计划和ZIP写入器。
3. 共用只读scope解析：正确校验得到一个pipeline.ScopeID后，head必须通过GetHeadByScopeID读取，版本取该head的snapshot，核对version.ScopeID等于捕获scope。禁止校验后再按env/pipeline重新解析另一代。
4. RuntimeReleaseWithScope当前也有“校验后再次按key查scope/head”的路径；若为闭合这次身份固定提取共享小方法，应让旧/新入口复用，限制在只读身份捕获，不泛化成全仓重构。
5. 捕获完成前发生同名删除重建，要么明确拒绝，要么基于一致捕获返回原代；不能返回新代。捕获完成后开始构包的在途旧请求允许完成，不能把此规则当作删除后的新请求可继续读取旧对象。
6. 资源URL、包内配置、manifest与X-Rainbow-*响应头均来自一次捕获的同一快照；构包中途切active不重新取新指针。192MiB/60秒、SHA校验和临时文件清理继续保持。

UUID校验和身份错误可在同service包共享，避免两个handler各写一份规则。对DAO错误用errors.Is区分NotFound与真实失败；读取不新增写闸门、head初始化、审计或缓存副作用。

## 4. D2：统一未知API的JSON404

### 4.1 对外合同

对真正未注册的API路径，不论纯API模式、完整页面模式、是否存在静态文件，均返回：

```json
{
  "code": 404,
  "msg": "not found",
  "error": "API endpoint not found",
  "error_type": "API_NOT_FOUND"
}
```

HTTP404，Content-Type:application/json（可带charset），Cache-Control:no-store。不回显原始URL、query、请求体或服务器路径；不返回200、HTML或纯文本。

- GET/POST等未注册API方法+路径组合按既有框架路由策略返回此404；不新增广泛405改造。已注册handler自行返回的404、401/403、资源二进制与业务error_type原样保留，不能全局把所有404改为API_NOT_FOUND。
- HEAD未知API返回404与相同JSON类型/no-store响应头，但正文为空。
- OPTIONS保留原CORS预检合同（可204），不机械变成404；管理鉴权和来源限制不变。
- API_NOT_FOUND是路由缺失，不是RELEASE_VERSION_NOT_FOUND、SCOPE_NOT_FOUND或资源丢失，客户端可明确区分。

### 4.2 注册时机与路径分类

API兜底必须在HTTP实例初始化时独立注册，不依赖setupStaticFileServer成功、FRONTEND_DIR或index.html存在；`--no-frontend`也必须生效。

统一一个路径分类与响应小方法供NoRoute、静态通配回退使用。NoRoute不能被静态初始化覆盖回另一套逻辑，不能加一个抢占已注册业务路由的高优先级API通配handler。

路径分类只认路径段边界：

- 根路径部署：`/api`、`/api/...`属于API；`/apix`不是API。
- 配置BasePath时：规范的`<BasePath>/api`与其子路径属于API；去前缀必须按完整段匹配，不能把`/rainbow-bridge-other`当成同一前缀。
- 错误地重复配置BasePath后接API的路径，如`/rainbow-bridge/rainbow-bridge/api/v1/...`，也应404 JSON，不能当SPA页面200；可以只为**分类错误路径**识别重复前缀，但绝不能替客户端修正后执行真实API、重定向或读取配置。
- 原始根级`/api/...`没有命中部署前缀下的任何有效路由时也按未知API404，不隐式转发到带前缀接口。
- 不用“路径中只要含api就算API”的宽泛判断，不改变`/docs/api-guide`等普通页面路由语义。

完整应用模式的正常页面如`<BasePath>/config`、登录页和页面刷新仍走已有SPA；真实JS/CSS/图片文件按原MIME输出，缺失静态文件保留原404，不能返回伪JS的index.html。纯API模式非API未知路径可保留原普通404，本次不要求所有站点路径都JSON。

### 4.3 代码组织建议

建议将共同API错误分类/响应与路由兜底组织放package main的独立`http_fallback.go`或等价小文件。main只负责装配；不继续扩充一个包含全部静态文件细则的大函数。

既有writeUnknownAPI可改为共享显式JSON实现。`static_routes_test.go`目前只检查404/非空，未验证Content-Type、JSON解码和error_type，需要补实质断言；且不能只有dev构建下的静态测试，必须覆盖no-frontend启动路径。

## 5. 编码与兼容边界

研发读取并使用 `$engineering-quality-preserving-refactor`：`C:/Users/MalfuRisa/.codex/skills/engineering-quality-preserving-refactor/SKILL.md`。

- handler只负责解析/映射，service负责范围身份与一次快照捕获，构包复用原实现；不在两个旧handler复制整套release逻辑。
- 若确需修改IDL，必须hz生成，不手改biz/model/*.pb.go与biz/router；现有scope_id声明无需删除或改名。
- 不新增鉴权、缓存或重试策略；匿名runtime继续公开，管理接口继续受保护。scope_id是对象身份条件，不是授权凭据。
- 保留前端正在实施的自动编号与任意版本比较，以及独立active_revision查询PRD的修改边界，不相互覆盖文件。
- 更新API文档说明旧接口scope_id生效及API_NOT_FOUND，并保留Fork现有结构。不新增首页接口展示或设计页面。

## 6. 必须通过的测试

| ID | 测试输入/时序 | 预期 |
| --- | --- | --- |
| F01 | 两个旧接口省略scope | 成功与原合同一致；默认跟随active，不读草稿 |
| F02 | 两个旧接口传正确scope、空值/空白 | 正确pin成功；空值保持无pin兼容，资源/ZIP字节正确 |
| F03 | A参数+B有效scope、未知合法UUID、非法UUID | 分别409身份冲突、404不存在、400非法；静态错误不是ZIP且无下载成功头 |
| F04 | 旧scope删除后同key重建 | 旧pin410，新pin只读新代；新代未发布404，不继承旧版本 |
| F05 | 身份校验与head捕获间插入删除重建 | 确定性时序夹具证明旧pin永不返回新代；成功响应范围/版本/快照自洽 |
| F06 | 构包捕获后切版 | 包与响应头仍对应捕获快照；无pin下一次请求才能读新active |
| F07 | 正确scope无active、资源缺失、保留选择参数 | 原404/503/400保持，无回退或成功假包 |
| F08 | 纯API/完整应用 × 根路径/带前缀 | 未知GET、POST均404可解析JSON、API_NOT_FOUND、no-store |
| F09 | HEAD、OPTIONS、重复前缀及前缀相似字符串 | HEAD无body；OPTIONS合同不变；双前缀API404JSON且不执行真实handler；相似路径不误识别 |
| F10 | 已注册API返回业务404、合法资源下载、匿名管理请求 | 业务error_type/二进制和401/403原样保留，不被路由兜底改写 |
| F11 | 正常SPA刷新、登录页、JS/CSS、缺失静态文件、静态目录不可用 | 正常页面/文件不退化；API404仍独立可用，不因缺index退回纯文本 |
| F12 | 注入DAO真实失败、取消/导出失败 | 不冒充scope404，临时文件和响应错误按原合同处理 |

以真实HTTP补跑RT23/RT24并强化断言：不能只检查“不是200”，而要逐条检查预期状态、error_type、JSON/MIME及没有下载副作用；RT24不能仅检查body非空。RT27根路径未知路由也要明确JSON，不继续保留“纯文本另记”的观察作为通过依据。

修复完成后重跑整套30项（`powershell -NoProfile -ExecutionPolicy Bypass -File script/test-runtime-business.ps1`），确认原28项无回归，并核对RT23/24已转通过。每轮使用新run_id，保留旧28/30失败证据，不覆盖或追改原报告成“当时全部通过”。

运行受影响Go service/handler/main及路由鉴权测试、vet，覆盖dev和普通构建的API兜底。实际no-frontend/完整模式HTTP启动都用隔离库和端口，不仅单独调用写JSON函数。根路径和带前缀均验证，不能只凭单元测试宣称真实链路已修好。

不扩展K8s、多数据库、热恢复或压力验收承诺；MySQL/PG、race未跑时明确登记。

## 7. 实施顺序与交付

1. 用原最小反例建立失败回归，核对实际当前文件，保留其他窗口改动。
2. D1补scope输入链路和scope_id绑定的head捕获，验证正确/错误/同名重建与并发边界。
3. D2统一独立API兜底，再接入完整应用静态回退，验证no-frontend根因与页面兼容。
4. 运行针对性测试和新一轮30项，补文档，在本PRD末尾登记真实结果、剩余风险和实际运行目录。

回传应分别说明两个缺陷改了哪里、旧合同如何保持、真正执行的HTTP/回归及未执行项；不要以“已经调用ValidateReadScope”或“body看起来像JSON”代替行为证据。本轮无须新建前端任务，也不另写每轮修复报告。

当前授权仅到PRD。后续研发实施不默认获得提交/推送、替换演示实例或改用户业务数据的权限。

## 8. ZCode 实施结果（2026-10-26）

### 8.1 D1：旧接口真正固定 scope 代身份

- 新增共享 `resolveReadScope`：无 pin 捕获当前 live pipeline；有 pin 先查该 scope 的完整生命周期，再要求它等于当前代。验证与后续读取使用同一个返回的 pipeline，不再先校验再按 key 重新解析。
- `runtime/config` 改为 `RuntimeReleaseLegacy`：`scope_id` 空/空白按无 pin；非空先校验 UUID，再走共享身份解析；head 用捕获到的 `scope_id` 查，版本必须同属该 scope。
- `runtime/static` 改为 `ExportActiveReleasePackageWithScope`：同样先捕获 scope，再用 `GetHeadByScopeID` 固定一次 active；快照、config.json、manifest 和 X-Rainbow-* 头全部来自这次捕获。
- 固定版本导出（新接口）也改为先捕获 scope，再按 `(scope_id, version)` 取版本；不再在身份校验后按 key 重新解析。
- 错误保持：非法 UUID 400 `SCOPE_INVALID_REQUEST`、未知 404 `SCOPE_NOT_FOUND`、错配 409 `SCOPE_IDENTITY_CONFLICT`、已删除/同名重建旧 ID 410 `SCOPE_DELETED`、正确代无 active 404 `RELEASE_NOT_ACTIVE`、真实 DB 异常不再伪装成 404。
- 测试钩子 `releaseRuntimeScopeHookForTest` 在身份捕获后、head/版本读取前插入删除重建，证明旧 pin 不会在捕获后改读新代。

### 8.2 D2：统一未知 API JSON 404

- 新增 `http_fallback.go`：`isAPIPath` 按完整路径段分类 `/api`、正确 BasePath 前缀、重复 BasePath 前缀；`writeAPI404` 统一输出：
  ```json
  {"code":404,"msg":"not found","error":"API endpoint not found","error_type":"API_NOT_FOUND"}
  ```
- `main` 在静态初始化之外独立注册 `setupHTTPFallback`，`--no-frontend` 同样生效；静态 `GET/HEAD /*filepath` 与 `NoRoute` 共用同一分类/响应。
- HEAD 返回 404、JSON Content-Type、`Cache-Control:no-store`、零字节；Hertz NoRoute 的空 404 兜底会用 text/plain 覆盖，因此使用空 body stream 保持 JSON 类型。OPTIONS 保持 204/CORS 预检语义。
- 重复前缀不执行真实 handler、不重定向；`/apix`、`/apiary`、`/docs/api-guide`、`/rainbow-bridge-other/api` 不误判；缺失静态文件仍普通 404；正常 SPA、JS/CSS 保留。

### 8.3 F01–F12 结果

| ID | 结果 | 证据 |
| --- | --- | --- |
| F01 | pass | 旧 config/static 省略 scope 仍跟随 active；现有 handler 回归和 30 项 RT01–RT18 通过 |
| F02 | pass | 正确 pin/空串/trim 空白均有测试；config 返回原结构，static ZIP 字节/头正常 |
| F03 | pass | A 参数+B scope 409、未知 UUID 404、非法 UUID 400；静态错误为 JSON，无 ZIP/Disposition |
| F04 | pass | 删除并同 key 重建后旧 pin config/static 均 410，新 pin 404 无 active，不继承旧版本 |
| F05 | pass | 捕获后删除重建钩子：旧 pin 读取/静态构包均不返回新代，也不返回旧数据 |
| F06 | pass | 固定构包计划后切换 active，ZIP 仍为旧快照；下一次无 pin 请求读新 active |
| F07 | pass | 正确 pin 无 active 404；资源缺失/不完整 503；version/expected_snapshot/content_format 仍 400 |
| F08 | pass | 真实 HTTP：no-frontend/full × 根路径/前缀，未知 GET/POST 均 404 JSON `API_NOT_FOUND`、no-store |
| F09 | pass | HEAD 404 JSON 空 body；OPTIONS 204；双前缀/根级 `/api` 404 JSON 且不执行真实 handler；相似路径不误判 |
| F10 | pass | 已注册 `/api/v1/version` 200；既有业务 404/401/资源二进制测试无回归 |
| F11 | pass | 完整模式正常 SPA 200、JS/CSS 200、缺失 JS 404、缺 index 时 API 兜底仍 JSON；页面未被 API 兜底改写 |
| F12 | pass | 取消 context 的运行时读取返回内部错误而非 scope 404；静态导出超时仍 RELEASE_EXPORT_TIMEOUT，临时文件清理不变 |

### 8.4 真实 HTTP 启动矩阵

使用隔离 SQLite、端口 18160–18164，未使用 18080/18081：

| 模式 | 未知 API | HEAD | OPTIONS | 双前缀 | 相似路径 | 正常页面/资源 |
| --- | --- | --- | --- | --- | --- | --- |
| no-frontend + `/rainbow-bridge`（18160） | 404 JSON | 404 JSON 空 body | 204 | 404 JSON | 普通 404 | 已注册 API 200 |
| full + `/rainbow-bridge`（18161） | 404 JSON | 404 JSON 空 body | 204 | 404 JSON | `/rainbow-bridge-other/api` 回 SPA 200 | `/config` 200 HTML、JS/CSS 200、缺失 JS 404 |
| no-frontend + 根路径（18162） | `/api` 404 JSON | 404 JSON 空 body | 204 | 非本部署路径普通 404 | `/apiary`、`/docs/api-guide` 普通 404 | 已注册 API 200 |
| full + 根路径（18164，dev + 根路径前端夹具） | 404 JSON | 404 JSON 空 body | 204 | 非本部署路径回 SPA | `/apix` SPA 200 | `/config` 200 HTML、JS/CSS 200、缺失 JS 404 |

常规嵌入前端二进制在根路径启动时，`index.html` 仍引用 `/rainbow-bridge/assets/...`，这是前端构建期 BASE_PATH 的既有部署约束；API 兜底和 SPA 本身正常。为验证根路径完整应用的 JS/CSS，使用了 dev 构建和根路径前端夹具（18164），不是改产品去猜客户端前缀。

### 8.5 30 项复测与旧证据

- 最终运行：`20261026-164954-1ba9ba`，**30/30 pass、0 fail、0 blocked、0 defects、564 个受控 HTTP 请求**。
- RT23：新 runtime/static pin 正确；旧 config/static 错 pin 均 **409 SCOPE_IDENTITY_CONFLICT**，`ignored=false`。
- RT24：未知 API 与双前缀均 404，JSON `API_NOT_FOUND`，不再 text/plain。
- RT27：根路径实例未知路由明确 404 JSON，原“纯文本另记”观察删除。
- 原 28/30 失败证据保留在 `20261026-154450-71530d`（以及更早 `20261026-154253-5ce719`），未覆盖或改写。
- 最终产物：`D:/kylin/rainbow_bridge/.local-qa/runtime-business/20261026-164954-1ba9ba/artifacts/summary.json`；该轮 RT23 旧 config/static 错 pin 均 409，RT24 未知/双前缀均 404 JSON，RT27 根路径未知路由 404 JSON。

### 8.6 回归与未验证项

- `go test ./...`：通过（含新增 main fallback、legacy scope pin、删除重建时序测试）。
- `go test -tags=dev .`：通过（静态 SPA/JS/CSS/API 兜底）。
- `go vet ./...`：通过。
- `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build`：通过。
- 未验证 MySQL/PostgreSQL、race、K8s 多 Pod、热恢复；本轮只确认 SQLite 单实例。
- 未修改 Vue、未提交/推送、未替换演示实例；无用户数据或备份被读取。
- 正常嵌入前端在根路径部署需要按空 BASE_PATH 重新构建前端；本轮未改构建发布流程，已在 8.4 明确登记。
