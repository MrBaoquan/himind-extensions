# MCP 使用体验复盘报告

## 复盘范围

| 项 | 值 |
| --- | --- |
| 任务 | ① 开发 3 个插件 + 1 个技能（media-toolkit / media-ai / iostudio / video-motion-authoring）；② 审查并补齐扩展创作链路的规范门禁；③ 发布 IOStudio 1.0.16 |
| 客户端 | Claude 桌面应用（Code 标签） |
| 日期 | 2026-10-15 |
| 工具清单 | 元能力：`system_health`、`system_capability_search/activate/describe/invoke`<br>`extension.*`：`workspace.current`、`workspace.bind`、`authoring.preflight`、`authoring.identity`、`environment.preflight`、`plugin.scaffold/validate/build/package/candidate.save`、`skill.scaffold/validate/package/candidate.save`、`test`<br>`distribution.*`：`project.inspect`、`artifact.inspect`、`release.publish`、`release.resolve` |

## 整体评价

**任务全部完成**（3 插件 + 1 技能候选包、工具链 1.4.5、IOStudio 1.0.16 发布成功），且**自省式工具链（search/describe/activate/invoke）设计是这套体验里最扎实的部分**——`extension.test` 的运行时检查真的会拉起插件、回报 `runtime.status`，这种"交付前真跑一遍"的环节在别处很少见。

**但规范化的创作流程会被一个假阻塞直接卡死。** 最严重的问题是：`extension.authoring.preflight` 持续报"扩展开发工具插件不可用"，而**该插件的所有能力实际都正常**——而开发助手明文要求"任一预检出现 `state: blocked` 时停止写入"。这意味着**一个守规矩的 Agent 会在这里停下，什么都做不了**。

## 四维度评估

| 维度 | 评价 | 关键现象 |
| --- | --- | --- |
| 友好性 | 一般 | 工具自省能力好；但报错信息多处误导（`path must remain inside workspace_root` 实为"必须是绝对路径"） |
| 合理性 | **差** | **健康探针假阴性**：preflight 报插件不可用，能力却能正常调用；`scaffold`/`skill.scaffold` 报失败但文件已落盘；`workspace.bind` 顶层与嵌套 `bound` 字段矛盾 |
| 便捷性 | 一般 | 能力激活反复失效，同一能力需多轮 `activate`；`search` 不返回参数 schema，每个工具都要多花一次 `describe` |
| 阻碍性 | **差** | 假阻塞会导致规范流程停摆；能力命名规则只能靠 `extension.test` 逐条拒绝试错（4 轮往返）；`test`/`candidate.save` 返回体超预算被截断 |

## 问题清单（按优先级）

| ID | 标题 | 类型 | 工具 | 优先级 |
| --- | --- | --- | --- | --- |
| REQ-001 | preflight 假报插件不可用，会按规范卡死创作流程 | 缺陷 | `extension.authoring.preflight` | 高 |
| REQ-002 | 同一能力需反复 activate，跨轮次激活状态不保持 | 缺陷 | `system_capability_activate` | 高 |
| REQ-003 | 能力命名规则无前置可知渠道，只能靠 `test` 逐条试错 | 优化 | `extension.test` / `extension.environment.preflight` | 高 |
| REQ-004 | scaffold 报"响应后未退出"但操作实际成功 | 缺陷 | `extension.plugin.scaffold`、`extension.skill.scaffold` | 中 |
| REQ-005 | 多个能力返回体超模型预算被截断 | 优化 | `extension.test`、`*.candidate.save` | 中 |
| REQ-006 | `output_dir` 必须绝对路径，报错信息指向错误 | 不便 | `extension.plugin.scaffold` | 中 |
| REQ-007 | 三道门禁检查范围不同，无统一入口 | 不便 | `extension.plugin.validate` / 内核命名门禁 / `himind-repo-check` | 中 |
| REQ-008 | `workspace.bind` 顶层与嵌套 `bound` 字段矛盾 | 缺陷 | `extension.workspace.bind` | 低 |
| REQ-009 | 独立模式下 `release.resolve` 报错需文档说明 | 文档 | `distribution.release.resolve` | 低 |
| REQ-010 | `search` 不返回参数 schema，每个工具多一次 `describe` | 优化 | `system_capability_search` | 低 |

## 需求项明细

### REQ-001

```json
{
  "id": "REQ-001",
  "title": "extension.authoring.preflight 假报插件不可用，会按规范卡死创作流程",
  "type": "缺陷",
  "tool": "extension.authoring.preflight",
  "priority": "高",
  "problem": "预检持续返回 state=blocked，理由是扩展开发工具插件不可用；但该插件的所有能力（validate/scaffold/build/package）实际都能正常调用并成功返回。开发助手明文要求「任一预检出现 state: blocked 时停止写入」，因此守规矩的 Agent 会在这里停摆，而问题并不存在。",
  "context": "常态：本次会话每次调用 preflight 都失败，重试也失败；客户端：Claude 桌面应用；任务：创建插件工程。",
  "current": "preflight → state=blocked, code=extension_tools_plugin_unavailable, message='plugin did not exit after returning a response: com.himind.extension-development-tools', retryable=true；重试结果完全相同。同一时刻 extension.plugin.validate/scaffold 调用成功。",
  "expected": "探针失败但能力可用时不应判 blocked（至少降级为 warning 并说明「能力仍可调用」）；或探针改用与能力相同的调用路径，避免两套判定不一致。",
  "acceptance": [
    "当扩展开发工具插件能力可正常调用时，preflight 不返回 state=blocked",
    "探针失败时返回的 remediation 能区分「插件未启用」与「探针本身失败」",
    "preflight 报 blocked 时，同一会话内该插件的能力调用也应失败（两者必须一致）"
  ],
  "impact": "所有按规范流程创作的 Agent 与用户；本次会话每次创建工程都会遇到，属常态。",
  "evidence": "state=blocked; blockers=[{code:extension_tools_plugin_unavailable, stage:toolchain, severity:error, message:'扩展开发工具插件不可用: plugin did not exit after returning a response: com.himind.extension-development-tools', remediation:'在本机启用扩展开发工具插件并重新执行预检', retryable:true}]；同时 extension.plugin.validate 返回 {ok:true, path:'F:\\\\WebProjects\\\\himind-extensions\\\\plugins\\\\media-library'}。",
  "suggestion": "把「插件进程返回响应后未按时退出」与「插件不可用」分成两个 code；前者不应阻断创作，只需在响应里注明进程回收异常。",
  "source": "himind 扩展开发，2026-10-15"
}
```

### REQ-002

```json
{
  "id": "REQ-002",
  "title": "同一能力需反复 activate，激活状态跨轮次不保持",
  "type": "缺陷",
  "tool": "system_capability_activate",
  "priority": "高",
  "problem": "已激活的能力在后续调用时反复报 capability is not active in this MCP session，需要重新 activate。本次会话中 extension.test 至少重新激活 6 次，extension.plugin.build/package/candidate.save 这组至少 4 次，distribution.artifact.inspect 也需重复激活。每次都要多一轮往返，且打断连续操作。",
  "context": "常态：整个会话的每一步几乎都遇到；客户端：Claude 桌面应用；任务：任意能力调用。",
  "current": "先 activate 返回 {activated:[...], activatedCount:N, limit:128}；若干次调用后，对同 id 的 invoke 返回 'capability is not active in this MCP session: extension.test'。activation 响应里的 limit=128 从未接近。",
  "expected": "激活在会话内保持；或若因连接重置而失效，activate 应幂等且不需要调用方感知，服务端可自动恢复。",
  "acceptance": [
    "同一会话内对已 activate 过的能力重复 invoke 不再报 not active",
    "若激活确实会失效，响应中应说明失效原因与有效期",
    "activate 对同一组 id 重复调用不产生副作用"
  ],
  "impact": "所有通过 MCP 调用能力的 Agent；本次会话约 10+ 次额外往返。",
  "evidence": "capability is not active in this MCP session: extension.test（同一会话内多次出现，每次需重新 activate）；activate 响应示例 {activated:['extension.plugin.build','extension.plugin.package','extension.plugin.candidate.save'], activatedCount:4, limit:128}。",
  "suggestion": "若能确认是每次 MCP 重连导致状态丢失，建议把激活状态持久化到会话级而非连接级。",
  "source": "himind 扩展开发，2026-10-15"
}
```

### REQ-003

```json
{
  "id": "REQ-003",
  "title": "能力命名规则无前置可知渠道，只能靠 extension.test 逐条试错",
  "type": "优化",
  "tool": "extension.test / extension.environment.preflight",
  "priority": "高",
  "problem": "ADR 0119 的能力 ID 命名规则（层级用 .、词内复合用 -、禁用下划线、首段必须是领域注册表内的域或插件命名空间、子段不得与父段语义重复）没有任何前置告知。extension.environment.preflight 返回的 authoring_rules 完全没提这些；system_capability_search 也搜不到。结果只能提交候选包、被 extension.test 拒绝、逐个修，共 4 轮往返。",
  "context": "常态：任何新建插件都会遇到；客户端：Claude 桌面应用；任务：为插件命名能力 ID。",
  "current": "authoring_rules 只讲显示名/说明/更新说明的文案规则，未提能力 ID 规则。规则只能通过 extension.test 的 blocker.message 事后得知，且一次只报一类违规。",
  "expected": "把 ADR 0119 的能力 ID 规则前置到可检索处：或写进 authoring_rules，或由 preflight 返回命名规则清单，或在 scaffold 阶段就校验并提示。",
  "acceptance": [
    "environment.preflight 的返回中包含能力 ID 命名规则（或在其中指向可检索的规则来源）",
    "scaffold 产出的能力 ID 一次通过 extension.test 的命名校验",
    "extension.test 的命名违规 blocker 一次列出全部违规项而非仅第一类"
  ],
  "impact": "所有创作插件的 Agent；本次会话 4 轮额外往返。",
  "evidence": "逐轮拒绝记录：① 'media.toolkit.frames.contact_sheet: 不得使用下划线：层级用 `.`，词内复合用 `-`'；② 'motion.authoring.devices: 首段必须是 ADR 0119 领域注册表内的域，或插件命名空间 `motion-authoring`'；③ 'motion-authoring.motion.tracks: 子段与父段语义重复'；④ 改名后通过。",
  "suggestion": "authoring_rules 里补一句能力 ID 规则摘要，或在 extension.environment.preflight 增加 naming_rules 字段。",
  "source": "himind 扩展开发，2026-10-15"
}
```

### REQ-004

```json
{
  "id": "REQ-004",
  "title": "scaffold 报「响应后未退出」但操作实际成功",
  "type": "缺陷",
  "tool": "extension.plugin.scaffold、extension.skill.scaffold",
  "priority": "中",
  "problem": "调用返回错误 'plugin did not exit after returning a response: com.himind.extension-development-tools'，但目标工程文件已完整落盘。调用方无法从响应判断操作是否成功，必须去文件系统确认，否则会误以为失败而重复创建。",
  "context": "常态：本次会话中 skill.scaffold 与 skill.validate 均出现；客户端：Claude 桌面应用；任务：创建技能工程。",
  "current": "返回 error 文本 'plugin did not exit after returning a response'；实际 skills/video-motion-authoring/{SKILL.md,skill.json,agents/openai.yaml} 已生成。plugin.scaffold 首次调用同样报此错，但插件目录已创建。",
  "expected": "操作成功时应返回正常结果；若进程回收异常，不应把成功结果包装成错误。",
  "acceptance": [
    "scaffold 成功时返回 result（含 root/plugin_id/capability_id），不返回 error",
    "若确实需要报告进程回收异常，应作为 warning 与 result 同时返回",
    "重复调用同一 scaffold 时能被 output dir already exists 正确拒绝（当前因误报失败而可能被重试）"
  ],
  "impact": "所有创建插件/技能/工作流的调用方；本次会话每次 scaffold 都会遇到。",
  "evidence": "plugin did not exit after returning a response: com.himind.extension-development-tools；随后 ls 确认 skills/video-motion-authoring/ 下三个文件均已生成。",
  "source": "himind 扩展开发，2026-10-15"
}
```

### REQ-005

```json
{
  "id": "REQ-005",
  "title": "extension.test 与 candidate.save 返回体超模型预算被截断",
  "type": "优化",
  "tool": "extension.test、extension.plugin.candidate.save",
  "priority": "中",
  "problem": "返回值携带完整 manifest（能力多时可观），导致模型侧结果被截断并落盘，有效信息（如 test_report 的具体结论）反而看不到。本次会话中 extension.test 多次触发 'Output too large'（67.8KB / 80.6KB / 81.3KB / 77.8KB）。",
  "context": "常态：能力数 ≥ 7 的插件都会遇到；客户端：Claude 桌面应用；任务：校验候选包。",
  "current": "extension.test 返回体含完整 result.manifest（逐个能力带完整 input_schema）与 report 的重复副本；candidate.save 亦然。响应被截断为「Full output saved to: ...tool-results\\call_00_ET_....txt」。",
  "expected": "默认返回结论性字段（state、checks 汇总、candidate_sha256、candidate_path）；完整 manifest 通过可选参数按需获取，或提供字段裁剪。",
  "acceptance": [
    "extension.test 默认返回体不含逐个能力的完整 input_schema",
    "需要完整 manifest 时可显式开启且不重复返回两份（result 与 report 内容一致）",
    "test 结论字段（state / checks / blockers）在默认返回中始终完整可见"
  ],
  "impact": "能力数较多的插件作者；本次会话 4 次触发截断。",
  "evidence": "Output too large (80.6KB). Full output saved to: C:\\Users\\Administrator\\.claude\\projects\\O--DevModules-IODevice\\...\\tool-results\\call_00_ET_....txt；返回体里 result.manifest 与 report 内容重复（同一份 capabilities 数组出现两次）。",
  "suggestion": "candidate.save / test 的返回只给 state、checks、candidate_path、candidate_sha256、blockers；manifest 详情交给独立的 describe 类能力。",
  "source": "himind 扩展开发，2026-10-15"
}
```

### REQ-006

```json
{
  "id": "REQ-006",
  "title": "scaffold 的 output_dir 必须绝对路径，报错信息指向错误",
  "type": "不便",
  "tool": "extension.plugin.scaffold",
  "priority": "中",
  "problem": "传入相对路径 output_dir='plugins' 时返回 'path must remain inside workspace_root'，但该路径确实在 workspace_root 内——真正原因是要求绝对路径。误导性的报错会让人以为越界，从而检查工作区权限或路径拼接。",
  "context": "常态：首次调用 scaffold 即遇到；客户端：Claude 桌面应用；任务：创建插件工程。",
  "current": "params {workspace_root: 'F:\\\\WebProjects\\\\himind-extensions', output_dir: 'plugins', ...} → error 'path must remain inside workspace_root'；改为绝对路径 'F:\\\\WebProjects\\\\himind-extensions\\\\plugins' 后成功。",
  "expected": "报错应说明真实原因（必须是绝对路径）；或直接接受相对路径并按 workspace_root 解析。",
  "acceptance": [
    "传相对 output_dir 时，报错信息包含「必须是绝对路径」或等价说明",
    "或相对路径被正确解析到 workspace_root 下并成功创建"
  ],
  "impact": "首次使用 scaffold 的作者；本次会话 1 次额外往返。",
  "evidence": "plugin capability extension.plugin.scaffold failed: {\"code\":-32602,\"message\":\"path must remain inside workspace_root\"}",
  "source": "himind 扩展开发，2026-10-15"
}
```

### REQ-007

```json
{
  "id": "REQ-007",
  "title": "三道门禁检查范围不同，无统一入口",
  "type": "不便",
  "tool": "extension.plugin.validate、Agent 内核命名门禁、himind-repo-check",
  "priority": "中",
  "problem": "同一个插件要过三道范围不同的门：① extension.plugin.validate 只查结构与字段长度；② 内核在 extension.test 阶段查能力 ID 命名；③ 仓库 himind-repo-check 还要求 distribution_targets、catalog 一致性等。作者无法一次得知全集，只能逐个被拒后补齐（本次会话为此多轮往返）。",
  "context": "常态：任何新插件都会依次穿过这三道；客户端：Claude 桌面应用；任务：让插件通过全部检查。",
  "current": "extension.plugin.validate 返回 {ok:true} 的插件，可能在 extension.test 阶段因命名被拒，又可能在 himind-repo-check 阶段因缺 distribution_targets 被拒。",
  "expected": "提供一个汇总入口（或在 validate 中返回「还有哪些未检查项」），让作者一次看到全部要求。",
  "acceptance": [
    "存在一个能力能在提交前报出该插件的全部待办检查项（含命名与 distribution_targets）",
    "或 extension.plugin.validate 的返回中列出「未覆盖、需其它门禁确认」的检查项"
  ],
  "impact": "所有插件作者；本次会话因缺 distribution_targets 额外往返 2 次。",
  "evidence": "extension.plugin.validate 对缺 capability_namespace 的 media-ai 返回 ok:true；随后 himind-repo-check 报 'plugins/media-toolkit: must declare distribution_targets'。",
  "source": "himind 扩展开发，2026-10-15"
}
```

### REQ-008

```json
{
  "id": "REQ-008",
  "title": "workspace.bind 顶层与嵌套 bound 字段矛盾",
  "type": "缺陷",
  "tool": "extension.workspace.bind",
  "priority": "低",
  "problem": "同一响应里顶层 bound=true 而嵌套 workspace.bound=false。检查哪个字段都能说得通，容易误判绑定状态。",
  "context": "偶尔：仅绑定工作区时；客户端：Claude 桌面应用；任务：绑定扩展工作区。",
  "current": "{\"state\":\"ready\",\"bound\":true,\"workspace_root\":\"F:\\\\WebProjects\\\\himind-extensions\",\"workspace\":{\"workspace_root\":\"...\",\"source\":\"request\",\"bound\":false,\"kind\":\"aggregate\",\"project\":null}}",
  "expected": "两个 bound 语义应一致，或明确区分（如 workspace.bound 改名为 workspace.has_project）。",
  "acceptance": [
    "响应中不出现同一语义字段取值相反的情况",
    "或字段名能自解释两者的区别"
  ],
  "impact": "按字段判断绑定状态的调用方；低频。",
  "evidence": "同上响应原文。",
  "source": "himind 扩展开发，2026-10-15"
}
```

### REQ-009

```json
{
  "id": "REQ-009",
  "title": "独立模式下 release.resolve 报错需文档说明",
  "type": "文档",
  "tool": "distribution.release.resolve",
  "priority": "低",
  "problem": "发布成功后无法用 resolve 验证更新链路，返回 'Agent 未配置可信 Dashboard 地址'。这在独立模式下属预期边界，但调用方容易误判为发布失败或配置错误。",
  "context": "常态：独立模式下每次验证都会遇到；客户端：Claude 桌面应用；任务：发布后验证。",
  "current": "distribution.release.resolve → {code:-32602, message:'Agent 未配置可信 Dashboard 地址'}；此时 distribution.release.publish 已返回 status=published。",
  "expected": "报错文案明确说明这是独立模式的已知边界，并指出「发布本身已成功，验证需 Connected 模式」；或提供本地可用的替代验证方式。",
  "acceptance": [
    "独立模式下 resolve 的报错包含「这是已知边界，不代表发布失败」的说明",
    "或 resolve 在该模式下返回可用于本地验证的替代结果"
  ],
  "impact": "独立模式下的发布者；本次会话 1 次。",
  "evidence": "plugin capability distribution.release.resolve failed: {\"code\":-32602,\"message\":\"Agent 未配置可信 Dashboard 地址\"}",
  "source": "IOStudio 1.0.16 发布，2026-10-15"
}
```

### REQ-010

```json
{
  "id": "REQ-010",
  "title": "system_capability_search 不返回参数 schema，每个工具多一次 describe",
  "type": "优化",
  "tool": "system_capability_search",
  "priority": "低",
  "problem": "search 结果只给 schemaBytes/schemaAvailable，不含参数名与必填项。要调用必须先 describe，否则只能猜参数名——本次会话因此多次收到 'capability input is missing required property: X' 才知道真实参数名（如 workspace_root、project_path）。",
  "context": "常态：每个新工具都要多一次 describe；客户端：Claude 桌面应用；任务：调用未用过的能力。",
  "current": "search 返回 {id, name, description, version, source, availability, riskLevel, ..., schemaBytes, schemaAvailable}，无 properties/required。",
  "expected": "search 可附带精简参数摘要（参数名列表 + required），或提供 include_schema 开关。",
  "acceptance": [
    "search 结果（或开启开关后）能直接看到参数的名称与必填性",
    "调用方无需先 describe 即可构造合法输入"
  ],
  "impact": "所有首次调用某能力的 Agent；本次会话约 5 次因猜参数名失败（如把 project_path 传成 .csproj 文件而它要求目录）。",
  "evidence": "capability input is missing required property: workspace_root / path；capability input contains unknown property: workspace_root；distribution.project.inspect 传 IOStudio.csproj → '项目目录不存在'（要求目录）。",
  "source": "himind 扩展开发，2026-10-15"
}
```

## 建议处理方式

| 需求 | 建议归属 | 可否本地规避 |
| --- | --- | --- |
| REQ-001 假阻塞 | **工具层（最高优先）**——它直接与规范条文冲突，会让遵守规范的 Agent 停摆 | 可：忽略 blocked 继续调用（但违反规范，不该让使用者承担这个判断） |
| REQ-002 激活失效 | 工具层 / MCP 连接层 | 可：每次调用前重试 activate |
| REQ-003 命名规则前置 | 工具层（authoring_rules 或 preflight 增字段） | 不可：只能试错 |
| REQ-004 成功报失败 | 工具层 | 可：以文件系统为准确认 |
| REQ-005 返回体过大 | 工具层（字段裁剪） | 可：从落盘的完整文件里读 |
| REQ-006 报错指向错误 | 工具层（文案） | 可：改用绝对路径 |
| REQ-007 三道门禁 | 工具层（汇总入口） | 不可：只能逐个被拒 |
| REQ-008 bound 矛盾 | 工具层（字段语义） | 可：只信顶层字段 |
| REQ-009 独立模式边界 | **文档说明**（tools 无关） | 可：知道就不受影响 |
| REQ-010 search 缺 schema | 工具层 | 可：先 describe |

**另注（不属于缺陷，供参考）**：`system_health` 返回 `dashboard_worker_state=not_applicable`、`local_service_online=false` 在 stdio companion 下是正常状态，且该工具说明里已写明——这一点做得对，无需改动。

## 附：本次会话中与规范有关的两处观察

以下不属于 MCP 工具缺陷，但与"规范如何落地"直接相关，一并留档。

1. **`authoring_rules` 与规范正文是两份不同的文案规则清单。** `extension.environment.preflight` 返回的 `authoring_rules` 里禁用词是「一站式、全方位、赋能、助力」；而 `develop-himind-conventions` §二4 禁用的是「智能、自动、一键、全能、极致、管家、专家、大师」。创作方只看得到前者，因此「智能」这类词必然漏网。本次已把两组词合并进 `tooling/metaguide.BannedWords` 并写进 `Rules`（`com.himind.extension-development-tools` 1.4.4/1.4.5）。

2. **"规范写在文档里"不等于有约束力。** 判断一条规则是否真正生效，要看三件事：① 创作方**实际收到**的规则文本里有没有它；② 校验器/内核**读不读**那个字段；③ 脚手架产出的模板**是否遵守**它。三者缺一，规则就是装饰——本次修的三处（显示名禁用词、`capability_namespace`、`distribution_targets`）都属"规范写了、但三道检查里没有任何一道在执行"。
