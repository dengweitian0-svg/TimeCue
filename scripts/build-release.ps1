param(
    [string]$OutputDirectory = '',
    [switch]$SkipChecks,
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
    if (!$SkipChecks) {
        Invoke-Checked -Program 'go' -Arguments @('vet', './...')
        Invoke-Checked -Program 'go' -Arguments @('test', '-p', '1', './...')
    }
    Invoke-Checked -Program 'go' -Arguments @('build', '-trimpath', '-ldflags=-s -w', '-o', (Join-Path $taskStaging 'timecue-core.exe'), './cmd/flutter')
    Push-Location (Join-Path $taskRoot 'desktop')
    try {
        if (!$SkipFlutterBuild) {
            Invoke-Checked -Program 'flutter' -Arguments @('pub', 'get')
        }
        if (!$SkipChecks) {
            Invoke-Checked -Program 'dart' -Arguments @('format', '--output=none', '--set-exit-if-changed', 'lib', 'test')
            Invoke-Checked -Program 'flutter' -Arguments @('analyze', '--no-pub')
            Invoke-Checked -Program 'flutter' -Arguments @('test', '--no-pub')
        }
        if (!$SkipFlutterBuild) {
            Invoke-Checked -Program 'flutter' -Arguments @('build', 'windows', '--release', '--no-pub', '--target=lib/main.dart')
        }
    } finally { Pop-Location }

    $taskFlutterOutput = Join-Path $taskRoot 'desktop/build/windows/x64/runner/Release'
    $taskFlutterConfig = [IO.File]::ReadAllText((Join-Path $taskRoot 'desktop/windows/flutter/ephemeral/generated_config.cmake'))
    if ($taskFlutterConfig -notmatch 'FLUTTER_TARGET=lib[\\/]main\.dart') { throw '拒绝打包演示或测试入口，必须构建 lib/main.dart。' }
    $taskDefines = [regex]::Match($taskFlutterConfig, '"DART_DEFINES=([^\"]*)"').Groups[1].Value
    foreach ($taskDefine in $taskDefines.Split(',')) {
        if ($taskDefine -and [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($taskDefine)) -eq 'TIMECUE_DEMO=true') { throw '拒绝打包内存演示模式。' }
    }
    foreach ($taskRequired in @('timecue_desktop.exe', 'flutter_windows.dll', 'data/app.so', 'data/icudtl.dat', 'data/flutter_assets/assets/tray/app_icon.ico')) {
        if (!(Test-Path -LiteralPath (Join-Path $taskFlutterOutput $taskRequired))) { throw "发行构建缺少 $taskRequired" }
    }
    Copy-Item -Path (Join-Path $taskFlutterOutput '*') -Destination $taskStaging -Recurse
    Copy-Item -LiteralPath (Join-Path $taskRoot 'docs/RELEASE.md') -Destination (Join-Path $taskStaging 'README.md')
    $taskManifest = [ordered]@{
        version = $taskVersion
        platform = 'windows-x64'
        sourceCommit = (& git rev-parse HEAD | Out-String).Trim()
        sourceDirty = [bool](& git status --porcelain | Out-String).Trim()
        createdAtUtc = [DateTime]::UtcNow.ToString('o')
        goVersion = (& go version | Out-String).Trim()
        flutterSdk = $taskDefines.Split(',') | ForEach-Object { if ($_){ [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($_)) } } | Where-Object { $_ -match '^FLUTTER_(VERSION|CHANNEL)=' }
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
