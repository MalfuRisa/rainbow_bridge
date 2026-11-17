# Windows 一键启动虹桥

双击仓库根目录的 `start-rainbow.cmd`。脚本构建整合后的前后端，启动后打开浏览器。

- 登录账号固定为 **admin**，密码固定为 **admin**。登录一次后即可编辑配置、锁版、发布和切换版本，不需要再输入锁版密钥。
- 默认地址为 `http://127.0.0.1:18080/rainbow-bridge/`，只监听本机。
- 数据保存在 `%LOCALAPPDATA%\RainbowBridge\local-demo`，原有配置、资源与版本继续保留。
- 发布快照固定使用该实例的 `data\release-snapshots`。启动前检查 Windows 能否将暂存目录重命名为正式快照目录。检查失败时，可用 `-DataDir` 指定 D 盘等可写目录。要迁移已有演示库，先停止原实例，再将完整 `data` 目录复制到新位置，保留数据库、资源和历史快照。
- 业务备份默认启用。登录后进入“备份与恢复”即可创建备份；控制目录为上述数据目录内的 `backup-control`，与业务数据目录 `data` 分开。使用 `-DataDir` 时自动使用该实例自己的控制目录。
- 旧演示账号如果使用随机密码，首次启动会将其改成 admin，同时撤销旧会话；以后重启密码不变。正常重启不会重置已正确的密码或撤销其他会话。
- 前后端已统一到当前仓库的 `main_v1` 分支，启动器直接构建当前目录，不再查找其他工作树。窗口显示实际代码路径；也可以用 `-RepoDir` 明确指定另一份完整代码。
- 启动会真实检查页面、登录、会话、管理读取及退出；检查失败不会继续运行旧程序。
- 使用期间保留启动窗口，按 Ctrl+C 停止服务。直接关窗口留下的同目录旧服务会在下次启动时被识别并替换；其他程序占用端口时只报错。
- 配置和程序在上述数据目录生成，不改写仓库配置。日志为 `server.stdout.log`、`server.stderr.log`，启动错误追加到 `launcher-error.log`。

首次启动需要 Go（版本见 go.mod）和 Node.js。缺少前端依赖时执行 npm ci。这里使用 SQLite、本地文件存储和登录会话，关闭 Redis 及外网版本查询。

```powershell
# 默认启动
.\start-rainbow.cmd

# 只做构建和启动检查，成功后自动停止
.\start-rainbow.cmd -CheckOnly -NoBrowser

# 独立端口、独立数据，不影响已有实例
.\start-rainbow.cmd -Port 18083 -DataDir "D:\rainbow-demo-two"

# 显式选择整合后的代码
.\start-rainbow.cmd -RepoDir "C:\path\to\integrated-repo"
```

同一数据目录只能有一个启动器。`-NoReplace` 可禁止替换遗留服务。`-NoBrowser` 保持服务运行但不打开浏览器。

与 EWork 本地联调时，保持虹桥启动窗口开启，再运行 EWork 仓库的 `start-ework.cmd`。如果虹桥通过 `-Port` 使用其他端口，在 EWork 脚本同时指定 `-RainbowPort`。

## 查看最新备份页面

启动器每次重新构建所选目录中的前后端，包括尚未提交的改动；不需要先发布镜像，也不需要安装 PostgreSQL、MinIO 或 Redis。双击 `start-rainbow.cmd`，登录后进入“备份与恢复”，即可查看当前代码的 SQLite 网页备份功能。

想保留已有演示数据、另开一套用于操作验证时，使用独立目录和端口：

```powershell
.\start-rainbow.cmd -Port 18083 -DataDir "$env:LOCALAPPDATA\RainbowBridge\backup-preview"
```

浏览器访问 `http://127.0.0.1:18083/rainbow-bridge/`，账号密码为 `admin / admin`。该目录中的操作会真实修改本地演示数据；恢复测试请只在这套独立实例执行，不要使用需要保留的数据。

这个启动器固定使用 SQLite、本地资源和关闭 Redis 的模式，因此不会显示分布式环境的“运维维护备份”分支，也不能验证多 Pod 停服、PostgreSQL 与 MinIO 的备份恢复。该分支的页面效果由 `tests/e2e/distributed-backup-frontend/` 专项使用契约 Mock 验证；真实运维流程参阅 [多 Pod 手动维护备份](../docs/operations/distributed-maintenance-backup.md)。不要为查看界面而把本地演示实例连接到正式数据库或伪造后端能力。

本启动器仅供本机开发与演示，固定密码和演示账号重置行为不适合生产部署。对外提供源码时也应保留这一使用边界。

启动器轻量回归：`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\script\test-start-local-output.ps1`。检查输出异常隔离、构建失败、错误日志降级、遗留服务识别及端口释放；真实启动使用 `-CheckOnly` 验证。
