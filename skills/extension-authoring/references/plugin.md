# 插件创作

1. 定义 Capability 的实际输入、输出、风险和最小权限。调用 `extension.plugin.scaffold`，传入 `output_dir`、`name`、`display_name`、`description`、`author`、`categories`、`release_notes` 和模板：只读工具选 `readonly-tool`，长任务选 `job-worker`，独立界面选 `ui-tool`。
2. JSON-RPC 2.0 走 stdin/stdout，脱敏日志走 stderr。核对清单的入口、平台、命名空间、真实插件依赖和分发目标。不接受任意 Shell、任意 URL、明文凭据或越界路径。长任务额外验证进度、取消和失败行为。
3. 用 `extension.plugin.validate` 校验目录，`extension.plugin.build` 执行 Go 测试和编译，`extension.plugin.package` 传 `path` 和 `output` 生成 `.hmpkg`。用 `extension.plugin.candidate.save` 传 `package_path` 保存包，再执行共用候选测试。传输探针不能代替业务断言；使用真实 Capability 的典型和异常输入验证结果。
4. 按共用流程获得提审授权后，依次调用 `extension.plugin.candidate.confirm` 和 `extension.plugin.submission.submit`；提交成功后才查询 `extension.plugin.submission.status`。
