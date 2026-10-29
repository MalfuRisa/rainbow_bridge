# 共享远程实例的有界诊断

对应 `docs/prd/remote-api-verification-and-request-id-v1.md`。只允许在已获授权的实例执行，不修改产品代码或部署。

- `node tests/e2e/remote-api/public-probe.mjs`：从IDL与注册源码生成矩阵，执行公共读取、匿名拒绝和真实浏览器安全上下文检查。
- `node tests/e2e/remote-api/managed-probe.mjs`：使用 `RB_REMOTE_USER`、`RB_REMOTE_PASSWORD` 进程环境中的已确认网页凭据，通过真实页面登录。仅向本轮随机环境及返回的内部身份写入，finally解锁、删除环境并注销该会话。
- `RB_PUBLIC_RUN`、`RB_MANAGED_RUN` 指定已完成证据目录后，执行 `node tests/e2e/remote-api/summarize.mjs` 生成完整状态矩阵。

默认测试地址为本PRD授权的远程地址；`RB_REMOTE_URL` 可覆盖探测地址。汇总脚本静态首页核验仍绑定PRD的地址。使用前确认目标、清理合同及所有旧轮次的 `cleanup.json`；失败清理项未解决时不要新造数。不要把密码、Cookie或CSRF作为命令行参数、写入文件、开启trace或存储浏览器会话。

证据写入忽略目录 `.local-qa/remote-api/<run_id>/`。脚本作为诊断采集器会继续独立场景并登记 `issues.json`，进程退出0不代表产品无缺陷；以 `cases.json`、`issues.json`、`cleanup.json` 和 `summary-final.json` 为准。HTTP200与业务成功分开判断，503限制响应符合合同也不等于功能可用。

本脚本不上传真实资源、不创建全实例备份、不执行恢复、改密或发布部署。范围删除保留逻辑删除及审计历史；没有物理文件即时清理合同，因此资源上传成功场景不在本轮执行。若部署变更后发布创建意外成功，需人工核对成功版本、物理快照与清理合同，不能沿用“没有物理资源”的结论。

2026-10-29已知诊断结果：HTTP页面冻结后randomUUID缺失、MinIO发布能力受限、普通配置列表与同scope详情/带类型列表不一致。产品修复只记录到PRD，没有实施。
