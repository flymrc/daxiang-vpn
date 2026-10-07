# 2026-10-07 提交 main 与清理其他分支

用户明确要求“提交到main分支，把其他分支全部删除”。16:39 JST复核：已经将已部署版本、前八波Steelman代码与部署记录提交到GitHub main；远端和本地均只保留main分支。本记录是随后追加的文档提交，不改变已部署binary。

## 发布范围和验证

- [x] GitHub仓库为 `flymrc/daxiang-vpn`，默认分支main。先fetch全部远端heads，确认原main=`92379a30c393b7a5ea7a626b0f52f0356c03ac83`。
- [x] 已验收工作树原HEAD=`dfb0394a862eb3e59e4dbcf816b6795139060442`，比远端main领先25个提交、落后0，原本地main=e69645a也在其祖先链。采用快进，未改写main历史。
- [x] 发布源码包含已部署的 `3b5a2c576aa344f907031b2c35c31c69499424b2`。Windows v13/Linux v3门禁、十目标hash和兼容生产/Windows连通证据沿用[部署记录](2026-10-07-wave8-compat-deployment.md)，本轮没有源码行为改动或重新部署。
- [x] 对这25个待发布提交的新增内容检查私钥PEM、常见GitHub/AWS credential及明确密码文档字段，未发现匹配项；这是具体模式扫描，不宣称覆盖一切秘密。私有证据、凭据、构建产物、原工作区未提交文件及归档bundle未进入push。
- [x] 用一次atomic push快进main并删除两个远端旧分支；删除操作分别绑定事先读取的exact SHA lease，若远端发生竞争变化则整笔拒绝。首次push后main=dfb0394。

## 删除和可恢复性

| 删除的分支 | 删除前HEAD | 范围 |
| --- | --- | --- |
| `feat/android-remote-control` | `f1ae6664804c57abf721f2984caca205df3b49ec` | GitHub远端 |
| `fix/hub-idle-preempt` | `54c9eeb12492563182ae74f1d81b4e89ada20c2f` | GitHub远端 |
| `codex/client-version-enforcement` | `c947e6baa7ee39e217ba95ea762d13c5293824a2` | 本地 |
| `codex/hub-idle-preempt-deploy` | `c737cb05e40e1134fa48c5d283c6ff88524d640e` | 本地 |
| `codex/zhvpn-steelman-runtime` | `dfb0394a862eb3e59e4dbcf816b6795139060442` | 本地，当前实现工作树已切main |

版本强制分支有两个未合入Steelman/main的独有提交；没有把它们顺带合入已部署版本。删除前全部refs保存为受保护的本地Git bundle并通过`git bundle verify`；旧工作树的HEAD也保留为detached，可恢复这些提交。两个远端分支已经包含在发布历史内。

私有归档在workspace外部 `.local/zongheng-vpn/steelman/2026-10-07/main-publish-branch-cleanup/`，仅当前OS用户/SYSTEM/Administrators访问。包含refs-before、完整Git bundle、原工作区89个dirty/untracked文件快照、SHA清单及index。bundle SHA256=`e0057a46e01aaafd8a3ea0d86b8d25dfbf79acf1651f6e018b5334577c884130`；归档不提交仓库、不留额外备份分支。

## 工作树现状

- [x] 当前干净的Steelman实现工作树持有main；路径仍以zhvpn-steelman-runtime命名，路径名不代表另有同名Git分支。
- [x] 原产品工作区保持e69645a的detached HEAD，原89个dirty/untracked文件hash与index均未变化。Android客户App、RDP与其他原工作不被这次发布认领、覆盖或提交。
- [x] 另外两个旧工作树分别在c947e6b/c737cb0的detached HEAD；目录和ignored文件保留，没有删除checkout来完成分支清理。它们原有nested-repository core.worktree解析问题用显式git-dir/work-tree读取，未改共享配置。
- [x] `git for-each-ref refs/heads`仅main；`git ls-remote --heads origin`仅main；fetch/prune后remote-tracking仅origin/main及origin/HEAD。

本次只发布Git和清理分支，生产binary仍来自3b5a2c5，兼容canary、未签名dev、v2未启用及Steelman安全迁移NO-GO边界保持。当前记录追加提交也推送main后，以实时远端HEAD为最终文档提交，不把自己的SHA写入自身内容。
