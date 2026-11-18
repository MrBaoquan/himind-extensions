---
name: extension-authoring
description: 创建、修改或评审 HiMind 插件、技能与工作流。根据用户目标选择扩展类型，通过 Agent 验证并交付候选。不用于普通软件开发或运行已有工作流。
---

# 扩展创作

先读[共用规范](references/conventions.md)，再按任务选择指南：

| 用户需要 | 类型 |
| --- | --- |
| 确定性的文件、进程、数据或设备操作 | [插件](references/plugin.md) |
| AI 根据场景判断、遵循操作知识或组合现有能力 | [技能](references/skill.md) |
| 有步骤依赖、审批、循环、状态或可验证交付物 | [工作流](references/workflow.md) |

已有工程先读清单，不因用户泛称「插件」就迁移类型。跨类型先复用现有能力，再按依赖顺序创建和验证缺失组件。仅评审时不安装、不写源码、不生成候选。

## 共用流程

1. 从需求提取可验证的结果、约束和失败行为。对可逆细节可声明假设；工作区不明，或歧义影响权限、外部写入及关键交付时，先询问用户。
2. 在支持该参数的调用中显式传入本会话绝对 `workspace_root`，不依赖共享绑定。调用 `extension.authoring.preflight` 时传 `kind` 和 `workspace_root`，核对返回工作区及 `source=request`。调用 `extension.environment.preflight` 时只传 `kind`。预检阻塞时停止写入，按 `blockers[].code` 和 `next_steps` 处理，不解析本地化文案。
3. 已发布内容变化则升版，不覆盖原制品；需要派生候选时用 `extension.revision.create`。清单支持 `author` 时通过 `extension.authoring.identity` 取作者。显式核对 `distribution_targets`，不把脚手架占位当成完成品。
4. 按类型校验、构建、打包、保存候选，然后调用 `extension.test`，只传 `kind`、`id` 和 `version`。把结果关联到同一个候选 SHA-256，区分结构校验、运行验证和业务验收；模拟、跳过和未覆盖不能记为真实通过。
5. 默认只交付本地候选、文件路径、摘要和验证结果。只有在工作台已对接、用户明确授权提审且确认的是同一个候选时，才按类型调用候选确认与提审能力。签名、发布和撤回不属于此流程。不得读取或传递凭据、自动补装业务依赖或在目标工作区外改源码。
