# 虹桥演示登录与光谱UI：本地整合结果

完成日期：2026-10-20。

当前统一分支：`main_v1`。2026-10-21已将前后端最新成果收回主目录；下文旧工作树与实例地址是历史验收记录。

当前代码目录：`D:/kylin/rainbow_bridge`。启动方式见 [一键启动](../script/start-local.README.md)。

## 最终能做什么

保留光谱工作台界面，增加一次登录、刷新恢复、退出和管理操作鉴权。锁版、解锁、创建版本和切换版本不再要求反复输入管理密钥；原因、revision和原请求确认流程保留。登录不解除锁版，发布快照仍不可修改。

真实页面已经走通：登录 → 编辑配置 → 上传图片 → 导出/预览归档 → 冻结 → 创建v1 → 生效 → 解锁并编辑 → 创建v2 → 切换v2 → 切回v1。匿名应用读取默认版本、固定v2和资源字节均核验通过。退出后原会话不能继续管理。

这里只是演示用本地登录，没有增加注册、IAM协议或权限平台。将来接外部IAM仍从集中身份验证入口替换。

## 怎样整合的

三个来源工作区当时均停在已提交基线 `f6184b497b27aeb2323b86cef8011f60b376a956`，功能尚未提交，直接merge分支名不会带入内容。因此在独立集成工作树按文件来源汇入未提交增量，对重叠文件三方比对：

- 主工作区提供首页生效配置、原文精度读取和固定版本ZIP导出后端。
- `codex/admin-login`工作树提供账号/会话、可信身份、管理路由与公共测试夹具。
- `codex/admin-login-ui`工作树提供完整光谱UI、登录页、auth store、统一请求层和页面测试。

Vue会话实现只采用前端版本。Go发布handler保留原文读取/静态导出，同时使用新身份来源。没有把来源工作区的凭据、启动配置、数据库、缓存或临时编辑脚本复制进代码提交。抽查方式为逐项比较导入清单中的137个来源文件哈希，结果全部未被本轮修改。

整合时修正了这些接缝：

1. 新旧测试签名：ZIP导出测试由旧Bearer参数迁移为可信管理员上下文，业务断言保持。
2. CORS与公开边界：auth接口和管理预检使用精确来源及凭据；业务公开读取独立列举，不能按整个runtime/version前缀豁免。未知API继续走404。
3. 注册路由检查：IDL解析允许不同数量空格，实际枚举46条声明、其中33条管理路由；伪造身份和旧Bearer无法授权。
4. 页面测试登录：直接导航也准备有效会话，浏览器与HTTP夹具共享同一合法会话，先验证me=200，避免空白页登录和Cookie搬运错误反复触发限流。
5. 预览只读验证：会话续期会改变SQLite文件，因此改为比较完整业务表和资源文件字节，不再把正常会话续期误判为预览修改配置。

本机系统TEMP下，Go独立探针和.NET目录移动都曾返回“句柄无效”；换到集成工作树的隔离目录后相同操作成功。产品的原目录发布逻辑保持，未合入复制目录或吞掉错误的兜底。真实发布及测试均使用可正常工作的独立目录。

## 实际验证

| 验证 | 结果 |
| --- | --- |
| Go全仓测试 | `go test -mod=readonly -tags=dev -count=1 ./...`通过 |
| Go vet | `go vet -mod=readonly -tags=dev ./...`通过 |
| Vue构建/类型 | `npm run build`通过；专项脚本的前缀及根路径构建通过 |
| 登录页面Mock | 22/22通过；与真实结果分别统计 |
| 最终可执行程序真实页面 | 2/2通过，直接访问18082，不拦截auth/管理接口 |
| Release专项 | 14/14通过，含首页生效配置、固定快照ZIP、发布页和韧性规格 |
| Freeze专项 | 4/4通过 |
| Full主回归 | 47次执行通过：前缀38 + 三次重启3 + 恢复1 + 根路径5，各阶段退出码0 |

Release证据运行名 `integration-release-20261020`，Freeze为 `integration-freeze-20261020`，Full为 `integration-full-check5-20261020`。结果保存在本工作树 `.dev-integration/evidence/`；最终Go、vet和真实页面输出分别在 `.dev-integration/go-final.log`、`vet-final.log`、`real-final.log`。前面用于定位夹具问题的失败运行保留在隔离目录，不冒充通过结果，不进入提交。

最终Go及真实页面验证在未知API边界修正后执行；专项及Full验证覆盖其余合并代码，未知API新增行为另有中间件回归。没有因为收尾而再次重复未受影响的套件。

本轮未验证外部IAM、真实MySQL/PostgreSQL、MinIO或多副本；它们不是本机演示完成条件。

## 本机怎么查看

完整应用：`http://127.0.0.1:18082/rainbow-bridge/`。

按演示要求，管理员账号与密码均固定为 `admin`。18082 已同步切换，原随机密码失效，需要重新登录。数据库、二进制、运行日志和截图都在被忽略的 `.dev-integration/` 中。该实例使用独立Cookie `rb_admin_integration`，不复用18081的数据。

原仓库的 `start-rainbow.cmd` 也已接入整合版本，默认在18080启动，沿用 `%LOCALAPPDATA%\RainbowBridge\local-demo` 数据；旧演示密码会自动迁移为 `admin`。具体参数见 `script/start-local.README.md`。密码最小长度同步改为5，初始化、登录、改密与本机重置使用相同规则。

启动器验收已通过：独立18083实例的首次启动、旧密码迁移、同目录重启；启动器异常与端口保护回归；18082浏览器实际登录、刷新恢复、退出与旧会话拒绝。修改后的 Go 全量测试和 vet 通过。此次启动器和固定密码的新增修改尚未提交、未推送。

该实例运行最终dev二进制并直接服务本工作树 `vue/dist`，不依赖Vite。额外的开发预览为 `http://127.0.0.1:3003/rainbow-bridge/`，代理到18082。

重启已有实例可在本工作树执行下列命令。已有账号无需再次提供首次密码；运行目录必须保持为隔离目录，避免写入其他实例的资源。

```powershell
$repo = 'C:/Users/MalfuRisa/.codex/worktrees/admin-login-integration/rainbow_bridge'
$env:FRONTEND_DIR = Join-Path $repo 'vue/dist'
Push-Location (Join-Path $repo '.dev-integration')
try { & './rainbow-admin-demo.exe' --config './config.yaml' }
finally { Pop-Location }
```

重跑完整回归时，用工作树内独立临时目录规避本机系统TEMP的目录移动问题：

```powershell
$env:TEMP = Join-Path (Get-Location) '.dev-integration/test-temp'
$env:TMP = $env:TEMP
New-Item -ItemType Directory -Path $env:TEMP -Force | Out-Null
./script/test-local.ps1 -Suite Full -AttachmentRoot './.dev-integration/evidence'
```

这次只在本地集成分支保存结果，没有推送Fork、向上游提PR或把18080切换到新代码。原三个工作区的未提交成果保留。
