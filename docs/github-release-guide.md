# GitHub 发布（`gh`）操作手册

## 发布目标

将本次“Grafana 落地能力增强”同步为可追溯的 GitHub Release 文档，便于团队引用版本说明、变更项和安装入口。

## 命令示例（按顺序）

1. 拉取当前仓库并确认分支与状态

```bash
git status --short -b
```

2. 生成变更说明（可先在本地 `docs` 目录补齐）

```bash
cat docs/ecosystem-integration-guide.md
```

3. 更新 `CHANGELOG.md` 后，准备发布内容

```bash
vim CHANGELOG.md
```

> 提示：发布前确认版本号和 tag 在历史记录中尚未占用，避免误改历史版本。

4. 使用 `gh` 创建/更新 Release

```bash
gh release create v0.2.3 \
  --title "v0.2.3 - Grafana Compatibility, Local Agent API, and Migration Rehearsal" \
  --notes-file docs/github-release-notes.md \
  --verify-tag
```

5. 给 Release 附件（可选）

```bash
gh release upload v0.2.3 \
  docs/ecosystem-integration-guide.md \
  docs/integrations.md
```

如需替换已有版本，可加 `--clobber` 覆盖同名文件附件。

## 推荐一条龙命令（含发布校验）

```bash
git status --short
gh release view v0.2.3 >/dev/null && echo "release exists"
git tag v0.2.3
gh release create v0.2.3 \
  --title "v0.2.3 - Grafana Compatibility, Local Agent API, and Migration Rehearsal" \
  --notes-file docs/github-release-notes.md \
  --verify-tag \
  docs/ecosystem-integration-guide.md docs/integrations.md
gh release view v0.2.3 --json tagName,name,createdAt,author,url
```

## 回滚建议

如果本次发布存在问题，可在不改动代码历史的前提下快速回滚“发布层”：

```bash
gh release delete v0.2.3 --yes
git tag -d v0.2.3
git push origin :refs/tags/v0.2.3
```

> 回滚动作仅影响 release 元数据；如需回滚代码，请重新发版新 tag 或还原对应代码提交并再发布新版本。

## 与 CI 的关系

如需自动触发正式构建，建议通过推送 tag 来触发仓库的 `Release` 工作流，再在 Release 中补充文档链接。

```bash
git tag v0.2.3
git push origin v0.2.3
```

CI 将在 `refs/tags/v*` 上执行 release 流程并产出制品，文档发布与二进制发布可分离进行。
