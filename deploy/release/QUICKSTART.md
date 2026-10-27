# v5.0.0 安装与启动

解压对应平台的应用包，在该目录启动；首次设置管理员初始化密码。

Windows PowerShell：

```powershell
$env:RAINBOW_ADMIN_USERNAME='admin'
$env:RAINBOW_ADMIN_INITIAL_PASSWORD='<自行设置初始化密码>'
./app.exe --config config.yaml
```

Linux / macOS：

```sh
export RAINBOW_ADMIN_USERNAME=admin
export RAINBOW_ADMIN_INITIAL_PASSWORD='<自行设置初始化密码>'
./app --config config.yaml
```

打开 http://127.0.0.1:8080/rainbow-bridge/ 登录。这里的密码占位符必须替换，不附带默认管理员密码。已有数据库账号不会因修改环境变量而改密。

config.yaml 默认 SQLite、本地资源、关闭 Redis，仅监听本机；config.reference.yaml 保留原仓库 PostgreSQL/MinIO 等示例，不代表其服务已经部署。

app 已嵌入前端，不需要另启 Node。web/ 和独立 frontend 包可供拆分部署，需部署在 /rainbow-bridge/ 并正确反向代理 API；独立前端包本身不包含后端。

通过管理员配置数据后，先锁版、创建版本并确认生效。业务读取示例：

```http
GET /rainbow-bridge/api/v1/runtime/release
x-environment: default
x-pipeline: default
```

运行时响应中的图片 URL 可直接请求。不登录管理账号也可读公开运行时接口；范围头是选数据，不是访问密钥。完整合同见 api/。

密码重置：为 admin-password-reset 工具进程设置 RAINBOW_ADMIN_RESET_PASSWORD，再运行 ./admin-password-reset --config config.yaml（Windows 为 .exe）。不要在命令行参数里填写密码。

业务备份按需在 config.yaml 开启 backup.enabled，并设置同盘、业务根目录以外的绝对 control_dir；只支持说明中限定的单实例部署。离线 backup-restore 工具须在服务停止后按 --help 使用，不要把迁移包、业务静态包与整实例备份混用。

生产部署：调整监听地址、HTTPS、安全 Cookie 和 trusted_origins，使用自己的密码与存储配置；不能直接把本机 HTTP Cookie 示例用于公网。修改 base_path 时前端需匹配重新构建。

升级应先保存数据库、uploads 与 release-snapshots 的一致副本。本包不含你的业务数据，不覆盖已有账号；降级前先恢复对应旧版备份。
