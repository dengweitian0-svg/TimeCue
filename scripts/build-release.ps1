param(
    [string]$OutputDirectory = '',
    [switch]$SkipFlutterBuild
)

$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path -Parent $PSScriptRoot
if (!$OutputDirectory) { $OutputDirectory = Join-Path $taskRoot 'build/releases' }
$taskOutput = [IO.Path]::GetFullPath($OutputDirectory)
$taskVersionMatch = [regex]::Match([IO.File]::ReadAllText((Join-Path $taskRoot 'desktop/pubspec.yaml')), '(?m)^version:\s*(\S+)')
if (!$taskVersionMatch.Success) { throw '找不到桌面版本。' }
$taskVersion = $taskVersionMatch.Groups[1].Value
$taskName = "TimeCue-$taskVersion-windows-x64"
$taskStaging = Join-Path $taskOutput ($taskName + '-' + [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss-fffffff'))
New-Item -ItemType Directory -Path $taskStaging -Force | Out-Null

# 每个外部命令都检查退出码，失败时不生成成功标记或压缩包。
function Invoke-Checked {
    param([string]$Program, [string[]]$Arguments)
    & $Program @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Program 失败，退出码 $LASTEXITCODE" }
}

Push-Location $taskRoot
try {
    Invoke-Checked -Program 'go' -Arguments @('build', '-trimpath', '-ldflags=-s -w', '-o', (Join-Path $taskStaging 'timecue-core.exe'), './cmd/flutter')
    Push-Location (Join-Path $taskRoot 'desktop')
    try {
        if (!$SkipFlutterBuild) {
            Invoke-Checked -Program 'flutter' -Arguments @('pub', 'get')
        }
        if (!$SkipFlutterBuild) {
            Invoke-Checked -Program 'flutter' -Arguments @('build', 'windows', '--release', '--no-pub', '--target=lib/main.dart')
        }
    } finally { Pop-Location }

    $taskFlutterOutput = Join-Path $taskRoot 'desktop/build/windows/x64/runner/Release'
    $taskFlutterConfig = [IO.File]::ReadAllText((Join-Path $taskRoot 'desktop/windows/flutter/ephemeral/generated_config.cmake'))
    $taskFlutterSdkMatch = [regex]::Match($taskFlutterConfig, 'file\(TO_CMAKE_PATH\s+"([^"]+)"\s+FLUTTER_ROOT\)')
    if (!$taskFlutterSdkMatch.Success) { throw '缺少 Flutter SDK 路径。' }
    $taskFlutterSdkRoot = $taskFlutterSdkMatch.Groups[1].Value.Replace('\\', '\')
    $taskFlutterSdkVersionFile = Join-Path $taskFlutterSdkRoot 'bin/cache/flutter.version.json'
    if (!(Test-Path -LiteralPath $taskFlutterSdkVersionFile)) { throw "找不到构建配置所用 Flutter SDK 的版本信息：$taskFlutterSdkVersionFile" }
    try { $taskFlutterSdkInfo = [IO.File]::ReadAllText($taskFlutterSdkVersionFile) | ConvertFrom-Json -ErrorAction Stop }
    catch { throw 'Flutter SDK 未提供有效的机器可读版本信息。' }
    if (!$taskFlutterSdkInfo.frameworkVersion -or !$taskFlutterSdkInfo.channel) { throw 'Flutter SDK 版本信息缺少 frameworkVersion 或 channel。' }
    $taskConfiguredTarget = [regex]::Match($taskFlutterConfig, '"FLUTTER_TARGET=([^\"]*)"').Groups[1].Value.Replace('\\', '\')
    if (!$taskConfiguredTarget) { throw '缺少 Flutter 构建入口。' }
    if (![IO.Path]::IsPathRooted($taskConfiguredTarget)) {
        $taskConfiguredTarget = Join-Path (Join-Path $taskRoot 'desktop') $taskConfiguredTarget
    }
    $taskExpectedTarget = [IO.Path]::GetFullPath((Join-Path $taskRoot 'desktop/lib/main.dart'))
    if ([IO.Path]::GetFullPath($taskConfiguredTarget) -ine $taskExpectedTarget) { throw '拒绝打包演示或测试入口，必须构建 lib/main.dart。' }
    $taskDefines = [regex]::Match($taskFlutterConfig, '"DART_DEFINES=([^\"]*)"').Groups[1].Value
    foreach ($taskDefine in $taskDefines.Split(',')) {
        if ($taskDefine -and [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($taskDefine)) -eq 'TIMECUE_DEMO=true') { throw '拒绝打包内存演示模式。' }
    }
    foreach ($taskRequired in @('timecue_desktop.exe', 'flutter_windows.dll', 'data/app.so', 'data/icudtl.dat', 'data/flutter_assets/assets/tray/app_icon.ico')) {
        if (!(Test-Path -LiteralPath (Join-Path $taskFlutterOutput $taskRequired))) { throw "发行构建缺少 $taskRequired" }
    }
    Copy-Item -Path (Join-Path $taskFlutterOutput '*') -Destination $taskStaging -Recurse
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'update.py') -Destination (Join-Path $taskStaging 'update.py')
    $taskReleaseNotes = Join-Path $taskRoot 'docs/RELEASE.md'
    $taskPackageReadme = Join-Path $taskStaging 'README.md'
    if (Test-Path -LiteralPath $taskReleaseNotes -PathType Leaf) {
        Copy-Item -LiteralPath $taskReleaseNotes -Destination $taskPackageReadme
    } else {
        $taskFallbackReadme = @'
# TimeCue Windows 使用说明

- 请保留解压后的完整目录，其中包含 `timecue_desktop.exe`、Flutter 运行文件及 `timecue-core.exe`；不要只复制主程序。
- 双击 `timecue_desktop.exe` 启动。默认使用托管模式，并将数据保存在当前用户的 `%LOCALAPPDATA%\TimeCue`。
- 关闭窗口会隐藏到托盘；需要结束应用时，请从托盘菜单选择退出。
- 更新时先退出 TimeCue，将新版 ZIP 解压到新目录，再在该目录运行 `python update.py`；旧版尚未登记安装位置时，先在新目录运行 `python update.py --register "D:\TimeCue"`，再运行 `python update.py`。
- 可通过参数指定数据目录，例如：`timecue_desktop.exe --data-dir="D:\TimeCueData"`。
- 开发外连模式需先启动 Go 服务，例如：`timecue_desktop.exe --backend=external --data-dir="D:\TimeCueData"`；退出 TimeCue 不会停止手动启动的 Go 服务。
- 如果 `127.0.0.1:10086` 已被占用，TimeCue 会拒绝启动，不会结束占用端口的其他进程。
'@
        [IO.File]::WriteAllText(
            $taskPackageReadme,
            $taskFallbackReadme.TrimStart(),
            [Text.UTF8Encoding]::new($false)
        )
    }
    $taskManifest = [ordered]@{
        version = $taskVersion
        platform = 'windows-x64'
        sourceCommit = (& git rev-parse HEAD | Out-String).Trim()
        sourceDirty = [bool](& git status --porcelain | Out-String).Trim()
        createdAtUtc = [DateTime]::UtcNow.ToString('o')
        goVersion = (& go version | Out-String).Trim()
        flutterSdk = [ordered]@{ version = $taskFlutterSdkInfo.frameworkVersion; channel = $taskFlutterSdkInfo.channel }
        files = @(Get-ChildItem -LiteralPath $taskStaging -Recurse -File | ForEach-Object {
            [ordered]@{ path = $_.FullName.Substring($taskStaging.Length + 1).Replace('\', '/'); sha256 = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash }
        })
    }
    $taskManifest | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $taskStaging 'manifest.json') -Encoding utf8
    $taskZip = "$taskStaging.zip"
    Compress-Archive -LiteralPath $taskStaging -DestinationPath $taskZip -CompressionLevel Optimal
    $taskHash = (Get-FileHash -LiteralPath $taskZip -Algorithm SHA256).Hash
    "$taskHash  $([IO.Path]::GetFileName($taskZip))" | Set-Content -LiteralPath "$taskZip.sha256" -Encoding ascii
    Write-Output "发行目录：$taskStaging"
    Write-Output "发行压缩包：$taskZip"
} finally { Pop-Location }
