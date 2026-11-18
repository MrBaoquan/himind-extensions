# 技能创作

1. 定义触发与不触发的情形、必要输入、期望输出和能力缺失时的处理。用 `extension.skill.scaffold` 创建独立工程，传 `output_dir`、`slug`、`id`、`name`、`description`、`author`、`categories`、`release_notes` 和所需版本、客户端。通用技能声明 `agent-skills`，有客户端专属行为时才限制客户端。
2. 保持 `SKILL.md` 的 frontmatter、清单和 `agents/openai.yaml` 文案一致。实际打包文件全部列入 `contents`；正文和引用文件调用的 Capability 全部声明，必需性与降级策略一致。
3. 用 `extension.skill.validate` 校验目录，调用 `extension.skill.package` 生成 `.hmskill`，用 `extension.skill.candidate.save` 传 `package_path` 保存原包，再执行共用候选测试。记录实际完成注册的客户端；目录渲染不等于对话正确。用真实对话验证触发、不触发和缺依赖时的行为。
4. 按共用流程获得提审授权后，依次调用 `extension.skill.candidate.confirm` 和 `extension.skill.submission.submit`；提交成功后才查询 `extension.skill.submission.status`。
