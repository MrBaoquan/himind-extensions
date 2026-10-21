# project.config.json 字段速查

## 文件优先级

- `project.config.json`：团队共享，影响编译/上传产物的开关必须写这里。
- `project.private.config.json`：个人本机偏好，同名字段优先。

## 常用字段

| 字段 | 写入 | 说明 |
|---|---|---|
| `appid` | common / private | private 优先 |
| `projectname` | common | 项目名称 |
| `compileType` | common | `miniprogram` / `game` / `plugin` |
| `libVersion` | common | 基础库版本，可写 `latest` / `trial` / `widelyUsed` |
| `miniprogramRoot` | common | 小程序源码目录 |
| `pluginRoot` | common | 插件源码目录 |
| `cloudfunctionRoot` | common | 云函数目录 |
| `cloudbaseRoot` | common | 云开发目录 |
| `setting.urlCheck` | private | 合法域名校验 |
| `setting.compileHotReLoad` | private | 保存后热重载 |
| `setting.es6` + `setting.enhance` | common | 必须同开同关 |
| `setting.minified` | common | 上传压缩 JS |
| `setting.minifyWXSS` / `minifyWXML` | common | 上传压缩样式/模板 |
| `setting.useCompilerPlugins` | common | 编译插件，如 `["typescript","less"]` |
| `packOptions.ignore` / `include` | common | 打包忽略/强制包含 |
| `scripts.beforeCompile` | common | 编译前本地命令，需审批 |
| `scripts.beforePreview` | common | 预览前本地命令，需审批 |
| `scripts.beforeUpload` | common | 上传前本地命令，需审批 |

## 修改原则

1. 先读 common 和 private，理解生效值。
2. 开发期偏好写 private；编译/上传产物开关写 common。
3. 合并修改，保留其余字段和合法 JSON。
4. 改 `packOptions` / `watchOptions` 后可能需要重开项目。
5. 不编造未出现过的字段名。
