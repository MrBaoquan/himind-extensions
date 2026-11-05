# 运行时目录

把与当前平台匹配的 ffmpeg / ffprobe 放在：

```text
runtime/windows-x64/ffmpeg.exe
runtime/windows-x64/ffprobe.exe
runtime/windows-x64/manifest.json
```

`manifest.json` 不是强制文件，但推荐提供。插件会在存在时校验：

- `platform` 与当前平台目录一致；
- `files.ffmpeg.exe.sha256` / `files.ffprobe.exe.sha256`；
- 可选 `size` 与实际文件大小一致。

没有 manifest 时，插件仍允许本地开发构建运行，但 `runtime.status` 会返回“无法证明来源与完整性”的风险提示。

注意：FFmpeg 及其编解码器有独立许可证。对外分发或提交市场前，应由发布负责人确认所选构建的许可证与再分发义务。
