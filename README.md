# 虹桥计划 · Rainbow Bridge

**自部署的配置与资源管理平台：编辑有工作区，发布有快照，业务读取有明确版本。**

虹桥面向需要统一管理文案、主题色、JSON、图片和文件的小团队。管理员在工作台维护配置、查看差异并决定何时生效；网站、客户端或后端服务通过 API 读取当前生效版本，也可以固定读取指定版本。

[下载 v5.0.0](https://github.com/yi-nology/rainbow_bridge/releases/tag/v5.0.0) · [启动说明](deploy/release/QUICKSTART.md) · [完整 API 文档](docs/docs/api/index.md) · [本次更新](docs/releases/v5.0.0.md)

![光谱工作台：首页与当前生效配置](docs/images/v5.0.0/home.png)

> 截图来自新版光谱 UI 的浏览器验收，使用演示夹具数据。环境、版本、资源和时间仅作界面示例，截图中的 `dev` 是验收构建标识。

## 能做什么

| 能力 | 使用方式 |
| --- | --- |
| 环境与渠道隔离 | 同一平台管理开发、测试、生产或不同业务渠道，每个组合有独立配置和版本 |
| 11 种配置类型 | 文本、多行文本、富文本、整数、小数、布尔值、键值对、JSON 对象、色彩、图片、文件 |
| 资源管理 | 上传、预览和下载图片及文件；受管资源随发布快照固定下来 |
| 编辑冲突保护 | 两人编辑同一条配置时，过期保存会被拦截，避免后保存的人静默覆盖前一个人 |
| 锁版与发布 | 锁定编辑范围后创建不可修改的快照，版本号按环境/渠道分别自动递增为 `v1`、`v2`、`v3` |
| 差异审阅与切换 | 查看新增、修改、删除和未变项；支持任意两个版本比较，以及切换、切回历史版本 |
| 业务 API | 默认读取当前生效版本，也可指定版本；返回的图片和文件地址可直接请求 |
| 导入、导出与迁移 | 可选择编辑区、当前生效版本或指定版本导出；导入内容进入目标编辑区 |
| 业务备份恢复 | 在支持的单实例部署中保存配置、原始资源、发布快照和历史，校验后通过重启恢复 |
| 管理员登录 | 使用账号和 Cookie 会话管理工作台，管理写请求校验 CSRF，不再逐次填写锁版密钥 |

**编辑区与生效版本分开。** 保存配置不会直接影响业务；创建版本也不会自动切换。只有明确确认生效后，业务默认读取才会变化。非生效版本可逻辑删除，生效版本需要先切换再删除；删除后的版本号不会重新分配。

## 界面预览

<details>
<summary>配置管理：选择工作范围、编辑配置与预览内容</summary>

![配置管理](docs/images/v5.0.0/config.png)

</details>

<details>
<summary>环境与渠道：按范围组织业务配置</summary>

![环境与渠道管理](docs/images/v5.0.0/environments.png)

</details>

<details>
<summary>资源管理：预览和下载范围内的图片与文件</summary>

![资源管理](docs/images/v5.0.0/resources.png)

</details>

<details>
<summary>版本发布：生效状态、候选版本与发布入口</summary>

![版本发布](docs/images/v5.0.0/releases.png)

</details>

<details>
<summary>任意版本比较：从基准版本到目标版本查看变化</summary>

![任意版本比较](docs/images/v5.0.0/compare.png)

交换基准和目标后，新增与删除的方向也随之交换。比较历史版本不会改变当前生效指针。

</details>

<details>
<summary>备份与恢复：业务备份列表、上传校验和恢复预览</summary>

![业务备份与恢复](docs/images/v5.0.0/backups.png)

</details>

## 快速开始

### 下载应用包

前往 [v5.0.0 Release](https://github.com/yi-nology/rainbow_bridge/releases/tag/v5.0.0)，下载对应平台的文件：

| 平台 | 文件 |
| --- | --- |
| Windows x64 | `rainbow_bridge-app-windows-amd64.zip` |
| Linux x64 / ARM64 | `rainbow_bridge-app-linux-amd64.tar.gz` / `rainbow_bridge-app-linux-arm64.tar.gz` |
| macOS Intel / Apple Silicon | `rainbow_bridge-app-darwin-amd64.tar.gz` / `rainbow_bridge-app-darwin-arm64.tar.gz` |

应用包已嵌入前端，无需安装 Node.js 或另启前端服务。另有 `frontend.zip` / `frontend.tar.gz` 供前后端分开部署；独立前端包不含后端。下载后可用 Release 的 `SHA256SUMS.txt` 校验文件。

在解压目录启动，首次通过环境变量设置管理员密码。

Windows PowerShell：

```powershell
$env:RAINBOW_ADMIN_USERNAME = 'admin'
$env:RAINBOW_ADMIN_INITIAL_PASSWORD = '<替换为你自己的初始化密码>'
.\app.exe --config config.yaml
```

Linux / macOS：

```sh
export RAINBOW_ADMIN_USERNAME=admin
export RAINBOW_ADMIN_INITIAL_PASSWORD='<替换为你自己的初始化密码>'
./app --config config.yaml
```

打开 **http://127.0.0.1:8080/rainbow-bridge/**，使用上述账号和密码登录。已有账号不会被初始化环境变量覆盖；重置密码使用包内 `admin-password-reset` 工具，详见 [启动说明](deploy/release/QUICKSTART.md)。

发布包附带的 `config.yaml` 使用 SQLite、本地资源、关闭 Redis，仅监听本机，业务备份默认关闭。`config.reference.yaml` 是其他部署方式的参考配置。对外部署时需配置监听地址、HTTPS、安全 Cookie 与精确的 `trusted_origins`。

### Windows 源码演示

已安装 Go 和 Node.js 时，可在仓库根目录双击 `start-rainbow.cmd`。该脚本构建当前源码，默认打开 `http://127.0.0.1:18080/rainbow-bridge/`，本机演示账号和密码均为 **admin**，数据保存在 `%LOCALAPPDATA%\RainbowBridge\local-demo`。

此固定密码仅用于本机演示；正式发布包由使用者设置初始化密码。脚本参数与数据位置见 [Windows 一键启动说明](script/start-local.README.md)。

## 从编辑到业务使用

1. **选择范围**：创建或选择环境、渠道。新库初始范围为 `default / default`；删除后重启不会重新生成。
2. **编辑配置**：新增配置，必要时上传同一范围内的图片和文件。此时内容只在编辑区。
3. **发布配置**：在版本发布页按引导检查、锁版、创建候选版本、审阅与当前生效版本的差异，再确认生效。
4. **业务读取**：应用调用运行时 API。默认跟随生效切换；希望保持内容的调用方可以指定版本。
5. **继续修改**：解锁后编辑下一轮内容，已发布快照不变；需要回退时选择历史版本，审阅后切换。

候选版本创建后取消发布，会保留为未生效版本。发布时若其他管理员先切换了版本，系统会拦截旧确认，需要重新比较并再次确认。锁版、解锁、创建和切换的备注均可选填。

## 业务 API 接入

例如业务 A、业务 B 各有自己的环境/渠道：调用方通过请求头选定范围，获取配置及图片地址，再使用返回的地址展示图片。**范围头是选数据的参数，不是访问凭据。** 运行时读取和资源下载公开，敏感配置不应放进公开分发内容。

读取当前生效版本（以下为 Bash 示例，Windows 使用 `curl.exe` 并调整换行）：

```sh
curl --fail-with-body 'http://127.0.0.1:8080/rainbow-bridge/api/v1/runtime/release' \
  -H 'x-environment: default' \
  -H 'x-pipeline: default'
```

固定读取 `v2`：

```sh
curl --fail-with-body 'http://127.0.0.1:8080/rainbow-bridge/api/v1/runtime/release?version=v2' \
  -H 'x-environment: default' \
  -H 'x-pipeline: default'
```

| 接口 | 用途 |
| --- | --- |
| `GET /api/v1/runtime/overview` | 获取可用的环境与渠道 |
| `GET /api/v1/runtime/release` | 读取当前生效快照 |
| `GET /api/v1/runtime/release?version=v2` | 固定读取指定已创建版本，可读取尚未生效的候选版本 |
| `GET /api/v1/runtime/release?content_format=raw` | 将配置内容作为字符串返回，便于保留 JSON 大整数原文 |
| `GET /api/v1/runtime/release/static?version=v2&expected_snapshot_id=<UUID>` | 导出已确认快照的静态 ZIP，需携带范围请求头 |

以上路径还需加部署前缀，例如 `/rainbow-bridge`。没有生效版本时默认读取返回 404，不会回退到编辑区；版本被逻辑删除后，固定读取返回 410。切换生效不需要重启，调用方下次请求可读到新版本；已有客户端缓存仍需由业务自行刷新。

旧 `runtime/config`、`runtime/static` 也统一读取当前生效版本，参数与返回结构保留兼容约定，不与新版接口混用。管理 API 要求登录会话，写操作还需 CSRF；字段、错误码、范围身份校验与无损 JSON 使用方式请看 [完整 API 文档](docs/docs/api/index.md) 和 [运行时接入说明](docs/docs/api/runtime.md)。

## 导出、迁移和备份有什么区别

| 文件/操作 | 解决什么问题 | 导入或使用后的效果 |
| --- | --- | --- |
| 迁移归档 | 在范围或实例间搬运配置和资源；来源可选编辑区、生效版本、指定版本 | 导入目标编辑区，不会自动锁版、创建发布版本或切换生效 |
| 业务静态包 | 把某个快照的配置和资源交给业务离线部署 | 包含 `config.json`、`manifest.json` 和 `assets/`，用于静态分发 |
| 整实例业务备份 | 保存配置、资源、锁版状态、发布版本和历史，以便恢复 | 校验并确认后准备恢复计划，重启服务完成替换；保留当前管理员账号，旧会话失效 |

业务备份目前只支持 **SQLite + 本地原始资源 + 本地发布快照 + Redis 关闭的单实例部署**，库存中也不能有 MinIO 快照定位（包括已删除历史）；需开启 `backup.enabled` 并配置业务根之外、同卷的绝对 `control_dir`。成功恢复后的旧副本由系统自动保留与轮换，默认保留最近 3 份；未完成或需要回退的异常恢复仍需处理。此功能不是热恢复，也不适用于 K8s 多 Pod。

详见 [导出来源与迁移归档](docs/docs/api/transfer-export.md)、[业务备份与恢复](docs/docs/api/backup.md)。

## 技术栈与源码开发

| 层次 | 技术 |
| --- | --- |
| 后端 | Go 1.25+、CloudWeGo Hertz、GORM、Protobuf / hz |
| 前端 | Vue 3、TypeScript、Vite、Pinia、Tailwind CSS、Radix Vue；使用 Node.js 22 构建 |
| 数据库 | SQLite，另有 MySQL / PostgreSQL 适配 |
| 文件与缓存 | 本地资源，另有 MinIO 适配；Redis 可选 |
| 测试 | Go 单测与 HTTP 合同测试、Playwright 页面/业务 API 验证、k6 脚本 |

```text
main.go / initialize.go   启动、配置与依赖装配
biz/handler/             HTTP 接口
biz/service/             配置、锁版、发布、迁移与备份逻辑
biz/dal/                 数据模型、DAO 与迁移
biz/middleware/          会话鉴权、CSRF、CORS 等
idl/biz/                接口定义，生成模型与路由的来源
vue/                    光谱工作台前端
pkg/                    配置、数据库、存储与静态资源等公共模块
docs/docs/api/          面向接入研发的 API 文档
deploy/                 发布配置及容器/Kubernetes 示例
script/ / tests/        启动、构建与测试工具
```

构建带嵌入前端的应用（Bash）：

```sh
git clone https://github.com/yi-nology/rainbow_bridge.git
cd rainbow_bridge
cd vue
npm ci
BASE_PATH=/rainbow-bridge/ npm run build
cd ..
mkdir -p pkg/static/web output/bin
cp -R vue/dist/. pkg/static/web/
go build -mod=readonly -o output/bin/app .

# 首次启动设置账号、密码，使用本机示例配置
export RAINBOW_ADMIN_USERNAME=admin
export RAINBOW_ADMIN_INITIAL_PASSWORD='<替换为你自己的初始化密码>'
./output/bin/app --config deploy/release/config.yaml
```

前端开发入口为 `cd vue && npm run dev`；后端开发构建可加 `-tags=dev`，通过 `FRONTEND_DIR` 指向实际前端产物。前后端的 BasePath 必须匹配。仓库根目录 `config.yaml` 包含外部服务配置，不能直接当作零依赖本机启动配置。

生成模型与路由来自 `idl/biz/*.proto`，应使用 hz 重新生成，不直接修改生成文件。

## 验证与部署边界

```sh
# 后端验证
go test -mod=readonly -tags=dev ./...
go vet -mod=readonly -tags=dev ./...

# 前端类型检查与构建
cd vue
npm run build
```

Windows 隔离业务读取验证：`powershell -NoProfile -ExecutionPolicy Bypass -File script/test-runtime-business.ps1`。该脚本构建并启动临时实例，验证默认/指定版本读取、资源字节、切版、重启、浏览器与静态包，结束后清理测试进程；详见 [运行时验证说明](tests/e2e/runtime-business/README.md)。其他场景见 [测试目录说明](tests/README.md) 与 `tests/e2e/`。

v5.0.0 的五个平台应用包及独立前端包已由 Release 工作流构建。容器与 Kubernetes 配置位于 [deploy/](deploy/README.md)，部署前需按当前鉴权、数据库与存储配置调整。当前源码支持从本地或 MinIO 原始资源创建发布，并选择 local 或 MinIO 保存不可变快照；这些修改已完成隔离验证，现有运行部署尚未更新，不代表旧版安装包已具备该能力。**不把示例配置或跨进程验证视为多 Pod 验收结论**：多实例仍需验证数据库并发与恢复协调，整实例备份保持上述支持限制。

快照存储缺省为 local。使用 MinIO 快照时，在已有 `storage.minio` 连接配置基础上显式添加：

```yaml
release_storage:
  type: minio
  bucket: rainbow-bridge-snapshots  # 运维预建的私有桶，必须与原始资源桶不同
```

原始资源区与快照区分桶；每份版本保存自身存储定位，切换全局配置不会迁移旧历史。已有 local 快照通过 [release-storage 工具](cmd/release-storage/README.md) 先只读盘点、明确选择后迁移，源文件保留；有 local 历史时继续保留旧卷。完整配置、权限和回退限制见 [MinIO 快照部署说明](deploy/kubernetes/release-snapshots/minio-snapshots.md) 与 [配置片段](deploy/kubernetes/release-snapshots/minio-snapshots.example.yaml)。生产部署、业务历史切换与旧卷移除尚未执行。

升级前先保存数据库、原始资源和发布快照的一致副本；数据库迁移后不要直接降级二进制。兼容变更与安装包内容见 [v5.0.0 发布说明](docs/releases/v5.0.0.md)。
