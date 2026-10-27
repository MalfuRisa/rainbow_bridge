# 生效状态查询独立返回 revision：后端修正 PRD

日期：2026-10-26。状态：设计完成，待实施。本轮只交付 PRD，不修改业务代码、不派发、不提交或部署。

## 1. 问题与事实边界

前端发布审阅要把真实 head revision 交给 active compare/activate，不能仅凭“没有生效版本”推断 revision 为 0。

当前 GET `/api/v1/release/active` 的 data 只有 `active`：有生效版本时 revision 位于 active 内；没有生效版本时只返回 `active:null`，丢失了实际 head revision。

本次核对依据：

- `biz/handler/release/release_service.go`：Active handler 的 releaseActiveDataJSON 仅含 Active。
- `biz/service/release_service.go`：loadActive 在 head 不存在或 ActiveSnapshotID 为空时都返回 nil；activeInfoFromHead 组装已有指针的元数据。
- `biz/dal/db/release_dao.go`：ClearHeadByScope 会清空指针并增加 revision，但其当前调用用于删除 scope；scope_delete 完成后旧身份访问应为 410，而不是有效范围的空 active 查询。
- 自动编号只改变 last_version_number，不推进 head revision；创建但从未激活的正常范围仍是 active:null、revision 0。
- 没有找到允许在有效范围上主动清空 active 的公开业务入口；不得为了本修正新增“清空生效版本”接口。
- 备份数据校验不以 active 为空就强制 revision 必须为 0；非零空指针可作为服务层/兼容边界状态构造，但尚无本轮真实正常业务路径复现证据。

结论：这是**已确认的响应表达缺口**，不是已证明发生在现有演示实例的发布故障。设计接受“有效范围、head 存在、指针为空、revision 非零”的读取表示，不自动修库、重置 revision 或宣称它是日常操作必然可达状态。

前端已在 `vue/src/lib/api/release.ts` 的 activeState 兼容可选 active_revision，`useReleaseComparison.ts` 会优先使用它；旧响应下只有正常首次发布能尝试 0，遇非零冲突即停止确认，不解析英文错误或猜下一个 revision。保持这一保护。

## 2. 范围与基线

仓库 `D:/kylin/rainbow_bridge`，编写时 `main_v1`，已提交 HEAD `c9aefe5`；大量后续功能及前端未提交。实施先记录实际状态，以已整合的自动编号、active/fixed 比较、scope 身份和备份成果为依赖，不用旧 HEAD 覆盖共享文件。

允许修改：手写 release service/DAO/handler、release IDL 及正规生成结果、相关测试、专业 API 文档。不得修改 Vue、编号分配、激活规则、历史版本、快照 payload/摘要或恢复策略。无新表、无数据迁移、无备份 schema 升级。

需要隔离时建议分支 `codex/release-active-state-revision`（未创建），独立工作树必须显式整合未提交依赖。不能在共享目录切分支、全仓 add、还原他人文件。本轮不启动/替换 18080、18081 等演示实例。

## 3. 最小 API 合同

沿用 GET `/api/v1/release/active?environment_key=...&pipeline_key=...&scope_id=...`，按实际 BasePath 挂载，新增 data.active_revision：

```json
{
  "code": 200,
  "msg": "OK",
  "data": {
    "active": null,
    "active_revision": "7"
  }
}
```

| 状态 | active | active_revision |
| --- | --- | --- |
| 有效范围，无 head 行 | null | 字符串 `"0"` |
| 有效范围，head 存在、空指针 | null | 真实 head.Revision 的十进制字符串，包括非零值 |
| 有效范围，head 指向有效版本 | 原 active 对象 | 与 active.revision 完全相同的字符串 |
| 查询失败、scope 不合法、指针引用损坏 | 原错误合同 | 不返回伪造的成功 null/0 |

字段始终存在：不使用 omitempty、不返回数字/null/空串，不从 last_version_number、事件数量或版本数量推导 revision。值在现有非负 int64 范围内，超过 JS 安全整数也用十进制原文；不经 float64 或 JS number。

保留原 active 对象字段及 active:null 语义，旧客户端继续可用。增加 `ReleaseActiveData.active_revision` 字符串字段并由 handler 显式输出。不是把 active 本身改成总有对象，也不是给空指针伪造 version/snapshot。

本轮**不扩展 CAS 错误响应**：冲突仍使用现有 error_type/latest_active，客户端重新调用此查询获取最新 revision，再重新比较、由用户确认。不从错误文字提取数字，不拿新 revision 自动重试激活。fixed compare 的 active_revision/target_is_active 继续为 null，和本接口的新字段属于不同响应合同。

## 4. 读取路径与一致性

建议新增一个职责单一的状态查询 service，例如 ReadReleaseActiveState，返回固定范围身份、head revision 和 nullable active；原 ReleaseActive/内部 loadActive 的调用方兼容保留，避免为了一个新增字段改写 create/activate/list 的全部调用关系。

推荐 DAO 用**同一条只读 SELECT**捕获：当前有效 environment/pipeline、LEFT JOIN 该 scope 的 head、LEFT JOIN head 引用的 version。通过可空 head 主键区分“没有 head”与“确实 revision 0”，不能对 SQL NULL/扫描失败一律默认 0。代码复用已有字段转换和错误分类，不再复制完整 runtime/payload 读取逻辑。

- optional scope_id 的格式与当前代校验沿用原合同；缺省仍按当前业务 key 查询。服务层也须绑定捕获到的 scope，不能只在 handler 先检查一次、随后按同名 key 读到重建的新代。
- 旧 scope、错配身份、已删除环境/渠道的错误语义保持；不能因为 LEFT JOIN 找不到有效父对象就当作正常首次发布。
- active metadata 与 revision 必须来自同一份捕获的 head。已绑定范围后优先使用 scope_id 查询，不再独立调两次 loadActive 和 GetHead 拼结果。
- 指向版本时校验版本存在、未删、与 head.ScopeID 一致，并保持既有元数据错误处理；缺版本/已删/跨 scope 引用返回 503 RELEASE_INCOMPLETE。head revision 为负等损坏状态也应报不完整，不能包装成合法字符串。
- 查询仅取状态元数据，不校验或读取全部快照文件；资源健康仍由原 compare/activate/runtime 各自合同处理。
- 如果实施选用多条查询而非上述单条 SELECT，必须说明并证明数据库一致快照边界。不能仅调用 Begin() 就假定 PostgreSQL READ COMMITTED 的多条 SELECT 必然读到同一视图；达不到一致性时保持单条捕获方案。
- 捕获后发生切换/删除允许响应表达捕获时状态，不能保证响应到客户端时仍最新；后续 compare/activate 的 scope 校验和 revision CAS 继续拒绝过期操作。A→B→A 虽指向相同快照，revision 变化仍应冲突。

查询不得执行 EnsureHead、计数回填、写闸门更新、修复 active 或新增审计。不要从全局缓存拿到另一时点 head/revision 的组合。

## 5. 鉴权、失败与兼容

- 此端点继续是管理 GET，要求现有 Cookie 会话，不因只读新增字段而放入公开 runtime 白名单。GET 不新增 CSRF 必填要求，Origin/CORS 按既有规则；管理写仍保留 CSRF。
- BasePath、Cache-Control:no-store、请求参数、HTTP code/error_type 不改变。
- DB/DAO 查询异常返回原内部错误，不吞掉为 active:null。scope 错误仍按既有 400/404/409/410 分类，测试记录实际既有合同，不随意统一成 200。
- 备份维护期间按原只读查询策略可用；该接口不触发初始化/迁移或维护写入。
- 不改变自动编号和旧/新 create 请求指纹、版本逻辑删除、active/fixed 比较、activate CAS、备份恢复及同名重建语义。

## 6. 实现要求

研发使用 `$engineering-quality-preserving-refactor`；实现前读 `C:/Users/MalfuRisa/.codex/skills/engineering-quality-preserving-refactor/SKILL.md`。

状态 DTO 和读取组织可放同 service 包独立 `release_active_state.go`；handler 只绑定、调用、转换。复用 scope 错误和 active 元数据转换；若提取公共小方法需明确保持原校验顺序，不把整个 release_service.go 顺手重构。

IDL 修改后用 hz 生成 pb/router，禁止手工编辑生成文件。显式 JSON 转换避免生成代码的 omitempty 省略必返字段。生成工具不可用时报告具体阻塞，不手工造类型。

更新 `docs/docs/api/management.md`、`release-compare.md` 的状态读取说明及前端交接；保留 Fork 文档格式。不修改 fixed 响应示例为非null，不把 GET active 的字段推广到所有不相关响应。

## 7. 验收标准

| ID | 准备/操作 | 必须验证 |
| --- | --- | --- |
| AS01 | 创建有效 scope，测试夹具移除 head，调用 GET active | active:null + active_revision:"0"，head/版本/事件记录数均不因读取增加 |
| AS02 | 有 head、空指针、revision=0 | 明确返回字符串0，不使用编号值；读取前后计数器和元数据一致 |
| AS03 | 服务/HTTP夹具：有效 scope，空指针、revision=7 | 返回 null+字符串7；说明该状态为边界夹具，不冒充正常接口造数产生 |
| AS04 | 在AS03夹具上创建/选已有候选 | 用查询取得的7做active compare，经显式确认activate成功；不直接猜7或加1 |
| AS05 | 有active、revision=9007199254740993（夹具） | 外层与active.revision原始JSON均为同一字符串，精度不丢 |
| AS06 | 读取捕获期间发生激活/切换，及A→B→A | 每个成功响应内状态自洽；旧compare/activate revision冲突，无静默推进 |
| AS07 | scope缺失/已删/同名重建/错配pin/非法UUID | 原错误分类和身份保护；无成功null/0，旧请求不读新代 |
| AS08 | head指向缺失、已删或别scope版本；负revision；注入DAO失败 | 503不完整/原内部错误，不伪造无active |
| AS09 | 无Cookie、可信/不可信Origin、备份维护 | 鉴权/CORS和只读维护策略保持；不新增GET CSRF要求 |
| AS10 | fixed compare、自动编号和create/activate幂等 | fixed两个null不变，创建不推进head revision，原指纹/重放和CAS不退化 |

纯查询测试应比对数据库前后，无写入副作用；并发测试观察真实响应与版本元数据，不只断言某个私有方法被调用。

在独立 SQLite 实例上跑真实 HTTP：正常首次→创建自动编号候选→查询0→比较→确认生效→查询revision一致；另在停服的临时库中构造非零空指针（不得对演示库操作），启动后查询→比较→激活。现有编号迁移不得偷偷将7改0。用例标清哪些由正常接口产生，哪些为边界夹具。

运行受影响Go服务/handler/DAO回归和vet，完成IDL生成一致性检查。MySQL/PG若未实跑，明确未验；本修正不新增race、K8s或热恢复承诺，也不把SQLite通过当所有数据库通过。

## 8. 交付与前端接入

后端回传应提供：始终存在的 data.active_revision、三个状态示例、只读与一致性证据、实际测试结果、合法状态可达性判断及未验证项。进度和结果追加本PRD，不为每轮修复另写桌面报告。

前端已有 activeState 字段兼容：接口升级后优先使用独立 active_revision；有active时也校验它与active.revision相同，非法响应停止确认。取得最新状态仅供重新compare，仍需用户重新确认生效；不再把“所有active:null”都当首次revision0。

本轮不要求前端窗口重做UI，不修改演示实例，不重新设计清空active业务，不提交推送。文档交付后由用户安排后端研发实施。

## 9. ZCode 实施结果（2026-10-26）

### 9.1 已实施

- IDL `ReleaseActiveData` 增加 `string active_revision = 2`，并用 hz 重新生成 `biz/model/release/release.pb.go`；未手改生成文件。
- GET `/api/v1/release/active` 改走新 `ReadReleaseActiveState`，handler 显式输出 `active` 和始终存在的 `active_revision`，未使用生成代码的 omitempty 字段作为成功响应。
- 新增单条只读 SQL 捕获（`biz/dal/db/release_active_state.go`）：同一 SELECT 返回当前 live environment/pipeline、可选 pin、head、head 指向的版本；用可空字段区分“没有 head”和“head revision 0 / 空指针”。
- 新增 service 层 `biz/service/release_active_state.go`：保留原 scope 错误语义，校验 pin/当前代、负 revision、缺失/已删/跨 scope 版本；不调用 EnsureHead、不做计数回填、不修复 active、不写审计。
- `ReleaseActive`/`loadActive` 保留给 create/activate/list 等旧调用方；本接口不再先 `ValidateReadScope` 再两次读取，避免 pin 校验和状态读取分属不同快照。
- 无新表、无迁移、无备份 schema 变更、无 Vue 变更；fixed compare 的 `active_revision:null` 合同不变。

### 9.2 AS01–AS10 结果

| ID | 结果 | 证据 |
| --- | --- | --- |
| AS01 | pass | 服务测试：无 head 返回 `active:null` + `active_revision:"0"`；读取前后 head/version/event 计数一致 |
| AS02 | pass | 有 head、空指针、revision 0 返回字符串 `"0"`；不使用 last_version_number |
| AS03 | pass | 停服夹具空指针 revision 7；HTTP 返回 `{"active":null,"active_revision":"7"}`，未改库 |
| AS04 | pass | 在 revision 7 边界上创建候选；查询仍为 7；active compare 使用 7；activate 成功并把 revision 推进到 8 |
| AS05 | pass | revision 9007199254740993；HTTP 原始 JSON 文本保留 `"active_revision":"9007199254740993"`，无 float64 舍入 |
| AS06 | pass | A→B→A 后 revision=3 且 active 为 A1；使用旧 revision 0 激活 B 返回 409 RELEASE_ACTIVE_REVISION_CONFLICT；单条 SELECT 保证每个成功响应内 active 与 revision 同源 |
| AS07 | pass | 缺范围 404 SCOPE_NOT_FOUND、错配 pin 409 SCOPE_IDENTITY_CONFLICT、无效/空白 pin 404、删除后 410 SCOPE_DELETED、同名重建后旧 pin 仍 410；未返回成功 null/0 |
| AS08 | pass | 缺失/已删/跨 scope 版本及负 revision 均 503 RELEASE_INCOMPLETE；取消 context 构造 DAO 失败返回原 500 内部错误，未吞成 null/0 |
| AS09 | pass | 无 Cookie GET 返回 401；备份维护期间 GET active 仍 200；既有可信/不可信 Origin CORS 回归通过；未新增 GET CSRF 要求 |
| AS10 | pass | create 成功和 request_id 重放后 head revision 仍为 0，只推进 last_version_number；fixed compare 的 `active_revision`/`target_is_active` 继续为 null；自动编号/CAS 回归通过 |

### 9.3 真实 HTTP 与重启验证

独立 SQLite 实例：`D:/tmp/rb-active-e2e`，端口 18150，`base_path=/rainbow-bridge`，备份关闭。未访问 18080/18081。

1. 首次读取：`{"active":null,"active_revision":"0"}`。
2. 锁版、自动创建 v1 后仍为 null+0；用 0 做 active compare；显式 activate 后 `active_revision:"1"`，与 `active.revision` 相同。
3. 连续 3 次 GET 前后：head 行数/最大 revision、版本数、事件数、last_version_number 完全一致；无写入副作用。
4. 停服后仅在临时库把 head 改为空指针 `revision=7`（保留 last_version_number=1）；重启读取 `null+7`。创建 v2 后仍为 7；用 7 做 compare；activate 返回 `applied_revision:"8"`；再读为 active v2 + `"8"`。
5. 再次停服把 revision 改为 `9007199254740993` 并重启；原始 HTTP 正文为 `"active_revision":"9007199254740993"`，`active:null`。
6. 无 Cookie `GET /release/active` 返回 401。

### 9.4 合法状态可达性

- 正常创建但从未激活：head 可能存在且 revision 为 0，active:null；自然可达。
- 有效范围、head 存在、空指针、非零 revision：本轮只通过停服后的临时库夹具构造。没有发现正常公开业务入口会生产该状态，也没有为此新增“清空 active”接口；因此不宣称已复现线上故障。
- 读取不会把该边界改成 0，也不会伪造 active 对象；后续 compare/activate 使用真实 revision。

### 9.5 验证命令与未验证项

- `go test ./...`：通过（含新增 active state service/handler/middleware 测试）。
- `go vet ./...`：通过。
- `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build`：通过。
- IDL 已用 hz 重新生成；无手改 pb/router。
- 未验证 MySQL/PostgreSQL 实跑、race、K8s 多 Pod、热恢复或权限隔离；本轮只确认 SQLite 单实例。MySQL/PG 的 SELECT 使用标准 LEFT JOIN/标量子查询与 GORM 占位符，但未实跑不表述为已验证。
- 未修改 Vue、未提交/推送、未替换演示实例；前端仍按既有 activeState 兼容逻辑，取得新 revision 后必须重新 compare 并由用户确认。
