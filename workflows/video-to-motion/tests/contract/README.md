# 契约测试

`run-local.ps1` 覆盖：

- Workflow manifest、入口出口、Loop、Artifact schema 与依赖
- 四个 IOUI 平台 profile 的有效能力和边界
- exact / approximate / unsupported 平台映射
- 错误通道、错误值类型、A3T 理论轴误用
- 三档 cue timing drift
- motion-composer 插件测试、构建与 manifest 校验
- `.hmwf` 打包与包校验
- YDL neutral=0 三档 dry-run 和 provenance 核对

该脚本不安装任何扩展，也不执行真实设备动作。
