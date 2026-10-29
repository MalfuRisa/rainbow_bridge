# 远程配置缓存修复与MinIO发布支持：后端PRD

日期：2026-10-29。负责人：虹桥研发（local / 01a1333a-342c-7903-8ca8-164b82392399）。状态：A/B及§8验收保留；新增MinIO快照区实现、迁移工具与隔离验证已交付，待架构验收。当前合同§9，实施登记§10；现有远程服务未更新，未执行业务历史切换。

## 1. 本次要交付什么

远程测试已证明两项独立后端问题：D2为配置写入后列表陈旧的实现缺陷；L1为MinIO源不支持创建发布的能力缺口。用户现要求分工修复。本任务先完成缓存修复，再补齐MinIO源到不可变快照的发布路径，交付可审查代码与实际验证证据。前端UUID问题由虹桥前端负责，不改vue业务代码。

本次保持最小架构：PG存业务数据，MinIO存原始资源，Redis按现有机制工作；发布快照继续使用现有独立文件目录，服务器/K8s单实例需持久卷。不要把原始资源的MinIO对象当发布快照，不扩大成对象存储快照重构、双Pod、热恢复或账号系统。

备份在PG+MinIO+Redis下仍保持BACKUP_UNSUPPORTED，不在本任务补备份恢复。尚未执行的接口不自动认定缺陷。任务授权实现与测试，不授权替换/重启现有远程实例、应用K8s清单、推送、发布或打tag；交付附具体部署改动供最后确认。

## 2. 已核实事实及版本边界

详见 remote-api-verification-and-request-id-v1.md §9。架构师已核对实施登记及 .local-qa/remote-api/qa_20261029_17f0351f/{browser,transfer-readback,cleanup,baseline-before}.json。

- 远程为 v5.0.3 / c49194a066f448a0730e485fca18f7f60e6e0e0b；镜像ghcr.io/yi-nology/rainbow_bridge-app:v5.0.3，1副本；namespace rainbow-bridge，deployment rainbow-bridge-app。
- 远程PG/MinIO/Redis均开启。未设置RAINBOW_RELEASE_DIR；/app/data/release-snapshots位于overlay，无业务持久卷，当前没有成功快照。不能以“Pod内重启进程”证明“Pod重建持久”。
- 远程普通config/list在导入后HTTP200却为空；同scope详情和type=text列表可读TITLE。该证据证明读写不一致，不是数据没落库。
- 本地ListConfigs/GetConfig/ListConfigsAsMap使用GenerateScopedConfig*键，config_cache.go普通写入失效仍清旧environment/pipeline键；scope删除已有scope键失效逻辑。旧config_import_cache_test.go主要断言旧命令序列，不能以它通过证明新scope读取已正确失效。
- release_service.go创建路径显式拒绝MinIO；release_snapshot.go resolveAsset拼本地data路径并os.Stat，release_storage.go只接受源文件路径复制。不能只删503分支。
- resource_files.go已有MinIO客户端、GetObject及missing-object识别，可复用语义。但readManagedAssetFileBytes无界ReadAll，不适合作为发布大资源的直接实现。

工作目录D:/kylin/rainbow_bridge，main_v1，编写时HEAD 05ff46b；远程提交未在本地对象库。实施前先核实远程对应源码差异，必要时正常只读fetch取证，不reset或自动把远程所有变化合入主目录。不确定源码对应关系须报告，不能宣称本地旧版测试修复了现有镜像。实现基于已核实本地工作区，交付记录补丁与远程版本的适用差异。

读取根AGENTS与 $engineering-quality-preserving-refactor（C:/Users/MalfuRisa/.codex/skills/engineering-quality-preserving-refactor/SKILL.md）。职责拆分、复用小方法、降低嵌套；不修改生成代码，不引入Python。前端并行修改vue及自己的测试，后端仅修改biz手写服务、pkg必要辅助、后端测试与部署模板/文档、本PRD；不要触碰vue、前端构建产物和对方PRD。保护已有未跟踪远程测试成果。

## 3. 工作包A：scope缓存写后失效

### 设计

- 修复invalidateConfigCaches，使创建、更新、删除、JSON/归档/选择性导入、migrate、资源引用重写等已提交配置写入统一失效实际scope键。沿用同一入口，不各处散落拼接字符串。
- 失效项为：受影响资源的GenerateScopedConfigKey(scopeID,resourceKey)，该scope的全部列表过滤组合（GenerateScopedConfigListPrefix(scopeID)+匹配后缀），GenerateScopedConfigMapKey(scopeID)。按scope去重；旧键兼容清理可保留，不能全库清缓存。
- scopeID优先取实际写入/删除实体；缺失时根据该操作已确认的身份解析，不能仅按同名环境渠道把旧操作映射新代。删除前实体信息、导入/迁移返回written实体须携带真实scope身份；不能静默跳过失效。
- 仅在数据库成功提交后执行失效，事务回滚不失效。缓存错误不把已成功DB写入变成失败；沿用有界超时、上下文取消隔离与必要日志。日志不能包含配置正文/凭据。
- 不修改列表筛选、权限和runtime只读快照语义；不能通过关闭Redis、缩短TTL、前端强刷或手动清Redis代替修复。模式转义沿用既有机制，键范围限具体scope；若修改遍历实现使用有界SCAN，不扩展为Redis框架。
- 关注在途旧读回填的并发风险，不宣称普通删除缓存提供严格线性一致性；如确定性行为验证发现修复仍会产生本轮主缺陷，记录具体时序并提出最小补充方案，不无依据重构全缓存协议。

### 验收

先读空列表让其缓存，再分别create、import、JSON import、selective、migrate；不等TTL、不手清Redis，普通列表、类型列表、详情都立即反映已提交内容。分别预热详情/map/过滤列表后update/delete，检查内容、edit_revision及删除可见性。验证旧scope删除同名重建不串代、其他scope缓存不被误删；提交失败与缓存故障维持原DB语义。

测试观察实际读结果，不仅断言DEL调用。可复用Hook测提交顺序，但需真实Redis开启的读写回归；Redis关闭也保持CRUD。适量并发读写覆盖，不做压力压测。本机不安装中间件，真实组件使用远程独立测试资源。

## 4. 工作包B：MinIO源发布与快照持久化设计

### 4.1 资源读取与不可变落盘

源读取抽象保持小：release源适配负责从本地或MinIO按受管资源元数据打开可关闭、可取消的流，并获得必要大小信息；发布复制器负责有界复制、SHA-256、落盘复读校验。业务捕获计划负责引用解析、去重、范围身份及限制；materialize保留事务编排。不建立跨业务大型存储框架。

MinIO只读取配置中固定bucket与asset.Path，不能把路径转成任意URL、访问任意桶或暴露签名凭据。使用现有客户端和错误识别，GetObject可能延迟至读/Stat才报不存在，必须覆盖。元数据必须属于本次scope，同名新代也不能串用。

保留现有单资源、总资源、数量和内容限制：预检查只是早拒绝，实际复制仍以max+1有界计数判超限；总实际大小也校验。避免无界ReadAll、一次把全部资源放内存或落到共享临时目录。所有源流、对象和文件在成功/错误/取消路径关闭，传播ctx并处理读超时。源对象缺失沿用resource_issues/现有错误类别，权限/网络/存储故障不能伪装成不存在或空文件；错误不泄露端点凭据。

本地与MinIO资源都复制到快照staging独占目录，再按现有机制原子移到final、写版本和清单。快照只引用已复制并校验的内容，不返回原MinIO可变URL。复制后改变或删除源，旧版字节和摘要必须不变。对Stat与读取期间变化的大小/版本应验证一致或拒绝，不得接受元数据大小与实际内容矛盾；优先使用SDK已有条件读取能力，不靠弱ETag推定SHA256。

保留冻结revision/范围身份检查、自动版本号事务、request_id指纹幂等、active_revision并发、创建不生效、切换不改编辑区、历史版本读取与删除保护。请求失败不生成可用半成品或改变active；事务提交未知时保留final待核对，不擅自删可能已提交快照；清理沿用现有已知/未知提交语义。空范围/无资源版本也支持，不能只测纯文本后宣称MinIO资源链通过。

### 4.2 本批快照落点

继续使用RAINBOW_RELEASE_DIR，要求部署时指向独立持久卷目录，staging与final在同一文件系统，以保持rename语义。本批单实例；多Pod共享卷与并发调度不在范围。不自动把所有快照搬进MinIO，也不修改现有快照ID、目录格式和对外下载URL。

读取现有deploy/kubernetes示例，提供最小部署模板/补丁或准确操作步骤：持久卷挂载点、环境变量、目录权限、容量要求、单实例约束、更新和回退方法。模板不含凭据，不把模板写好当作持久化已完成；本任务不apply、不重启现有服务。若无法确定现有storage class/PVC资源，保留明确待填写项并说明读取证据，不编造可用卷。

不能仅因本地路径可写便宣传生产安全；当前远程overlay风险必须在交付明确。测试中可在独立持久目录停止并重启本轮临时实例验证读取，不能冒充现有K8s Pod重建验收。

### 4.3 验收

| 场景 | 预期 |
| --- | --- |
| local源回归及MinIO纯配置/含图片文本 | 创建候选成功、自动编号、初始不生效；详情与快照清单正确 |
| 生效→匿名runtime→资源/静态ZIP | JSON大整数不丢、引用正确、字节/Content-Type/SHA256一致，旧/新入口按合同读active或fixed |
| 编辑或删除原始对象 | 旧快照仍可按fixed及发布资源URL取原字节；仅操作隔离测试对象 |
| 对象缺失/权限拒绝/网络中断/超限/取消 | 分类准确，active不变，无可用半快照，不泄露凭据；异常残留有精确清单 |
| 冻结revision过期、scope重建、并发/重放 | 保持原冲突与幂等；相同请求不产生重复版本，不串scope |
| 独立实例重启+同持久目录 | 版本、active、资源和ZIP仍可读；既有目录格式可读 |

故障注入只能在独立实例或单元/集成夹具，不使共享服务断网或撤销权限。补运行时、比较、固定导出等原被阻塞主路径的正向证据，不能仅证明503消失。备份支持状态保持原限制。

## 5. 验证环境、清理与部署界限

后端修复尚未部署，不能反复调用旧实例声称验证新代码。按需构建dev后端用于验证，避免依赖前端并行构建产物。

允许在远程建立本轮拥有的临时测试进程与独立测试数据资源来验证修复，前提是实际可隔离：独立PG测试数据库或经核实隔离schema、独占MinIO测试桶或完全独占前缀（业务代码不支持前缀时必须用独立桶）、明确独立的Redis键空间/实例、独立快照目录、唯一端口。用既有中间件，不在本机安装、不改现有服务。只通过SSH转发或已有访问方式访问临时实例，不改变生产Ingress/Service/防火墙，不停止他人进程。

不具备隔离能力时先完成可做的实现和行为测试，报告缺少的具体资源与真实联调阻塞；不能退而复用现有业务DB进行迁移/启动候选版本，不能擅自更新原deployment。测试资源的创建与删除均限本轮独占、先登记归属再操作；数据库连接和MinIO权限从本任务授权的配置获取仅供进程使用，不打印完整env/Secret。

沿用原测试PRD §4：清单、finally清理、范围身份与原业务基线核对。本次新增真实图片验证应优先放完全独占临时实例/桶，实例停止后删除独占测试桶/目录与测试DB，绝不在共享原始桶上传后用全桶删除清理。删除目录先验证绝对路径严格位于本轮工作根；对象清单防遗漏，清理失败明确报告。共享服务审计/逻辑删除历史不抹除。

测试脚本可沿用tests/e2e/remote-api并在其下新增backend-fixes子目录；单元测试与源职责放对应Go包。Go编译、受影响测试、真实PG/Redis/MinIO HTTP证据分别报告，Mock不冒充真实组件。不得由架构师代跑。

## 6. 顺序与回传

1. 对齐版本差异与隔离环境，先给最小源码对应证据。
2. 缓存缺陷先复现后修复，完成提交/回滚与读回验证，登记A包结果。
3. 实现MinIO流读取与既有发布管线复用，故障/限制单测，完成B包独立真实组件主路径。
4. 准备单实例持久卷部署补丁和回退步骤，不应用现有部署。回传实现、测试清理与部署前剩余项。

本PRD内追加实施登记，分A缓存、B发布、C部署待办三类状态。列实际文件、版本差异、执行命令、证据、未验证项、清理结果、设计偏差和具体待部署动作。不得用前端未完成掩盖后端HTTP验证，也不在双方未部署时宣称远程已修复。新发现无关缺陷回传最小复现，不悄悄扩大范围。

派发：2026-10-29 已发送至虹桥研发，工具确认送达。产品修复结果：见以下实施登记；不代表现有远程镜像已修复。

## 7. 后端实施登记（2026-10-29）

### 7.1 版本适用范围

本地实施基线仍为main_v1 / 05ff46b，加本轮未提交补丁。正常只读fetch取得远程c49194a066f448a0730e485fca18f7f60e6e0e0b后，对比确认两者差异仅涉及Dockerfile.Remote.BIN、docs/releases/v5.0.2.md、v5.0.3.md及pkg/config/{auth.go,auth_test.go,config.go}。本轮涉及的缓存、导入、迁移、发布源读取和快照代码在两基线间一致，因此缺陷及补丁适用；未将远程所有提交合并入工作区。

候选ARM64后端使用dev构建独立运行于远程节点loopback，版本标识为backend-candidate、GitCommit为05ff46b-plus-uncommitted-backend-fixes，未复用前端并行构建产物。未来正式镜像必须同时保留远程HTTP会话显式opt-in与ARM64打包修正，不能拿本地旧基线构建覆盖v5.0.3。测试没有替换、停止或重启现有deployment，没有push、tag或发布。

### 7.2 A：缓存修复及证据

代码：biz/service/config_cache.go、pkg/redis/redis.go；测试：config_import_cache_test.go、backend_remote_components_test.go、backend_remote_http_test.go。

统一写后入口现在同时删除真实scope的单条详情、所有筛选组合列表和map缓存，保留旧键兼容清理。scope去重、glob转义，新增SCAN分页与分批DEL，每批128；整体沿用5秒超时。数据库成功后才失效，Redis故障不反转已提交DB结果。没有全库清缓存、关闭Redis或缩短TTL。

逐入口核查scope身份来源如下，未发现当前生产写路径返回缺失ScopeID的written实体：

| 写入口 | 已确认的身份来源 |
| --- | --- |
| config_service.go创建/更新 | 写协调事务内resolveScopeForWrite得到pipeline.ScopeID并赋给entity |
| config_service.go删除 | 同一事务内确认范围并删除，保留pipeline.ScopeID和resource_key供提交后失效 |
| config_import.go JSON与兼容入口 | 新增经ConfigDAO.Create在事务内填ActiveScopeID；覆盖后回读saved完整实体，不使用输入的持久化身份 |
| archive_import.go普通/选择性归档导入 | 复用importConfigs返回的written，成功提交后统一失效 |
| transfer_service.go迁移 | migrateOneConfig在保存点内创建/覆盖后回读stored，只有成功项加入written；外层提交后失效 |
| asset_paths.go启动引用重写 | 查询已有Config完整实体（启动身份迁移在前），成功Updates后传cfg.ScopeID |

失效辅助遇到缺失ScopeID会明确记录异常而不按提交后的同名业务键推测新代；这是防御性告警，不是支持无身份写入的降级保证。将来新增写入口必须维持上述不变量。

真实PostgreSQL独立schema＋独占Redis下，先读空缓存再create、JSON、archive、selective、migrate，随后普通列表立即可见；预热详情、map与列表后update读回新内容及edit_revision，delete后详情/列表/map不再返回该行。未等TTL、未手工删除缓存；其他scope预热缓存仍在。真实候选HTTP也覆盖了先空列表、上传/创建后立即列表读回。初次类型过滤断言不足，补强验收见§7.6。

单元/服务回归覆盖提交后失效、回滚不失效、Redis故障保持DB成功、特殊字符转义；既有scope删除重建、编辑并发和发布期间并发写用例随受影响包回归通过。**本次未额外在真实Redis上确定性调度“旧读阻塞→新写失效→旧读回填”或同名scope重建竞态，不能将这些场景标为真实组件并发验收通过。**

剩余一致性边界：数据库提交后清缓存解决本次键体系不一致导致的持续陈旧；若旧读取在DB提交前读到旧值、在DEL之后才SET，仍可能回填旧值，直至后续成功失效或TTL到期。当前详情/map的TTL为1小时，列表为30分钟，均从该次SET计时；因此无后续失效时，旧值可能持续相应完整TTL，不能称为短时风险。未引入缓存代际/读写栅栏，不能宣称严格线性一致；本次顺序写后读验收未再出现原缺陷。若产品要求消除此竞态，需要另行明确缓存代际协议及确定性并发验收，不以本次结果代替。

### 7.3 B：MinIO发布、故障与真实HTTP

代码：新增release_source.go负责本地/MinIO源捕获及流生命周期；release_snapshot.go保持引用解析/范围/限额职责；release_storage.go复用有界复制、哈希及落盘复读；release_service.go保留事务和快照发布编排，移除MinIO无条件503分支。生成代码、前端、接口字段和快照目录格式未变。

MinIO客户端只使用现有配置的bucket及受管asset.Path，读源不创建桶。先Stat取得大小、ETag、VersionID，GET带If-Match及可用VersionID，读后再Stat校验大小/ETag/时间；ETag仅作一致性令牌，最终SHA-256仍由实际流计算。对象及本地文件均defer Close，ctx传入SDK和复制读取；本地文件在读取前后核对size/mtime。单资源max+1有界复制、实际大小与元数据一致性、总大小、数量限制保持；没有为MinIO发布新增无界ReadAll或全部资源内存缓冲。

复制仍进入独占staging、复读文件校验后同盘rename到final，再按原事务语义记录版本。已知失败清理、未知提交保留final待核对的逻辑未改。资源属于当前scope才可进入计划。冻结revision、自动版本、request_id重放、active并发保护仍由原路径执行；旧快照下载不再依赖源对象是否存在。

真实远程PG/MinIO/Redis验证：

- 服务路径上传有效PNG及文本，创建自动v1且初始不生效，重放返回同一snapshot；生效后固定runtime、资源字节和SHA一致。
- 删除**独占测试桶**中的原始对象后，原快照、固定runtime及ZIP仍可读；再次创建因缺失源返回RELEASE_RESOURCE_INVALID，原active不变。大整数原文在runtime/ZIP保持。
- 使用新编译候选二进制的真实路由，覆盖登录、MinIO multipart文本上传、配置创建、锁版、候选创建/重放、比较、生效、匿名runtime、发布资源下载，以及旧runtime/static与新runtime/release/static入口。
- 停止并重新启动该独占候选进程，复用同一schema与快照目录后，版本/active/runtime/资源/ZIP仍可读，并正常登录登出。它只证明同目录进程重启，不是K8s Pod重建持久化验收。

新增release_source_test.go覆盖S3协议夹具下HEAD缺失、GetObject延迟读才缺失、权限拒绝、断流、取消、ETag条件失配，以及流超限/元数据大小不符时拒绝并移除文件。错误不含SDK凭据；不存在归为资源问题，权限/网络不能当空文件或不存在。MinIO配置下空范围、纯文本无资源候选及已提交请求在源不可用时重放均通过。上述故障注入是隔离夹具，**没有对共享MinIO撤权、断网或注入故障**。

HTTP真实上传此次为文本，真实PNG在服务集成路径验证；故障夹具不冒充真实组件故障验收。比较HTTP覆盖active模式，固定版本比较/导出及冻结/身份/并发保护另由既有包回归提供证据。PG+MinIO+Redis备份能力未扩展，仍为BACKUP_UNSUPPORTED。

### 7.4 C：部署交付及未完成事项

交付deploy/kubernetes/release-snapshots/{pvc.example.yaml,deployment.patch.example.yaml,README.md}，针对现场rainbow-bridge-app/app提供单副本Recreate、独立RWO PVC、RAINBOW_RELEASE_DIR=/var/lib/rainbow/releases、同文件系统staging/final及回退保留PVC的步骤。StorageClass、UID/GID与20Gi容量均明确需现场核实，不编造可用卷。

**尚未部署，远程overlay风险仍存在。** 后续需审核实际卷和权限、组合远程已有修正构建固定digest镜像、批准停机窗口后应用PVC/部署补丁；已有快照时先完整迁移并校验。最终需重建实际Pod验证同一卷的历史snapshot/active/资源/ZIP，之后才可认定生产快照持久化完成。回退保留卷与目录，不通过删PVC、清业务库或丢弃新快照回退。本轮没有执行这些动作。

### 7.5 执行记录、隔离和清理

本地受影响包完整回归及静态检查：

```text
go test ./biz/service ./biz/handler/release ./biz/handler/config ./biz/handler/transfer ./pkg/redis
go vet ./biz/service ./biz/handler/release ./biz/handler/config ./biz/handler/transfer ./pkg/redis
go test ./biz/service -run 'TestReleaseMinIOEmpty|TestReleaseReplayIndependent' -v
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c ./biz/service -o .local-qa/backend-fixes/service.test
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags=dev -o .local-qa/backend-fixes/hertz_service .
go run ./tests/e2e/remote-api/backend-fixes/remote test
go run ./tests/e2e/remote-api/backend-fixes/remote inspect
```

环境变量构建命令为平台无关说明，PowerShell实际使用$env形式；候选构建另注入前述版本标识。远程test需本轮SSH凭据、已核实known_hosts、两个二进制路径及日志路径环境变量；不在文档/代码写入密码。采用Go SSH传输，未使用Python。

证据目录为仓库本地忽略目录.local-qa/backend-fixes/，未作为凭据或产物提交：

| 文件 | 实际结果 |
| --- | --- |
| backend-tests-final.log | 上述受影响包通过，service 70.342秒；无远程环境变量时真实组件用例明确skip |
| vet-final.log | go vet通过、无诊断 |
| source-tests.log、empty-replay-tests.log | 源流故障/限额，以及空范围/纯文本/源变化后重放通过 |
| remote-final.log | 真实组件＋候选HTTP＋独占进程重启通过，14.20秒 |
| remote-final.log.cleanup.json | 独占schema、bucket删除成功，记录本轮准确归属 |
| remote-final.log.transport.json | 独占Redis停止、转发停止、远程工作根删除均为true |
| cleanup-inspect-final.log | 中断续作后重新SSH只读检查，无rb-backend临时目录、候选进程或独占Redis pid残留 |
| baseline-before.json、baseline-after.json | 原4个scope的environment_id/scope_id、freeze状态/revision、active逐项一致；不代表比较了全部配置正文或抹除了审计历史 |

真实隔离采用同PG数据库内唯一schema（独立search_path和独立迁移表）、全新唯一MinIO桶、redis-0容器内额外loopback Redis进程（独立空目录、禁止持久化）、节点唯一/var/tmp/rb-backend-*目录与loopback端口。没有改共享Redis配置、执行FLUSH或复用业务缓存；没有迁移业务schema、向共享MinIO桶上传、改Service/Ingress。测试创建的临时管理账号在测试schema内；基线检查使用独立网页登录会话并登出。

夹具调试阶段的已有schema/bucket清理清单也均成功（remote-components.log.cleanup.json、remote-components-http.log.cleanup.json）。初次独占Redis启动曾因缺少独立dir加载已有RDB，夹具发现DB非空立即拒绝，未运行写测试；已停止该额外进程并改为全新空目录，未flush或修改共享Redis。当前没有已知本轮待清理远程资源。源码、测试脚本与上述日志保留，原远程测试成果和前端并行改动未覆盖。

### 7.6 架构验收断言补强（2026-10-29）

架构验收发现初次真实组件测试使用isLatest=true，实际进入ListLatestByScope而没有按type过滤；原遍历断言在TEXT缺失时也会跳过。因此原remote-final.log不能单独证明真实类型过滤缓存刷新，本节补跑结果替代该项证据。不修改产品实现或扩大筛选语义。

仅修改backend_remote_components_test.go与backend_remote_http_test.go的断言：

- text/object均明确isLatest=false。先预热空类型列表，创建TEXT后断言text恰为该成员、object为空；更新后断言TEXT的resource_key、内容和edit_revision均与更新结果一致。
- 预热object空缓存后导入JSON，断言object恰为JSON、text恰为TEXT。迁移/导入后从持久化详情取得当前身份与编辑版本，再预热两种类型列表；删除TEXT后断言text严格为空、object仍恰为原JSON且内容/编辑版本不变。
- 共用检查器先比条数，再按alias核对精确成员集合、resource_key、content和edit_revision并移除已匹配项，缺成员、多成员或重复成员均失败，不以空循环作为成功。
- HTTP相同request_id第二次create必须返回原snapshot_id及version；每次匿名fixed runtime必须恰好找到一个FILE，且下载返回200及原始字节。该检查在临时进程重启前后均执行。

重新交叉编译service.test，复用上一轮未改变的候选产品二进制，只执行一轮既有独占远程fixture；没有重跑本地整包回归、构建前端或操作现有deployment。TestBackendRemoteComponents通过，耗时14.15秒，日志.local-qa/backend-fixes/acceptance-strengthened.log明确记录latest=false精确类型断言通过；同轮HTTP重放/资源断言也通过。产品源码没有变化，先前受影响包回归结果继续适用。

本轮临时归属：schema为rbqa_d3c392d1e6df4359886ff012bebbab25，MinIO桶为rbqa-d3c392d1e6df4359886ff012bebbab25，工作根为/var/tmp/rb-backend-20261029T080405，独占Redis端口31400。acceptance-strengthened.log.cleanup.json记录schema/bucket清理成功；acceptance-strengthened.log.transport.json记录Redis停止、转发停止、工作根删除均为true。随后再次只读SSH inspect，acceptance-cleanup-inspect.log显示无本轮候选进程、rb-backend目录或独占Redis pid残留。没有已知清理遗留。

§7.2同时修正旧读回填的时间描述：详情/map最多可保留该次SET后的1小时、列表30分钟，除非更早有后续成功失效；本次未通过generation协议消除此竞态。部署及真实Pod重建验收仍为§7.4所列待办。

## 8. 架构验收结论（2026-10-29）

结论：**A缓存键失效修复、B MinIO源发布实现及约定范围的隔离验证验收通过；C只验收部署方案交付，实际部署与Pod重建持久化未完成。** 当前远程v5.0.3没有被更新，不能向用户宣称现场问题已经消失。本结论不覆盖前端修复验收。

架构师只读核对了config_cache.go与各写入口的身份来源、GenerateScopedConfig*读取键、SCAN失效范围，release_source.go的条件读取/流关闭/错误分类、release_snapshot.go的scope与大小校验、release_storage.go的有界复制/摘要复读，以及原事务/幂等路径的保留情况；确认05ff46b与c49194a差异文件清单与§7.1一致。未发现本批需退回修改的产品实现问题。

证据核对包含backend-tests-final.log、vet-final.log、source-tests.log、empty-replay-tests.log、remote-final.log及补强后的acceptance-strengthened.log；核对测试实现确实执行latestOnly=false类型过滤的精确集合/内容/revision断言、HTTP重放快照与版本一致、FILE必须存在并下载成功。补强用例由虹桥研发执行，架构师没有代跑测试。补强前的弱断言不作为对应验收依据。

清理核对：原4scope的baseline-before/after解析结果一致；原轮及补强轮的schema/bucket清理、Redis/转发停止、工作根删除均有成功记录，acceptance-cleanup-inspect.log没有候选进程或临时目录残留。结论限于本轮资源与已观测业务元数据，不等同全库内容逐字节比较。

保留边界：在途旧读仍可能在DEL后回填旧缓存，详情/map的TTL为1小时、列表30分钟，未取得强一致性保证；本批修复解决已复现的键体系不一致，不将该竞态标为已修。部署前仍须把远程已有HTTP会话与ARM64打包修正纳入同一产物、确认PVC/权限/容量并挂载快照目录，部署后再做同一卷Pod重建验证。PG+MinIO+Redis整实例备份仍不支持。没有因本次验收获得推送、发布或更新现有服务的授权。

## 9. 用户新增决定：MinIO快照区与历史文件迁移（2026-10-29，当前实施合同）

### 9.1 目标和范围变更

用户明确要求一步到位：原始资源和不可变发布资源都使用MinIO，分开原始资源区与快照区；所有对象完整上传并校验后才能使用版本，上传中途失败可追踪清理，原始资源变化不影响历史，已有文件快照安全迁移。

本节替代§1/§4/§7/§8关于“本批快照继续文件目录、新部署必须快照PVC”的未来实施决定，前批已完成的代码与验收事实不追溯改写。上批local/MinIO源读取、流限额和scope缓存修复复用。deploy/kubernetes/release-snapshots/标为历史local方案，新目标给MinIO快照部署配置；迁移未完成时local历史文件仍需保留，不能提前去掉卷。

本批交付：可选择local/MinIO的新快照写入、按版本定位的混合历史读取、MinIO发布完整性、可恢复失败清理、文件快照到MinIO的迁移命令和真实组件验证。API URL/字段与前端操作不改，payload和发布索引仍在业务数据库，MinIO保存独立副本资源及存储校验清单。临时ZIP/有界临时文件允许使用可清理的临时盘，不能成为永久快照依赖。

本批不扩展整实例PG/MinIO备份恢复，不修上批披露的缓存强一致竞态，不把存储共享改造宣传成全系统多Pod验收通过。允许原研发自主实现/测试和独占远程实验；现有远程部署、业务库迁移与历史快照真实切换、旧文件物理删除、推送/发布均留到具体交付后再授权。迁移工具在独占夹具实际执行，对现有实例仅可只读盘点/dry-run，不自动执行历史切换。

### 9.2 配置、隔离和版本定位

**存储隔离决定：使用独立私有bucket作为快照区。** 同一MinIO服务的原始bucket保持不变；快照bucket不得与原始bucket相同，不能使用原始file_id路径或可变资源对象作为历史副本。无需部署第二套MinIO；独立bucket便于限制写删权限及避免原始资源清理误伤。禁止公开bucket、公开MinIO凭据或将签名URL替代虹桥资源API。

新增最小配置块（建议release_storage）：type=local|minio；缺省为local以兼容旧配置。MinIO模式必须显式给快照bucket（如rainbow-bridge-snapshots）；连接默认复用现有storage.minio的endpoint/region/SSL/凭据，快照bucket不回退到原始bucket。不另加一套账号系统或多存储profile框架。连接将来需独立凭据可按既有配置机制扩展，本批不要求。示例只使用占位符，不复制真实Secret。配置选MinIO但bucket不合法/不可访问时明确失败，禁止静默写回文件目录。

独立bucket由部署准备步骤显式创建并校验权限，不在业务请求中悄悄建桶。最低校验包括读写所需权限；清理命令需要其自身删除/列举权限。用户业务API仍通过现有Cookie/CSRF/Origin与公开runtime合同，不新增前端配置项。运行时只读权限失败不可伪装成文件不存在。

**每个已提交版本有持久存储定位，不能由全局开关猜测。** 在手写模型/DAO中增加存储定位元数据（可同ConfigReleaseVersion新增字段）：backend、bucket、对象根前缀/locator schema版本；不保存凭据、绝对源路径或用户提供的任意URL。旧行缺省/空backend解释为legacy local，旧RAINBOW_RELEASE_DIR规则继续支持其读取。新版本记实际写入定位。此元数据不混进已冻结payload/manifest摘要，不改变原snapshot_id、version、asset_id、request_id、scope身份或active_revision。

MinIO对象根建议snapshots/v1/<snapshot_uuid>/<attempt_uuid>/，资源键固定为assets/<asset_uuid>.bin；尾部对象为完整性清单（如manifest.json），包含schema、snapshot/attempt身份、完整资源集合及size/SHA256、payload/manifest摘要。UUID由服务端生成，记录中真实locator是定位权威，不按当前全局bucket替换旧bucket。bucket/endpoint变动时旧数据连接必须保持可用或先完成显式迁移，不进行故障fallback。

快照对象仅由本次拥有的写流程新建；既有已提交locator禁止覆盖。对象存在时只有确认本次同一attempt且内容匹配才可复用，冲突明确拒绝，不能盲目Put覆盖。MinIO的ETag只能作为传输一致性令牌，完整性依据仍为流字节数和SHA256。不得通过硬链接/仅记原始对象引用实现“复制”。

### 9.3 完整发布协议：对象先准备，数据库后可见

MinIO不提供目录rename或跨对象数据库事务，不复制原来的moveStagingToFinal假语义。将“版本已提交且定位清单校验成立”作为应用可见边界：

1. 入口保持request_id指纹幂等：已有提交记录直接重放；冲突字段拒绝。并发相同request保持原写门控及数据库唯一约束。未提交attempt不能冒充成功版本，重试不能复用清理中的对象空间。
2. 第一次发出对象写请求前持久登记独占attempt的归属：attempt_id、snapshot_id、request_id/指纹、scope_id、bucket/prefix、状态、时间、需清理/未决原因。该登记必须在业务事务回滚或进程崩溃后仍存在；可增加小型存储操作表和相应DAO，不把半成品插进正式版本表。必须在业务大事务开始前独立落库或采用等效可证明机制，不能在同一最终回滚事务内写“持久日志”。SQLite事务锁约束也要验证，不能靠嵌套连接写锁死。
3. 在现有协调流程下核对scope、冻结状态/revision与配置/资源计划；复用源读取、大小/数量/总量限制及上下文。全部对象写入独占attempt根，流式上传，有界临时缓冲/临时文件可用，禁止无界ReadAll/整版内存缓存。每份先记录预期大小/摘要并核对实际上传字节；实际重新GET快照对象计算size/SHA256，不能只检查上传成功码或用户自填metadata。
4. 全部资源验证成功后，最后写完整性清单并GET核对内容/摘要/资源集合；十张图就是十张完整成功，空范围/无资源版本也有可验证的完整性清单。
5. 仅在上述准备全部成功后，同一业务DB事务提交正式版本、资源清单、存储定位、创建事件及自动编号结果。创建不自动生效。attempt的结果标记可同事务或提交后确认更新；即使结果标记写入失败，真实已提交版本的locator仍是不可删除的权威引用。不存在“对象没齐先插可用版本再补传”的窗口。
6. 响应丢失/DB提交结果不明，使用独立、有界、脱离已取消请求的确认上下文检查request_id及snapshot/locator是否已提交；已提交保留对象并按原语义允许确认重放。不能确认则保留attempt为unknown，不报告创建成功、不立即删除对象。旧业务active不变，不能把可能已提交目录当失败残留处理。

整个流程维持冻结/身份并发保护，跨网络期间锁/事务超时按照既有发布上限有界；原有版本计数、revision不因失败出现错误可用版本或复用已提交号。可复用现有写门控而不重写分布式事务框架。实现前若发现必须改变锁时序/幂等公共语义，先给具体代码证据和最小偏差方案，不能自行弱化保护。

### 9.4 失败清理和崩溃恢复

- 正常失败：DB确认本attempt未提交后，立即用独立清理ctx和有界重试清理**本attempt精确prefix**。逐项核对RemoveObjects错误通道、再次列举确认空，不把HTTP200当批量全删成功。中断multipart需显式abort或记录uploadID供恢复；盘点未完成multipart，不能只检查可见对象为空。
- 删除失败、网络不明或清理超时：持久标为cleanup_pending/unknown并保存精确归属，原始失败不被清理错误掩盖；对外不返回半成品成功，回传可诊断ID而非Secret。无已提交版本引用时的孤儿对象不是可用发布，但必须有补清路径，不能只写一行stdout就丢失线索。
- 增加运维CLI的inspect/dry-run/reconcile（可与迁移命令同cmd/release-storage工具）。默认只读，显示attempt/已提交引用/对象及multipart残留和建议动作。正常同步清理后写终态；已提交版本（包括逻辑删除/已删scope历史）的对象永远不由“失败残留清理”删除。
- 本批为小团队采用保守恢复，不建设自动分布式GC：pending/unknown不能仅按年龄或“DB里暂时查不到版本”在线删。执行崩溃attempt补清必须进入经核实的发布写停用/无在途写者窗口，并在清理前重新检查版本和定位、事务已结束、attempt不再可能提交；无法建立停写证明则只dry-run保留，不猜测安全。提供可执行检查/guard，不仅在README警告。删除必须绑定明确attempt ID及归属，防与仍存活的重试/创建争抢。
- guard复用现有freeze_guard.go的DB协调行锁，不另信Redis锁TTL：创建在登记attempt后进入DB写门控，取得协调锁后重新检查attempt状态；reconcile execute也必须持有同一协调锁，在锁内确认无已提交引用并CAS把目标attempt置为禁止提交的清理状态。排队创建发现attempt已被清理认领必须拒绝，不能拿它继续上传/提交。任何不在此门控内的上传工作都不能在事后提交版本。SQLite使用同一锁内连接读取引用/更新状态，避免锁内另开连接死锁；无法证明与所有写者采用同一门控时execute拒绝，不能用命令行--force绕过。对进程崩溃后的未完成multipart同样登记和精确清理。
- 不自动物理清理逻辑删除版本，不改变现有删除/范围删除的历史保留语义。原始bucket清理绝不扫描快照bucket，反之亦然。禁止TTL生命周期规则自动删除任何已提交快照prefix。

### 9.5 所有读取/导出统一走快照存储

本次不能仅改创建入口。建立窄接口及local/MinIO两个实现，提供按持久locator读取、Stat/校验、流打开、写准备/确认/清理所需方法；不用any/反射/巨大通用存储框架。保留来源读取与快照存储两个职责，避免原始对象与快照对象混用。

逐项核对改造：release_service.go创建/激活与完整性校验、release_runtime.go active/fixed/旧runtime与资源下载、release_export.go业务静态包、transfer_export_plan.go active/version迁移归档；比较/详情若会读资源也必须采用同一定位。现存verifyFile、assetPath、os.Open、os.ReadFile的直接依赖不能漏掉。读local旧版本不要求MinIO健康，读MinIO版本不要求原始文件或本地永久目录存在。

资源返回前按既有合同校验完整摘要；runtime/激活至少保持既有payload、manifest、资源集合与存在/长度保护，不能因远程成本跳过当前校验。下载/ZIP读取流必须有大小边界、ctx和关闭；导出未完整生成前不得交成功ZIP（避免流一半后才发现损坏却已经200）。保留既有API路径、BasePath、Content-Type、scope pin、404/410/503、active_revision和raw大整数规则；不向客户端返回内部bucket/key/签名凭据。精确错误沿用现有RELEASE_*体系，新增细分确需改IDL时走hz生成，不手改生成代码。

### 9.6 已有文件快照迁移

交付cmd/release-storage下迁移工具及使用说明，默认plan/dry-run，execute显式确认配置/实例身份及选中snapshot清单。此工具只改变存储定位，不做业务导入，不改变版本内容、创建时间、版本号、删除标志、创建幂等记录、审核历史、active snapshot/revision或scope身份。

1. 盘点全部版本，包括非生效版本、逻辑删除版本及已删scope的历史。按现有DB的payload摘要、清单摘要、资源集合/数量/大小/SHA和真实local文件逐一验证；盘点旧目录的未提交残留另列，不当作版本迁移。路径边界/symlink遵循原规则。无法读到旧文件、摘要损坏或DB错误必须明确失败，不能从当前原始资源重建冒充旧快照。
2. 为每个snapshot创建独占migration attempt，写入新快照bucket和清单、完整回读校验；上传失败/重跑遵循§9.3—4归属与清理。可逐版本推进，失败保留该版本local定位，不阻断其他独立可迁版本；最终报告总数与每条结果，部分失败非全量成功。
3. 完整验证后，用事务/CAS仅把该snapshot的存储locator从原local值切为已校验的MinIO值；检查payload/manifest摘要及原定位仍匹配。正式读者切换前继续local，切换后用MinIO；在途读者用已捕获的旧locator读取，源文件仍保留，不能读半迁移混合状态。若版本删除/active切换并发，不覆盖其生命周期/指针；版本物理保留，因此不为了迁移解锁或重新创建业务版本。
4. 再次执行已迁移版本：核对MinIO完整性并skip/resume，不重复创建版本、不覆盖对象、不变revision。需持久记录旧local定位用于回退核对；确认旧文件仍完整才能显式回退locator，不能把指针退回已不存在文件。回退不还原整库、不把原始资源当快照。
5. **迁移不自动删除local源文件。** 出具迁移前后资源字节/SHA、active/fixed/runtime、业务ZIP和transfer version归档对照。只有所有仍需读历史均迁移并完成应用验证后，才可在另行授权维护窗口移除旧卷/文件。本次不给用户“旧卷可以立即删”的结论。

若现场确实没有历史成功版本，也要用多版本local夹具覆盖完整迁移，不能省略工具；对现场只读给出精确盘点，不能以之前一轮空list永远假定零历史。兼容迁移期间local+MinIO混合，MinIO坏了不自动读原始bucket或猜local副本兜底。

### 9.7 模型、备份兼容与工程拆分

允许修改biz/dal/model手写实体、DAO、initialize.go注册迁移、pkg/config校验和必要cmd；新增定位/attempt表或列采取可兼容的增量默认。旧行明确解释local，未知backend不能猜成local。配置/locator不含secret，不能让HTTP模型自动序列化出内部对象定位。业务协议不变，不无必要引入IDL字段。

保持SQLite+原始local+快照local既有整实例备份恢复功能及旧包候选库升级。新增MinIO快照模式或库存中仍有MinIO locator时，备份能力应明确unsupported，不能只检测原始storage.type而漏掉远程快照，产出“不包含快照却声称完整”的包。核对biz/backup/schema.go、validate、路径与模型迁移：旧包缺新增列按local恢复；若新持久模型影响备份清单/schema版本，明确实际兼容处理与用例，不绕过校验。不得把attempt运行状态带入恢复后当成正在进行的在线写任务自动删除对象。PG模式备份能力仍不扩展。

建议职责：release_snapshot_store接口与locator，local实现复用release_storage，minio实现独立文件；attempt/cleanup协调独立；migration计划/执行/CLI独立；release_service保留业务编排。reuse已有release_source条件读取/限额/ctx，不把所有逻辑塞进release_service.go或一个千行文件。允许合理私有组织，开始前读取 $engineering-quality-preserving-refactor。

### 9.8 验收用例与真实实验

| 用例 | 必须验证的结果 |
| --- | --- |
| 十资源完整创建 | 原始区十对象复制到快照区，逐一回读size/SHA和清单；版本只在全部成功后可见，初始不生效，激活后URL/字节正确 |
| 第N个上传/回读失败、超限、取消 | 正式版本不存在、active不变；本attempt对象/multipart清理成功或持久cleanup_pending；绝不删其他attempt/原始对象 |
| DB回滚/提交响应不明/响应丢失 | 回滚对象可清；unknown保留；提交成功的版本通过原request_id重放定位，reconcile不能删除其资源 |
| 上传后进程崩溃 | durable attempt可查；新实例能区分已提交与孤儿；恢复清理在停写guard下执行，不仅凭年龄删除 |
| 并发同request/不同request | 保持scope/revision及唯一约束；不同attempt绝不清理对方，相同请求不产生两个可用版本 |
| 原始对象改写/删除 | active/fixed和发布资源、ZIP仍是原快照字节；原始资源清理不触碰快照区 |
| 资源损坏/缺失/MinIO不可用 | 沿用完整性失败/存储错误，不能回编辑区、原始对象或返回成功空内容；私有bucket不能匿名直读 |
| 本地永久目录不可用 | MinIO新版本创建/激活/runtime/下载/新旧静态包/transfer active与version导出均可用；临时导出目录独立有界可清理 |
| 两个独立候选实例 | 共享隔离PG/MinIO/Redis且local永久目录各自不同/空，实例A创建的MinIO版本实例B可读取/生效/导出；不冒充全系统K8s多副本验收 |
| 历史迁移 | 至少两个local版本含active、非active、删除历史及图片；逐字节/摘要对照，故障中断续作，切换前后API/版本号/revision不变；缺失local保持失败不改定位 |
| local兼容与备份 | 原local读写、schema升级与旧备份恢复不退化；original local + snapshot minio不能声称支持完整备份 |

故障注入可用S3/DB可控夹具作确定性边界，成功主链、迁移和跨实例读取必须有真实远程PG+MinIO证据；测试层次分开。测试不在现有业务实例撤权、断网、杀Pod、改数据。沿用现有独占schema/桶/Redis/临时进程方案，快照桶和原始桶均本轮独占，维护归属清单并finally安全清理、复查无遗留、核对原4scope元数据。不要在本地安装三件套。

生成可审查部署配置，显式分开原始与快照bucket、权限/无TTL清理规则、迁移顺序及回退版本要求。local模式旧二进制不能读MinIO locator，因此新增数据后不能无条件回退旧版；给出安全回退限制，不通过改backend值假装回退。保留远程c49194a的HTTP会话与ARM64打包修正，集成构建仍不得覆盖前端并行成果。

### 9.9 实施顺序与交付

1. 盘点上面全部路径、确定增量模型/配置和attempt持久策略，先在本PRD登记关键决定（不为普通实现等架构师逐条审批）。
2. 实现store接口与local兼容，MinIO写入/校验/正式提交边界及按locator统一读/导出；增量迁移校验。
3. 实现失败持久记录、精确同步清理及保守reconcile guard，补半途失败/提交未知/崩溃测试。
4. 实现旧local迁移plan/execute/verify/回退核对，覆盖中断、部分失败及混合读取；检查备份边界。
5. 独占远程真实主链/迁移/跨实例验证，清理和基线复核，提供部署与实际迁移待办，不应用现有环境。

在本文件§10追加本批实施登记，区分产品实现、真实/夹具验证、数据迁移工具验证、现场盘点和部署未完成项。列实际修改/命令/证据、旧数据兼容、cleanup_pending恢复示例与最终清理、API不变量和设计偏差。历史§7/8验收仅代表上批，不可改为本批通过。不向其他聊天/代理转派。

派发状态：2026-10-29 已发送至原虹桥研发（01a1333a-342c-7903-8ca8-164b82392399），工具确认送达；本批实现、工具及隔离验证已完成并登记§10，待架构验收。现有部署和业务历史未切换。前端无需新增功能，原request-id修复保持原任务独立。

## 10. MinIO快照区实施登记

状态：产品实现、故障夹具、真实组件及迁移工具验证已完成，待架构验收；未部署、未推送、未执行业务历史迁移。下列五项为实施前登记的决定，实际结果及限制见§10.1—10.6。

- 增量扩展ConfigReleaseVersion的内部backend/bucket/prefix/attempt及旧local回退标记，空backend按local；新增独立持久attempt表。attempt在进入现有DB写门控前独立提交，门控内重读状态后才允许上传；清理复用同门控检查全部历史引用并CAS认领，认领后排队创建拒绝继续。
- MinIO准备使用本轮独占系统临时目录作为有界流复制/哈希缓冲，不依赖RAINBOW_RELEASE_DIR；上传已校验资源，最后上传完整性清单，逐对象GET核对后才插正式版本。每attempt唯一前缀，不模拟rename；上传使用有界单PUT避免新增multipart，清理仍盘点并abort精确前缀multipart。
- 将读取收敛到按版本locator选择的窄资源接口，覆盖激活、runtime、资源、ZIP、transfer归档；旧local保留原路径规则。MinIO清单与DB摘要、资源集合匹配后读取，不公开定位。
- 运维工具复用Service门控，默认plan/inspect，只在显式实例确认和选定ID后execute；迁移逐版本完整校验后CAS切locator，源文件保留。reconcile锁内认领为禁止提交状态，失败持久pending，未知提交不盲删。
- 按职责新增store/MinIO/attempt/maintenance文件及cmd，先local兼容和模型/备份边界，再故障夹具和独占远程十资源、历史迁移及跨实例验证；本地不安装中间件，不修改vue。

### 10.1 产品实现与旧数据兼容

| 职责 | 实际文件及行为 |
| --- | --- |
| 配置、定位 | pkg/config/config.go新增release_storage.type/bucket；biz/dal/model/release.go新增StorageBackend/Bucket/Prefix/Attempt及LegacyLocal，均json:"-"。空backend按legacy local，未知值拒绝；initialize.go注册attempt表增量迁移 |
| 窄读取接口 | release_snapshot_store.go的releaseAssetStore提供资源打开/读取/校验，localSnapshotStore复用release_storage.go，MinIO独立实现；按捕获版本行选store，不随全局开关重定向历史 |
| 对象准备 | release_snapshot_minio.go复用MinIO连接、独立bucket；snapshots/v1/<snapshot_uuid>/<attempt_uuid>/内固定assets/<uuid>.bin及最后的manifest.json；不模拟rename，不将原始可变引用作为副本 |
| 发布编排 | release_service.go在事务前独立登记attempt，原门控内重读pending；有界捕获源→上传→实际GET核对size/SHA→完整性清单上传/GET→同事务写版本/清单/locator/事件/committed。创建不自动生效 |
| 恢复归属 | model/release_attempt.go、release_storage_attempt.go；pending/unknown/cleanup_pending/cleaned/committed持久状态。独立20秒ctx确认提交，无法确认保留unknown；正式locator优先 |
| 维护工具 | release_storage_maintenance.go、release_storage_inventory.go及cmd/release-storage；默认只读事务，SQLite只读打开且要求已有库，不AutoMigrate；逐版本校验、CAS迁移/重跑、显式回退，源文件保留 |
| 全读路径 | release_service.go激活；release_runtime.go active/fixed/旧runtime/资源；release_export.go业务ZIP及旧静态入口；transfer_export_plan.go active/version归档统一locator，未保留这些入口直接拼永久本地资源路径 |

源读取复用前批release_source.go，不重新翻修缓存。MinIO新快照用独占系统TempDir有界捕获，沿用单资源32MiB/总100MiB等限制，正常返回各路径移除临时文件；不解析RAINBOW_RELEASE_DIR。上传单PUT禁用multipart以避免新增分片，恢复仍盘点已有分片。相同attempt已有对象必须GET大小/摘要一致才复用，冲突不覆盖。ETag不作SHA；完整性清单含schema=1、snapshot/attempt、DB payload/manifest摘要和完整资源集合，核对数量/总字节。

下载/ZIP流有大小上限、ctx和Close；ZIP完整生成校验后才交成功。local资源拒绝路径symlink，只读查历史不创建缺失根。runtime对两种store均校验引用资源SHA，比旧版仅长度检查更严格：同长度损坏也返回既有RELEASE_INCOMPLETE，未改API。时间/空间仍按资源字节线性增长，增加上传后GET的网络/I/O成本，不承诺性能提升；没有新增整版内存缓冲。沿用transfer既有有界数据计划。IDL/生成文件及vue未修改。

### 10.2 清理guard与运维恢复

attempt第一次独立Create发生在withWriteGate之前；SQLite单连接测试没有锁内另开连接登记死锁。创建/迁移获得config_write_gate(id=1)后检查pending，全部上传和正式定位提交在锁内。reconcile同锁内查全部历史引用（含删除/已删scope），CAS持久认领cleanup_pending；第二阶段仍持同锁复核、逐对象版本删除、逐multipart abort、重新列举为空后写cleaned。两阶段之间崩溃可重跑，排队创建见认领状态拒绝，清理重试也串行。没有按年龄/TTL猜删，没有force绕过；外部绕过此协议直写DB/对象的脚本必须停用，不能纳入门控证明。

删除/abort失败保留cleanup_pending，提交确认失败保留unknown或原pending归属；不将失败当成功、不丢失归属。对外存储错误可附attempt UUID，不输出Secret。提交/清理使用脱离取消请求的有界ctx，未完成由工具补清；不自动GC已提交、删除历史或迁移回退后保留的远程副本。

操作详见cmd/release-storage/README.md，恢复示例：

```text
release-storage --config <private-config> --action inspect
release-storage --config <private-config> --action reconcile --execute \
  --confirm-instance <plan的Instance> --attempt <明确attempt_uuid>
```

inspect显示版本完整性、attempt/正式引用、对象版本和multipart数量；LocalDirectories另列未提交目录、staging和异常文件/链接，不遍历链接、不自动删除。默认plan不会改DB/文件，root_unavailable不等同没有残留。migrate/rollback要求明确snapshot列表与实例确认；逐版本执行，部分失败返回非零、其他成功保留。旧local文件不删。迁移仅CAS定位/LegacyLocal，不改payload/清单摘要、编号、创建信息、删除历史、active/revision；已迁重跑只校验，不从原始区重建缺失历史。

### 10.3 本地与确定性故障证据

日志在本地忽略目录.local-qa/backend-fixes/snapshot-minio/：

| 日志 | 实际验证 |
| --- | --- |
| go-all-final.log | go test ./...通过；service 58.411秒、backup 8.678秒，含既有local、scope/冻结/并发/导出/备份恢复回归；未配真实fixture时远程用例明确skip |
| regression-final.log | 此前受影响包回归通过，service 54.661秒 |
| recovery-tests.log | S3夹具第N次上传/回读失败、权限拒绝、DB回滚、清理失败pending与补清、同request并发重放、失败保持active/别的attempt、local迁移/回退通过 |
| recovery-tests.log | 真实子进程在after_snapshot_upload及after_commit退出97：前者正式版本为0且durable pending可补清；后者正式locator保留，即使marker=unknown仍拒绝清理，request重放确认。同机夹具不冒充生产PG不确定提交故障 |
| multipart-recovery.log | owned与foreign分片夹具，abort失败保持pending，补清只删owned，foreign保留 |
| boundaries-final.log、last-boundary-tests.log | 旧版本、schema升级、备份与版本列举清理边界通过 |
| final-extra-boundaries.log | MinIO空范围/纯文本均有完整性清单；取消无正式版本且attempt清理；全局切local/新桶仍读旧定位；缺清单/未知backend拒绝且不回源 |
| inventory-tests.log、cli-local-plan.log | SQLite默认plan文件mtime/size不变，缺根不创建，已提交/未提交/staging目录分别盘点 |
| vet-final.log | 受影响service/backup/config/CLI go vet无诊断 |

备份schema升4，严格allowlist增加版本定位列，attempt运行状态不进恢复包。旧schema1/2/3在隔离候选库升级为local；schema2/3测试实际删新列后校验。SQLite+原始local+快照local整包回归通过。快照配置MinIO或库存含MinIO locator（包括删除历史）时明确unsupported；实际任务也在一致DB副本检查，不能产缺快照的完整包。PG备份未扩展。

首次本地测试受Windows系统TEMP目录rename句柄错误影响，后续沿用.local-qa/tmp设置TEMP/TMP完成验证，未改local rename协议来绕过。旧runtime测试允许同长度损坏，已按更严格校验更新并通过。

### 10.4 真实远程主链、历史迁移与跨进程

Linux ARM64交叉编译service.test、dev候选及release-storage CLI；候选为snapshot-minio-candidate / 05ff46b-plus-minio-snapshot-work。已定向保留c49194a的HTTP会话显式allow_insecure_remote及Dockerfile.Remote.BIN架构修正，配置测试通过；未整体merge远程、未碰前端成果。

remote-final.log通过（22.96秒），使用独立PG schema、两只独占MinIO桶及额外独占Redis：

- 十张有效PNG写原始区再复制快照区，每份实际GET字节/size/SHA及完整性清单通过；新版本初始不生效。永久快照根不可用时仍能创建，DB与对象恰为10份。
- 三份真实local版本（active、非active、逻辑删除）均含图片，实际运行维护CLI execute迁移；故意临时移走其中一份历史后明确失败、不切定位，恢复后重跑成功。中间混合local/MinIO可读，已迁重跑不增版本。static ZIP逐字节、runtime结构、transfer version资源字节一致；版本除定位外逐项相同，head/revision不变，旧文件仍在。
- 第二Service实例能生效/读active/fixed/业务ZIP及transfer active/version归档。删除独占原始区对象后仍读原快照字节；快照桶匿名直接GET为403/404。
- 真实HTTP登录、上传、创建/重放、比较、生效、匿名runtime/FILE下载、新旧静态包通过。进程A创建后停止，从另一空工作目录启动进程B，同隔离PG/MinIO/Redis、无本地永久目录，由B比较/生效/读取/导出，再启动另一空目录进程仍可读取。重放必须同snapshot/version，FILE必须存在并下载一致。

跨进程为顺序启动的独立候选实例，不声称两个K8s Pod同时运行或全系统多副本已验收。权限/断流/未知提交采用夹具，不在共享中间件撤权断网；真实单PUT没有人为制造multipart失败，由协议夹具证明精确恢复。

### 10.5 现场只读盘点与清理

live-inventory-final.log在现场DB只读事务执行plan：正式版本0（包括删除历史），旧现场未部署attempt表，没有记录；未AutoMigrate或切定位。另只读kubectl检查实际Pod的/app/data/release-snapshots，返回LIVE_LOCAL_ROOT_ABSENT。工具独占目录的root_unavailable本身不作现场磁盘证据。盘点只代表2026-10-29本次观测，执行前必须重新plan。

最终测试归属：PG schema rbqa_010f718bddfb4b6c9a10e07c5e00bc4c，原始桶rbqa-010f718bddfb4b6c9a10e07c5e00bc4c，快照桶rbsnap-97ac3c66-920a-4dd9-be6f-b1fd1ed9a7bd；remote-final.log.cleanup.json三项删除均成功。Redis额外端口26800、工作根/var/tmp/rb-backend-20261029T090353；transport.json记录Redis/转发停止、根删除均true。TMPDIR和测试快照/ZIP在独占根内，finally清理。此前remote-test.log轮（23.08秒）也通过且全清理；两次只读inventory上传根rootRemoved=true，未启动Redis/转发，对应false表示未创建而非清理失败。

cleanup-inspect-final.log再次只读核查无本轮目录、候选进程或额外Redis pid残留。snapshot-minio/baseline-before/after.json的原4scope身份、freeze状态/revision、active完全一致，独立会话已登出；不等同全部业务正文逐字节比较。没有停/重启原服务、改ConfigMap/Ingress/Service、FLUSH或清共享业务桶。没有已知远程清理遗留；未提交、推送、tag/release，未向其他聊天发送消息或转派代理。

### 10.6 部署交付及未执行项

deploy/kubernetes/release-snapshots/minio-snapshots.example.yaml及minio-snapshots.md明确私有独立桶、Get/Put/List和补清Delete/Abort/ListVersions权限、禁止TTL、容量/临时盘、迁移和旧二进制回退限制；原PVC文档标历史local方案。有local历史就保留原卷；生产业务迁移、部署应用、旧卷/文件删除本轮均未执行。

上线前仍需审核镜像digest和增量DDL、备份业务库、预建私有桶并核实生命周期/对象锁、注入配置并重新plan。真实业务迁移execute及旧卷移除需另行授权。原生MinIO版本没有local副本，旧二进制不识别locator，不能改backend或直接退旧镜像；迁移版本也须完整验证旧文件再显式回退，远程副本保留。PG整实例备份、多Pod调度、缓存线性一致性不在本批完成范围。

## 11. MinIO快照区架构验收与推送范围（2026-10-29）

验收结论：§9约定的产品实现与隔离验证通过，准予按用户本轮授权整理提交并推送origin/main_v1。此结论不等于现有运行服务已更新或现场历史已执行迁移。前批§8文件快照验收保留历史含义，新实现按每版本locator读取local/MinIO。

架构师只读检查release_snapshot_store/minio、release_storage_attempt/maintenance/inventory、创建/激活/runtime/资源/业务ZIP/transfer导出调用链、模型默认与json隐藏字段、CLI只读/显式执行和备份schema4升级边界。确认上传回读与清单完成先于正式版本提交，attempt独立登记、DB门控认领与清理时引用复核，迁移不改业务版本及active/revision、源文件保留，已提交及回退后保留的attempt不自动GC。没有发现本批阻断推送的实现问题。

核对证据：snapshot-minio/go-all-final.log全仓Go回归通过；recovery-tests及multipart-recovery覆盖故障、崩溃、unknown与清理guard；final-extra-boundaries覆盖无资源清单、取消、历史locator与未知backend；remote-final.log真实十资源、三个历史local版本迁移、候选HTTP及跨进程读取通过。核对测试源码与日志中的实际断言，明确跨进程为顺序独立进程，不宣称全系统双Pod并发验收。架构师未代跑测试。

清理核对：两只独占MinIO桶、PG schema删除及Redis/转发停止/远程根删除记录成功，cleanup-inspect-final.log无本轮候选进程或临时目录；原4scope基线JSON逐项一致。现场plan为零版本且实际Pod旧快照根不存在，只代表本次只读盘点，部署前重新核实。

推送内容包含前批scope缓存修复、MinIO源适配、本批MinIO快照及迁移恢复工具、必要测试/部署示例/文档，以及保留远程c49194a已有的HTTP会话与ARM64打包修正。凭据、现场配置、临时二进制和忽略目录下证据不提交。剩余边界仍为§10.6，另保留缓存旧读回填竞态，不借本次推送扩大部署、真实迁移、删除文件/卷或发布授权。
