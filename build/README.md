# Wails 构建资源

本目录由 Wails v2.15 构建流程使用。Windows 发布构建从仓库根目录执行：

```powershell
wails build -platform windows/amd64 -webview2 embed
```

Wails 会在此目录维护平台元数据、图标和 `bin/` 输出；生成的二进制不提交到仓库。
