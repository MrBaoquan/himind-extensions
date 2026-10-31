# YDL platform-aware dry-run

运行：

```powershell
powershell -ExecutionPolicy Bypass -File .
un-ydl-dry-run.ps1
```

脚本会生成 8 秒合成测试视频，执行平台映射、三档 compare、IOStudio build/validate/tracks provenance 核对，并生成 delivery 与 checksums。

合成视频只用于软件 dry-run；真视频和机械验收必须走人工现场流程。
