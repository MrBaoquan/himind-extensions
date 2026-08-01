---
name: git-svn-commit-summary
description: Generate accurate, concise commit summaries for Git or SVN changes and optionally carry out a confirmed commit. Use when asked to summarize current changes, draft or improve a commit message, prepare a Git/SVN commit, review staged or working-copy changes before committing, or explain what a revision contains. Supports Codex, GitHub Copilot, and WorkBuddy project workflows.
---

# Git/SVN 提交摘要

根据版本库中的真实差异生成可审阅的提交摘要，并在用户明确要求且确认范围后执行提交。默认只读分析，不自动暂存、提交、推送、打标签、合并或修改工作区。

## 触发与边界

在以下请求中使用本 Skill：

- “帮我写提交说明/提交摘要/commit message”。
- “总结这次 Git/SVN 改动”。
- “检查暂存区后提交”或“准备一个 SVN 提交”。
- “这个 revision 做了什么？”

不要把提交摘要当成完整 changelog、PR 描述或发布说明。用户没有要求提交时，只生成摘要和建议命令，不执行写操作。

## 工作流

### 1. 确认版本库与范围

1. 先确认当前工作目录和用户指定的路径；不要猜测其他仓库。
2. 使用只读探测判断版本库类型：
   - Git：`git rev-parse --show-toplevel`。
   - SVN：`svn info --show-item wc-root`。
3. 若两者都可用，向用户说明选择依据并要求指定 Git 或 SVN；不要把两种差异混在一个提交中。
4. 确认范围：当前工作树、暂存区、指定文件、指定 revision，或用户给出的路径。没有范围时，默认分析当前仓库的工作区改动。
5. 如果仓库根目录与当前目录不同，报告根目录；不要把绝对路径写进摘要。

### 2. 收集证据（只读）

Git 至少检查：

```text
git status --short
git diff --stat
git diff
git diff --cached --stat
git diff --cached
git log -5 --oneline
```

SVN 至少检查：

```text
svn status
svn diff
svn info
svn log -l 5
```

按范围缩小命令；大型仓库先读 `status`、`stat` 和相关文件，再读取必要的完整 diff。不要为了写摘要读取 `.env`、凭据目录、密钥、Cookie、Token 或无关的大型构建产物。

对每个变更回答三个问题：

1. 改了什么：文件、模块、接口、数据、测试或文档。
2. 为什么改：从 diff、测试、Issue/任务号或用户说明中提取证据；没有证据就标为“原因未说明”，不要臆测。
3. 如何验证：已有测试、构建、lint、手工验证；未验证的内容明确写出。

发现疑似敏感内容、生成物、超大二进制或与请求无关的变更时，先暂停并指出风险。不要把敏感内容复制到摘要或日志。

### 3. 组织提交摘要

默认输出简洁、可检索的 Conventional Commit 风格：

```text
<type>(<scope>): <一句话摘要>

变更：
- <事实性的主要改动>
- <事实性的主要改动>

原因：
- <需求、缺陷或约束；没有证据时写“原因未说明”>

验证：
- <已执行的命令及结果>
- <未执行的验证，明确标注>
```

类型选择：`feat` 新能力，`fix` 缺陷修复，`refactor` 行为不变的重构，`docs` 文档，`test` 测试，`build`/`ci` 工具链，`chore` 维护。范围使用模块名或领域名，不使用机器路径。

一句话摘要要使用动词、说明结果、避免“更新代码”“修改若干文件”等空话；长度建议不超过 72 个字符。若变更包含多个互不相关主题，先建议拆成多个提交，并为每组分别给出摘要。

用户指定语言、格式、Issue key 或团队模板时，优先遵守；否则使用中文说明，保留稳定的英文 `type(scope)`。

### 4. 提交前门禁

只有满足以下条件才可以执行提交：

- 用户明确要求执行 `git commit` 或 `svn commit`；
- 已展示最终摘要、提交范围和验证状态；
- 已确认没有把凭据、无关文件或未审阅的大范围改动带入提交；
- Git 已明确哪些文件在 index 中，SVN 已明确提交路径；
- 生成的摘要与实际 diff 一致。

若用户只说“写摘要”“总结改动”或“准备提交”，不要提交。若需要暂存文件，列出精确路径并等待用户确认；禁止使用 `git add .`、`git add -A` 或等价的全量暂存来掩盖范围。

### 5. 执行确认后的提交

Git：

1. 再次运行 `git status --short` 和对应的 staged diff。
2. 只在已确认文件上暂存，例如 `git add -- path/to/file`；不要暂存 `.env`、凭据、构建目录或用户未确认的文件。
3. 使用用户确认的单条消息执行 `git commit -m "..."`。多段正文使用多个 `-m`，不要依赖交互式编辑器。
4. 读取 `git show --stat --oneline --summary HEAD`，报告 commit id、摘要和文件统计。

SVN：

1. 再次运行 `svn status` 与 `svn diff`，确认提交路径。
2. 使用精确路径执行 `svn commit path/to/file -m "..."`；不要把整个工作副本作为默认路径。
3. 读取提交输出或 `svn info`，报告新的 revision、摘要和验证状态。

提交完成后不要自动 `git push`、创建 tag、合并分支、关闭 Issue 或执行其他远程动作，除非用户另行明确要求。提交失败时保留工作区原状，报告错误、已执行命令和下一步，不要重试破坏性操作。

## 输出格式

按以下顺序返回：

1. `版本库`：Git/SVN、仓库范围和工作区状态。
2. `提交摘要`：最终建议的 subject 和正文。
3. `变更范围`：将纳入或排除的文件/路径。
4. `验证`：已执行与未执行的检查。
5. `动作`：未提交时给出“等待确认”；已提交时给出 commit id 或 SVN revision。

不要输出凭据、Token、Cookie、私钥、完整本机路径、未脱敏 URL 或与摘要无关的完整 diff。
