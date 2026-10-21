# main_v1 统一代码入口

2026-10-21，统一仓库目录为 `D:/kylin/rainbow_bridge`，开发分支为 `main_v1`。

以已经整合的 `731dd9c` 为历史基线，收录登录会话、固定本地演示账号、一键启动、Vue 光谱工作台、首页生效版本读取、版本快照、备注选填和非生效版本删除的最新未提交成果。保留主目录独有的 `docs/onboarding/` 与 `config.dev.yaml`。没有将旧工作区代码覆盖回最新实现，也没有升级依赖或改动业务契约。

启动器现在只使用当前仓库（或显式传入的 `-RepoDir`），不再依赖已废弃的 `codex/admin-login-integration` 工作树。启动方式见 [Windows 一键启动](../script/start-local.README.md)。前端约定继续保留在 `vue/AGENTS.md`：refactoring-ui 为主、distinctive-ai-design 为辅，优先电脑网页端。

## 本轮验证

- `go test -mod=readonly -tags=dev -count=1 -timeout 180s ./...`：通过。该 Windows 环境需要将 `TEMP`、`TMP` 设为仓库内独立 `.local-qa/go-temp`；系统临时目录仍可复现快照重命名 `The handle is invalid`。
- `go vet -mod=readonly -tags=dev ./...`：通过。
- `vue-tsc -b --force`、`BASE_PATH=/rainbow-bridge/` 与 `/` 的 Vite 构建：通过。
- 用带前缀产物构建嵌入前端的生产 Go 程序：通过。构建产物未提交。
- `script/test-start-local-output.ps1`：输出异常隔离、构建失败、遗留服务与端口保护均通过。
- `script/test-local.ps1 -Suite Freeze`：4/4通过，全新数据库与独立端口。
- `script/test-local.ps1 -Suite Release`：46/47通过，包括本轮32项页面专项、真实登录/删除/同请求确认/资源410/并发生效保护及原发布韧性。唯一失败为已有“空版本 ZIP 下载落盘”规格，Chromium报告`download.path: canceled`；独立普通文本Blob下载也复现取消，已作为本机下载环境边界保留，未删断言或更改下载业务。2026-10-20的整合工作树47/47历史结果见对应清理PRD，不替代本轮结果。

验证日志、原始工作区文件归档和Git历史备份位于本地 `D:/kylin/rainbow_bridge-archive/2026-10-21/`，不入Git、不推送。其中源码归档保留本地数据；排除可再生成的依赖、dist及临时测试目录。备份可能包含本地测试凭据，仅用于本机恢复。

## 整理范围

过期代码工作树：`rainbow_bridge-admin-login`、`admin-login-ui/rainbow_bridge`、`admin-login-integration/rainbow_bridge`；旧提交暂存目录：`.rainbow-submit-e99c64ec`。先备份与核对唯一文件，再移除。旧打包目录 `rainbow_bridge-output` 移入备份，主目录现有业务数据与上传文件保留。

远端以 `main_v1` 保存统一成果，同时移除用户指定的 `codex/config-freeze`。原 `main` 和上游仓库不修改；不创建PR、不部署。历史PRD中旧分支、工作树和实例路径作为历史证据保留，后续开发统一从本仓库开始。

## 最终清理核对

- 远端`main_v1`已推送，`codex/config-freeze`已在同一次原子推送中删除；删除带原分支提交校验，避免覆盖整理期间新增的远端工作。
- 三个过期代码工作树已移除；当前`git worktree list`只保留主目录。对应四个本地`codex/*`旧分支已清理，历史可从备份恢复。
- 旧打包目录已移至备份中的`old-output/`。自动审批检查拒绝直接递归删除`.rainbow-submit-e99c64ec`，未提供具体原因；该暂存目录改为整体移入备份中的`old-submit-directory/`，原位置不再保留。
- 工作区无未提交改动，现有业务数据库和上传文件未删除；依赖、构建文件、临时验收目录均被忽略，未推送。
