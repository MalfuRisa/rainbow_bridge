# 单实例发布快照持久卷：待审核部署方案

> 历史local方案：2026-10-29 PRD §9已将新目标改为独立MinIO快照bucket，见minio-snapshots.example.yaml和minio-snapshots.md。本目录PVC模板只用于仍需读取local历史的阶段；未迁完历史前仍需保留旧卷，不是新MinIO版本的永久依赖。

本目录仅为模板，本轮没有 apply、替换镜像或重启现有服务。现场观测的应用为 `rainbow-bridge/rainbow-bridge-app`、容器 `app`、单副本，没有业务数据卷。现有 `pgsql-minio` 示例的 deployment/container 名称不同，不能直接把它当现场补丁。

PG 保存配置/版本元数据，MinIO 保存可变原始资源，本卷保存已校验的不可变发布字节。`RAINBOW_RELEASE_DIR` 指向卷根；staging、final 必须留在同一文件系统中。此方案限一个应用进程、一个 Pod；`ReadWriteOnce` 本身不等于单进程写保护，因此使用单副本和 `Recreate`，不启用自动扩容或滚动双实例。

部署前需核实：

1. 保存当前 deployment、镜像 digest、配置引用及 Secret 引用的安全备份；不把明文配置和 Secret 提交入库。记录应用是否已有成功快照。若已有，不可直接切到空卷：需要停写、核对每份已提交 snapshot ID、清单和摘要，迁移完整文件，再切换路径；不可清库或只拷 active。
2. 核实实际 StorageClass、挂载节点/可用区、回收策略与恢复方法，替换 PVC 模板中的占位符。20Gi 仅为示例；容量应按保留版本资源实际大小、一次 staging（单次资源总量上限100MiB）、失败残留调查及增长余量估算。不要把逻辑删除版本等同物理空间立即回收。
3. 确认运行 UID/GID、容器安全策略与卷权限。使用受支持的卷 fsGroup 或仅针对**新快照卷**的初始化授权，使应用可创建/读取/rename；不要对原数据目录执行递归 chmod/chown，也不要假定镜像一定以 root 运行。模板不编造现场 UID 或 StorageClass。
4. 新镜像必须包含本批后端补丁，同时保留远程 `c49194a` 的 ARM64 打包及 HTTP 会话显式 opt-in 修正。本地补丁未合入这两项，不能直接用本地旧构建覆盖现场。固定镜像 digest，保留旧 digest 回退。
5. 最终批准后才依次创建独立 PVC、设置新镜像及应用本 patch。首次切换有单实例停机窗口。检查 Pod 的实际 `volumeMounts`、`RAINBOW_RELEASE_DIR`、文件系统设备和可写性，不能仅以启动200作为持久化验收。

操作审核时可参考（本轮未执行）：

```sh
kubectl -n rainbow-bridge apply -f pvc.reviewed.yaml
kubectl -n rainbow-bridge patch deployment rainbow-bridge-app --type=strategic --patch-file deployment.reviewed.yaml
# 新镜像digest由正式发布单设置；两个变更应在同一次审批窗口内完成。
kubectl -n rainbow-bridge rollout status deployment/rainbow-bridge-app
```

验收应在批准的独占验收范围完成：含图片/文本的候选创建→初始不生效→审阅→生效→匿名runtime/资源/固定ZIP字节与摘要→修改原始MinIO对象后固定历史不变。之后在明确停机窗口重建 Pod，复核**同一卷**上的旧 snapshot、active、资源和 ZIP。独立测试进程的重启只能证明该目录复用，不能替代现场 Pod 重建验收。

回退：先停止进一步创建/切换发布，记录新版本和 active 状态；按原 deployment/image digest 回退，但保留 PVC 和完整快照目录，禁止删除 PVC 或恢复旧业务数据库来回退应用。旧版本应先确认能读取当前快照格式；若回退前的配置未挂卷，必须继续挂本卷并保留路径，否则 PG 中的版本会指向不可读文件。回退后的旧二进制对 MinIO新创建仍可能返回503，不能承诺具备新能力。故障时导出诊断清单，保留可能已经提交的final，不自动删除。

PG+MinIO+Redis的整机备份仍为 `BACKUP_UNSUPPORTED`。该卷不代表已有备份、多Pod共享存储或灾备方案。
