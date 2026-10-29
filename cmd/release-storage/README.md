# 发布快照运维工具

使用与当前实例一致的私有配置和旧RAINBOW_RELEASE_DIR，在仓库根构建：`go build -o release-storage ./cmd/release-storage`。配置不放命令参数，不提交凭据。工具不启动业务服务、不建桶、不AutoMigrate、不改账号或配置正文；SQLite要求数据库已存在。执行前需已用支持本协议的服务完成模型升级。新旧写者必须共同遵守config_write_gate(id=1)，绕过应用写协调的外部写者不得并行运行。

默认只读，盘点全部版本（包括删除版本/已删scope历史）、完整性、attempt、正式引用、精确前缀的对象版本和未完成multipart数量：

```sh
./release-storage --config /private/config.yaml
./release-storage --config /private/config.yaml --action inspect
./release-storage --config /private/config.yaml --action verify
```

`plan/inspect`输出每版本verified/verification_failed，不隐瞒坏历史；`verify`遇到坏版本返回非零。Instance字段是业务身份确认token，不是凭据。plan不会迁移或清理。LocalDirectories另列未提交目录、staging条目及异常文件/链接，只读、不遍历链接、不当作版本、不自动删除；已迁版本的旧目录仍属于保留源。root_unavailable需确认旧卷是否正确挂载，不能据此推断没有残留。

配置release_storage为minio且独立快照bucket已经建好后，明确选中snapshot UUID：

```sh
./release-storage --config /private/config.yaml --action migrate \
  --execute --confirm-instance '<plan的Instance>' --snapshots '<uuid1>,<uuid2>'
```

不带execute仍仅盘点。逐版本先校验完整local历史，复制十份就必须十份全部GET验证，再CAS切内部定位；版本/编号/active/revision/删除历史不变。部分失败返回非零，其他已完成结果保留。失败版本继续local，修正源文件可读性/存储故障后重新执行。重跑已迁版本只校验，不重建版本或覆盖对象。不能从当前原始bucket重建缺失历史。local文件始终保留。回退仅限迁移版本且旧文件完整：同样显式确认，action改为rollback；先审核部署文档的旧二进制限制。

cleanup_pending/unknown的恢复：先inspect取得准确attempt ID、定位、引用数和残留对象/multipart；在发布维护窗口明确暂停外部直写者，执行：

```sh
./release-storage --config /private/config.yaml --action reconcile \
  --execute --confirm-instance '<plan的Instance>' --attempt '<attempt_uuid>'
```

代码guard而非年龄规则：先取得与创建相同的DB写协调锁，核对所有正式历史定位，CAS持久认领cleanup_pending，排队创建随后重读状态必拒绝；再在同一协调锁保护下复核并逐对象版本删除、abort multipart、重新列举为空，最后写cleaned。崩溃在两阶段之间可重跑；删除失败持久保留cleanup_pending，不改成成功。任何已提交引用或committed状态（包括删除历史、迁移回退后保留的旧远程副本）均拒绝删除。无法取得门控/确认DB则保留，**没有force绕过，也不按TTL清对象**。生产应使用本协议服务，不能配合直接写库/写对象的外部脚本猜测安全。

上传采用有界单PUT避免新增multipart，恢复仍精确处理已有未完成上传。退出后复查状态和对象/multipart数量。unknown不等于可删，DB正式版本定位优先于attempt结果标记。操作输出用于受控运维，不作为公开API返回。
