# Windows 一键启动虹桥

双击仓库根目录的 `start-rainbow.cmd`。脚本构建整合后的前后端，启动后打开浏览器。

- 登录账号固定为 **admin**，密码固定为 **admin**。登录一次后即可编辑配置、锁版、发布和切换版本，不需要再输入锁版密钥。
- 默认地址为 `http://127.0.0.1:18080/rainbow-bridge/`，只监听本机。
- 数据保存在 `%LOCALAPPDATA%\RainbowBridge\local-demo`，原有配置、资源与版本继续保留。
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

启动器轻量回归：`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\script\test-start-local-output.ps1`。检查输出异常隔离、构建失败、错误日志降级、遗留服务识别及端口释放；真实启动使用 `-CheckOnly` 验证。
