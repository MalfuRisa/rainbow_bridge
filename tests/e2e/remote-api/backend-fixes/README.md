# 后端独占远程组件验证

本目录不部署现有应用。`remote/main.go test`通过已核实SSH主机密钥，在远程创建唯一工作根、独占Redis进程/转发，上传候选dev后端和service测试二进制。Go测试新建独立PG schema、MinIO桶和快照目录，覆盖实际组件及候选HTTP，并在退出时清理。不能给它业务schema或共享桶作为测试目标。

运行前交叉编译Linux ARM64二进制：

```powershell
$env:GOOS='linux'; $env:GOARCH='arm64'; $env:CGO_ENABLED='0'
go test -c ./biz/service -o .local-qa/backend-fixes/service.test
go build -tags=dev -o .local-qa/backend-fixes/hertz_service .
Remove-Item Env:GOOS,Env:GOARCH,Env:CGO_ENABLED
```

在进程环境安全提供`RB_SSH_PASSWORD`及`RB_SSH_KNOWN_HOSTS`，不要将密码写入文件或提交。另设置`RB_REMOTE_TEST_BINARY`、`RB_REMOTE_CANDIDATE_BINARY`、`RB_REMOTE_MAINTENANCE_BINARY`为本地文件路径（第三项用同平台构建cmd/release-storage），`RB_REMOTE_TEST_LOG`为本地忽略日志路径。随后运行：

```powershell
go run ./tests/e2e/remote-api/backend-fixes/remote test
go run ./tests/e2e/remote-api/backend-fixes/remote inspect
```

本轮MinIO快照验收扩展为独占原始桶＋独占快照桶、十资源GET摘要校验、真实local active/inactive/deleted历史经维护CLI迁移及不同空目录候选进程跨读。`remote inventory`仅上传维护工具执行原实例只读plan并只读检查既知Pod目录，不迁移业务数据；需设置维护工具与日志路径。snapshot-minio子目录保留前后基线与清理证据。构建与测试临时文件在唯一远程工作根内，finally一起清理。

结束后删除本进程凭据环境变量，核对日志及`.cleanup.json`、`.transport.json`，不能只看工具外层退出码。若清理失败，只处理清单记录的唯一资源，禁止全库/全桶/共享目录清理。`inspect`只读取脱敏拓扑、额外Redis pid、rb-backend工作根及候选进程；不启动测试。

`baseline.mjs before|after`从原服务读取scope身份、锁定revision和active，通过环境变量`RB_REMOTE_USER`/`RB_REMOTE_PASSWORD`创建独立会话并登出。需使用已有Playwright依赖，前后JSON只能证明所列字段相同，不证明业务正文的完整一致性。

详细验收证据与部署限制见docs/prd/remote-release-cache-backend-v1.md §7。
