# 发布审阅：前端实现与验收

2026-10-26。使用 refactoring-ui；在现有 Vue 3、Tailwind 4、Radix 与光谱主题内实现，不增加依赖。

## 入口与操作

- 版本页主入口“发布配置”：准备候选 → 审阅差异 → 确认生效。
- 未冻结时锁版再创建；已冻结时直接创建。创建后停在审阅，不自动生效。
- 已有版本可从列表查看差异或进入切换审阅，无需再次锁版或创建。
- 保留独立创建版本、版本详情、操作记录、删除版本和配置页锁版入口。
- 取消审阅保留候选和冻结状态。生效请求结果不确定时，关闭按钮改为“稍后确认”，避免暗示已经撤销。

## 界面约束

| 层级 | 呈现 |
| --- | --- |
| 主要 | 当前版本 → 候选版本；新增、修改、删除、未变计数；本步骤唯一主按钮 |
| 次要 | 范围与步骤；逐项变化字段；展开后双栏内容对照 |
| 按需 | 配置原文、复制、图片大图、备注；不展示内部 UUID 和 revision |

复用光谱材质、文字与阴影 token；采用 8/16/24/32 间距和现有圆角。颜色同时配合文字表达状态，不只靠色差。内容区独立滚动，标题和确认操作保持可见。支持 1280/1440 桌面宽度及明暗主题，沿用系统减少动态效果设置。

Refactoring UI 复核：§2.1/§2.8 的信息和操作层级、§3.6 的分组间距、§5.7 的非颜色状态信号、§6 的既有材质、§8.4 的空版本与失败状态均在真实渲染中检查。修正 Radix Portal 导致局部弹窗宽度样式没有命中的问题；截图和测试同时核对宽度、标题及确认按钮在视口内。

## 接口与身份保护

按 [后端接口合同](../docs/api/release-compare.md) 编排既有接口，不修改后端。

- compare 固定 scope UUID、候选版本和 snapshot UUID、生效 snapshot 和 revision；revision 始终为字符串。
- 差异分页独立使用 offset，失败不会伪装零差异；身份或并发冲突使全部旧分页失效。
- 详情使用两个固定版本读取并核对返回的 scope、版本名和 snapshot。内容通过既有 ConfigContentPreview 展示，保留原文与复制，不在前端比较资源 URL 或解析配置大整数。
- activate 使用 compare 返回的 revision。冲突后必须重新比较、再次确认；不自动推进 revision 或重发新操作。
- create 与 activate 使用独立 request_id。首次完整请求先保存在本标签页 sessionStorage，按 BasePath、用户和 scope UUID 隔离；只存操作意图，不存凭据或配置正文。原请求结果不确定时只能显式原样确认。
- 关闭、路由离开、身份变化或范围变化后，迟到响应不能推进旧界面。刷新后不自动重放写请求，重新读取后端事实。已确认切换还会重新读取 active，分别展示“操作已确认”和“此刻所选版本仍生效”。

职责划分：API 类型和响应校验在 release-compare.ts；固定基准、分页与内容对照在 useReleaseComparison；流程编排在 useReleaseReview；不确定请求持久化在 releaseReviewPending；主面板和逐项预览分别由组件呈现。

## 验证

- 在 `vue` 目录执行 `npm run build`：类型检查和生产构建通过。
- 全部现有前端 Mock 回归及首批新测试：113 项通过。
- 补充身份、维护及响应字段校验后，发布审阅与请求专项：21 项通过，包含 16 项发布审阅场景。
- 独立 SQLite 实例前端联调：2 项通过，覆盖原导入导出操作及完整发布审阅流程。最新证据：`output/scope-transfer-ui/instance-f3bb3c83/release-review-evidence.json`。
- 真实场景：首个候选取消后保留且未生效；四种资源正常预览；第二版比较为新增 1 / 修改 2 / 删除 1 / 未变 2；另一个管理员切到 v3 后旧确认被拒绝；重新比较后再次确认 v2，业务读取值为 B。
- 截图：`output/release-review-ui/visuals/review-{1280,1440}-{light,dark}.png`。测试只使用临时独立实例，不操作现有 18080/18081 数据。

## 先前接口边界（本轮正式接入已解除）

早期 `/release/active` 仅返回 active，无法区分 null+0 与 null+非零 head。该依赖已由后端独立状态字段和本轮前端正式接入解除；最新规则及验证见文末“独立状态字段、旧 runtime 范围与未知 API 适配”。不再把该字段列为待后端补齐项。

本次未更换品牌标志、首页封面、拓扑背景或配置预览规则。保留工作区原有未提交成果，未提交、推送或部署。

## 自动编号、定向比较及恢复副本适配（2026-10-26）

接入最新 [后端实施合同](release-numbering-compare-retention-backend-v1.md) 及 API 文档；以下为本轮新增登记，上面的验证数字保留为首轮记录。

### 实际改动

- **自动编号**：独立“创建版本”和集中“发布配置”均移除版本输入，不计算、预占或猜测下个版本，新 create payload 不含 version。成功后仅使用返回 meta.version/snapshot_id 继续比较及切换。选填备注、范围 UUID、冻结 revision、Cookie/CSRF 和独立 request_id 保留。400 自动编号错误、409 编号耗尽明确提示，不显示原请求盲重试入口。
- **未确认请求**：两入口共用 releaseReviewPending 的按用户、BasePath、scope UUID 隔离记录。auto 重放仍不补入返回版本；升级前保存的旧手填 version 和 request_id 原样确认。确认独立创建结果后，以服务端实际版本更新反馈。未确认的切换不能被独立创建入口覆盖。
- **定向比较**：版本页增加“版本比较”，基准在左、目标在右，可交换方向、选择相同版本、加载更早版本。选择列表保持后端创建顺序，旧名继续可选。active/fixed 请求及响应采用区分联合类型，分别构造字段；fixed 的 active_revision 与 target_is_active 严格保留 null。
- **历史比较保护**：共用 useReleaseComparison 的固定身份、分页、详情读取和旧请求失效机制。fixed 不读取当前 head，切换 active 不影响固定双方的后续分页；换任意一侧、方向、范围或身份都会清除旧结果。404/409/410 标明基准或目标异常；详情再次核对 snapshot。内容不经 JS 数字解析，原文、复制和各类型预览继续复用。
- **转入发布**：“审阅目标版本发布”只打开既有发布审阅，重新读取 active 并执行 active compare；fixed 结果没有可用发布 revision，也不能直接 activate。当前已生效时不显示重复切换动作。
- **恢复副本**：capabilities 兼容可选 rollback_retention。仅在当前能力可用、managed=true 且数量有效时显示“系统自动保留最近 N 次恢复前的数据”，与可下载备份包数量分开。移除正常留存副本的通用人工清理提示；完成恢复后清除旧预览和文件，允许下一轮上传校验。仍保留重启、覆盖确认、维护、预览过期与会话恢复交互。
- **异常兼容**：BACKUP_RESTORE_PENDING 仍要求完成或取消计划；BACKUP_ROLLBACK_REVIEW_REQUIRED 只描述异常或安全状态无法确认，并保留旧后端 BACKUP_STATE_CONFLICT 的两条精确兼容映射。没有清理服务器副本的新按钮或自动调用。

### 组件与视觉检查

新增 FixedReleaseCompareDialog；ReleaseComparisonSummary 从发布审阅提取，共用摘要而不复制配置比较算法。选择、摘要、展开预览与底部次要操作分层，复用现有材质和 Select/Dialog；1280/1440、明暗主题已查看实际截图，确认标题、选择器与底部操作可见。

截图：`output/release-review-ui/visuals/fixed-{1280,1440}-{light,dark}.png`、`backup-preview-{1280,1440}-{light,dark}.png`。沿用桌面布局，不增加移动端改造。

### 本轮验证登记

- 类型检查与生产构建通过。
- 根路径 `/` 的自动编号、旧请求确认、fixed→active、无 active 且 revision 缺失保护：4 项通过。
- 独立 SQLite 发布/导出联调：2 项通过。证据：`output/scope-transfer-ui/instance-a5627b4d/release-review-evidence.json`。涵盖自动返回 v1/v2/v3、创建后取消、并发切换重新确认、fixed 正反方向、自比、null 字段和重新进入 active 审阅。
- 独立 SQLite 备份界面联调：1 项通过。证据：`output/backup-ui-20261025/instance-cec93eb8/frontend-evidence.json`。同一有效包连续两次上传、预览、确认、实际重启、重新登录及恢复值核对均成功；两次任务 state=succeeded。未要求人工处理正常留存副本。
- 完整前端 Mock 回归：140 项通过（4.1 分钟）。包含新请求无 version、返回实际编号、auto 重试与两入口旧手填 pending、编号错误、v9/v10/旧名列表顺序、fixed 自比/换向/分页/侧错误/null、迟到响应及同名范围重建、恢复保留数量 1/3/20、缺省策略与成功后再次恢复，以及既有发布、备份、身份、配置类型、导入导出与交互回归。

### 先前状态字段依赖（已解除）

自动编号等适配时曾保留“无 active 且 head 非零”的查询缺口；现已由独立 active_revision 必返合同解决。旧服务缺少字段时停止确认并提示服务合同不完整，不再用 active:null 猜 revision=0；本轮真实前端联调已覆盖 null+7，详见以下登记。

本轮只验前端接入和独立 SQLite 联调，没有扩展 MySQL/PG、race、K8s/多 Pod 或热恢复验收；旧 schema 1/2 包夹具的来源限制沿用后端登记，没有把反向构造夹具写成旧二进制原件。未改后端业务代码、未提交/推送、未部署演示服务；测试实例结束后均按所属进程身份停止。

## 独立状态字段、旧 runtime 范围与未知 API 适配（2026-10-26）

依据 [状态读取修正实施结果](release-active-state-revision-backend-v1.md)、[运行时范围与 API 404 实施结果](runtime-scope-and-api-404-fix-backend-v1.md) 接入现有功能；未重做自动编号、定向比较或备份策略。使用 refactoring-ui 保持现有界面，无新视觉改版；按 engineering-quality-preserving-refactor 将状态校验与流程编排分开。

### 实际修改

- 新增 `vue/src/lib/api/release-active-state.ts`，统一校验 GET active 的必返字符串 active_revision。active 必须是 null 或有效对象；存在 active 时外层与内层 revision 相同，范围为请求的 scope，snapshot/version 均非空。缺失、数字、负数、非法格式、内外矛盾或目标信息不完整返回明确响应错误。
- releaseApi.activeState 和 releaseApi.active 共享该校验。useReleaseComparison 仅使用独立 active_revision，删除 null 推零和“请补齐后端接口”旧提示。不支持缺字段旧服务的推测性兼容；失败保留输入、停止审阅，确认服务版本后显式重试。
- 读取最新状态仍只用于重新 compare，不自动 activate。原请求精确确认、CAS 冲突重比、A→B→A、scope 重建隔离及 fixed 模式两个 null 合同保持。
- 实际调用检索：页面仅用 runtimeApi.overview，配置预览和发布资源继续使用 runtime/release，导出继续固定版本/snapshot；无页面调用旧 config/static，未迁回旧接口。保留的 runtimeApi.config/exportStatic 适配方法补 scope_id：config 放 query、范围放 headers；static 范围与 pin 均放 query。复用 readScope 的前后 UUID 校验，身份变化时丢弃旧结果；不删除 pin、不重试无 pin、不自动接入新代。未来若新增 UI 调用，仍需调用方处理自己的展示请求批次。
- 在统一 operationMessage 映射 API_NOT_FOUND 为“接口地址不存在或服务版本不匹配，请确认服务地址与版本。”。auth 地址的该错误保留此提示并清除错误原始载荷；不将其当 AUTH_REQUIRED 清会话。业务版本不存在、范围删除等原错误分类不变。
- ZIP 下载仍经过统一 HTTP、JSON 类型及归档签名校验，错误响应不能被保存为 ZIP。未改 SPA 通配路由或正常资源路径。

### 本轮实际验证

- `vue` 中 `npm run build`：类型检查与生产构建通过。
- 发布、身份、下载和登录相关 Mock 回归：本轮实际重跑 91 项，全部通过（2.0 分钟）。上一轮 140 项/4 项根路径结果为既有基线，不计作本轮新跑。
- 本轮根路径 `/` 补充检查：5 项通过（9.8 秒），覆盖 null+7、未知 API 导出、登录地址缺失提示、旧 runtime 的真实 pin 和迟到响应隔离；包含新增登录接口 API_NOT_FOUND 场景。
- 新增 Mock 覆盖 null+0/null+7/大整数原文、有 active 的一致 revision、缺失/非法/矛盾响应停止、备注保留、API_NOT_FOUND 不清会话或下载；原 CAS、fixed null、幂等、身份重建回归复用并实际重跑。保留的旧 runtime 适配器另验证请求 pin、409/410/404 不降级、同名新 UUID 迟到响应失效。
- 独立 SQLite 前端真实联调：3 项通过，包含既有导入导出、正常首次发布/已生效/CAS/fixed 路径及本轮 null+7 边界。
- 边界准备：`script/test-scope-transfer-ui.ps1` 初始化全新独立目录后，核对进程 exe/PID/启动时间并停服，再运行 `script/qa/release-state-fixture`，只对允许目录中无版本、无生效指针且原 revision=0 的 default scope 改 revision=7，然后重启。夹具不是正常业务入口，不宣称复现演示故障；没有新增“清空 active”产品接口。
- 浏览器真实链路：GET 返回 null+字符串7；创建候选后仍为7；compare 请求/响应均7；确认前 activate 请求数0；用户点击后请求仍携带7，最终外层与内层均8且 snapshot 匹配。将实际 UI 请求指向服务器未知 API，查询/导出均得到准确提示、auth/me 仍200、下载数0。
- 最新证据：`output/scope-transfer-ui/instance-5d646bee/state-fixture.json`、`release-active-state-evidence.json`、`release-review-evidence.json`。脚本复用原隔离端口和凭据环境机制，测试进程已按归属停止。

没有遗留的 active revision 字段依赖。当前前端要求升级后的 GET active 合同，旧服务响应缺字段时会保留输入并阻止发布；这属于明确的兼容策略。没有重新验收整个后端，也未扩展 MySQL/PG、race、K8s/热恢复结论；未提交/推送、未替换 18080/18081 等用户实例。
