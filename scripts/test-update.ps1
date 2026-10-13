param()

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2
$helper = Join-Path $PSScriptRoot 'install-update.ps1'
if (!(Test-Path -LiteralPath $helper -PathType Leaf)) { throw '找不到 scripts/install-update.ps1。' }

function Assert([bool]$Condition,[string]$Message) { if (!$Condition) { throw $Message } }
function Write-Json([string]$Path,$Value) { [IO.File]::WriteAllText($Path,($Value | ConvertTo-Json -Depth 8),(New-Object Text.UTF8Encoding($false))) }
function Copy-Json($Value) { return ($Value | ConvertTo-Json -Depth 8 | ConvertFrom-Json) }
function Invoke-Helper([string]$Mode,[string]$Plan,[string]$Journal='') {
    $arguments = @('-NoProfile','-NonInteractive','-ExecutionPolicy','Bypass','-File',$helper,'-Mode',$Mode)
    if ($Mode -eq 'Recover') { $arguments += @('-JournalPath',$Journal) } else { $arguments += @('-PlanPath',$Plan) }
    & powershell.exe @arguments
    return $LASTEXITCODE
}
function Invoke-PrepareWithoutGetFileHash([string]$PlanPath,[string]$WrapperPath) {
    $safeHelper = $helper.Replace("'","''")
    $safePlan = $PlanPath.Replace("'","''")
    $source = "function global:Get-FileHash { throw 'Get-FileHash intentionally unavailable' }`r`n& '$safeHelper' -Mode Prepare -PlanPath '$safePlan'`r`nexit `$LASTEXITCODE`r`n"
    [IO.File]::WriteAllText($WrapperPath,$source,(New-Object Text.UTF8Encoding($true)))
    & powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $WrapperPath
    return $LASTEXITCODE
}
function Invoke-InstallWithOwnDialogClosed([string]$PlanPath) {
    if (!('TimeCueUpdateTestNative' -as [type])) {
        Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class TimeCueUpdateTestNative {
  public delegate bool EnumWindowsProc(IntPtr hwnd, IntPtr data);
  [DllImport("user32.dll")] static extern bool EnumWindows(EnumWindowsProc callback, IntPtr data);
  [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr hwnd, out uint pid);
  [DllImport("user32.dll")] static extern bool PostMessage(IntPtr hwnd, uint message, IntPtr wParam, IntPtr lParam);
  public static void CloseOwnedWindows(uint owner) { EnumWindows(delegate(IntPtr hwnd, IntPtr data) { uint pid; GetWindowThreadProcessId(hwnd,out pid); if(pid==owner) PostMessage(hwnd,0x0010,IntPtr.Zero,IntPtr.Zero); return true; },IntPtr.Zero); }
}
'@
    }
    $start=New-Object Diagnostics.ProcessStartInfo
    $start.FileName='powershell.exe';$start.Arguments='-NoProfile -NonInteractive -ExecutionPolicy Bypass -File "'+$helper+'" -Mode Install -PlanPath "'+$PlanPath+'"'
    $start.UseShellExecute=$false;$start.CreateNoWindow=$true;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
    $process=[Diagnostics.Process]::Start($start);$deadline=[DateTime]::UtcNow.AddSeconds(20)
    while (!$process.HasExited -and [DateTime]::UtcNow -lt $deadline) { [TimeCueUpdateTestNative]::CloseOwnedWindows([uint32]$process.Id);Start-Sleep -Milliseconds 100;$process.Refresh() }
    if (!$process.HasExited) { $process.Kill();$process.Dispose();throw '锁定文件失败测试超时。' }
    $null=$process.StandardOutput.ReadToEnd();$null=$process.StandardError.ReadToEnd();$code=$process.ExitCode;$process.Dispose();return $code
}
function Get-Sha([byte[]]$Bytes) { $sha=[Security.Cryptography.SHA256]::Create();try{return ([BitConverter]::ToString($sha.ComputeHash($Bytes))).Replace('-','').ToLowerInvariant()}finally{$sha.Dispose()} }
function New-Bundle([string]$Folder,[string]$Version,[string]$Platform='windows-x64',[string]$UnsafePath='') {
    $rootName = 'TimeCue-' + $Version + '-windows-x64-' + [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss-fffffff')
    $zipPath = Join-Path $Folder ('TimeCue-' + $Version + '-windows-x64-' + [Guid]::NewGuid().ToString('N') + '.zip')
    $payload = [ordered]@{
        'timecue_desktop.exe' = $script:TestDesktopExecutable
        'timecue-core.exe' = [Text.Encoding]::UTF8.GetBytes('owned go bytes')
        'timecue-updater.ps1' = [Text.Encoding]::UTF8.GetBytes('helper bytes')
        'flutter_windows.dll' = [Text.Encoding]::UTF8.GetBytes('flutter dll bytes')
        'native_assets.json' = [Text.Encoding]::UTF8.GetBytes('{"nativeAssets":[]}')
        'data/app.so' = [Text.Encoding]::UTF8.GetBytes('aot bytes')
        'data/icudtl.dat' = [Text.Encoding]::UTF8.GetBytes('icu bytes')
        'data/flutter_assets/assets/app.txt' = [Text.Encoding]::UTF8.GetBytes('asset bytes')
    }
    $manifestFiles = @()
    foreach ($key in $payload.Keys) { $manifestFiles += [ordered]@{ path=$key; sha256=(Get-Sha $payload[$key]) } }
    if ($UnsafePath) { $manifestFiles += [ordered]@{ path=$UnsafePath; sha256=('0' * 64) } }
    $manifest = [ordered]@{ version=$Version; platform=$Platform; files=$manifestFiles }
    $archive = [IO.Compression.ZipFile]::Open($zipPath,[IO.Compression.ZipArchiveMode]::Create)
    try {
        foreach ($key in $payload.Keys) {
            $entry=$archive.CreateEntry($rootName+'/'+$key)
            $stream=$entry.Open();try{$stream.Write($payload[$key],0,$payload[$key].Length)}finally{$stream.Dispose()}
        }
        if ($UnsafePath) { $unsafe=$archive.CreateEntry($rootName+'/'+$UnsafePath); $stream=$unsafe.Open();try{$bytes=[Text.Encoding]::UTF8.GetBytes('escape');$stream.Write($bytes,0,$bytes.Length)}finally{$stream.Dispose()} }
        $entry=$archive.CreateEntry($rootName+'/manifest.json')
        $stream=$entry.Open();try{$bytes=(New-Object Text.UTF8Encoding($false)).GetBytes(($manifest|ConvertTo-Json -Depth 8));$stream.Write($bytes,0,$bytes.Length)}finally{$stream.Dispose()}
    } finally { $archive.Dispose() }
    $zipHash=(Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText(($zipPath+'.sha256'),($zipHash+'  '+[IO.Path]::GetFileName($zipPath)+"`n"),(New-Object Text.ASCIIEncoding))
    return @{ archive=$zipPath; checksum=$zipPath+'.sha256'; root=$rootName; payload=$payload }
}

try {
    Add-Type -AssemblyName System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $tempRoot=Join-Path ([IO.Path]::GetTempPath()) ('TimeCue 更新测试 '+[Guid]::NewGuid().ToString('N'))
    [IO.Directory]::CreateDirectory($tempRoot) | Out-Null
    try {
        $install=Join-Path $tempRoot '程序目录'; $data=Join-Path $tempRoot '用户数据'; $work=Join-Path $tempRoot '工作区'; $stage=Join-Path $work 'stage'; $packages=Join-Path $work 'packages'
        foreach ($path in @($install,$data,$work,$stage,$packages)) { [IO.Directory]::CreateDirectory($path) | Out-Null }
        $fakeAppSource = 'public static class FakeTimeCue { public static void Main() {} }'
        $script:TestDesktopPath = Join-Path $tempRoot 'fake-timecue.exe'
        Add-Type -TypeDefinition $fakeAppSource -Language CSharp -OutputAssembly $script:TestDesktopPath -OutputType WindowsApplication
        $script:TestDesktopExecutable = [IO.File]::ReadAllBytes($script:TestDesktopPath)
        [IO.File]::Copy($script:TestDesktopPath,(Join-Path $install 'timecue_desktop.exe'))
        [IO.File]::WriteAllText((Join-Path $install 'timecue-core.exe'),'旧Go程序')
        $database=Join-Path $data 'timecue.db';[IO.File]::WriteAllText($database,'用户数据库内容')

        $bundle=New-Bundle $packages '2.4.6+17'
        $planPath=Join-Path $work 'plan.json'
        $plan=[ordered]@{schemaVersion=1;archivePath=$bundle.archive;checksumPath=$bundle.checksum;stagingDirectory=$stage;installDirectory=$install;currentVersion='2.4.5+16';targetVersion='2.4.6+17';dataDirectory=$data;parentPid=0;ownedGoPid=0;restartArguments=@('--backend=managed','--data-dir='+$data);statusPath=(Join-Path $data 'software-update-status.json');protectedPaths=@($database,(Join-Path $data 'logs'))}
        Write-Json $planPath $plan
        Write-Output '测试：有效发行包 Prepare'
        Assert ((Invoke-Helper 'Prepare' $planPath) -eq 0) '有效更新包 Prepare 应成功。'
        Assert ((Get-Content -Raw (Join-Path $work 'status.json') | ConvertFrom-Json).state -eq 'prepared') 'Prepare 状态应为 prepared。'
        Assert (Test-Path -LiteralPath (Join-Path $stage 'manifest.json')) '发行 manifest 应进入暂存目录。'
        Assert ((Get-Content -Raw $database) -eq '用户数据库内容') 'Prepare 不得修改用户数据库。'
        $noHashPlan=Copy-Json $plan;$noHashPlan.stagingDirectory=Join-Path $work 'no-hash-stage';[IO.Directory]::CreateDirectory($noHashPlan.stagingDirectory)|Out-Null;$noHashPlanPath=Join-Path $work 'no-hash-plan.json';Write-Json $noHashPlanPath $noHashPlan
        Assert ((Invoke-PrepareWithoutGetFileHash $noHashPlanPath (Join-Path $work 'no-get-filehash.ps1')) -eq 0) 'Prepare 不得依赖 PowerShell Get-FileHash 模块。'
        [IO.File]::WriteAllText((Join-Path $install 'obsolete-plugin.dll'),'旧插件')
        [IO.File]::WriteAllText((Join-Path $install 'native_assets.json'),'旧原生资源')
        Write-Json (Join-Path $install 'manifest.json') ([ordered]@{version='2.4.5+16';platform='windows-x64';files=@([ordered]@{path='obsolete-plugin.dll';sha256=('0'*64)},[ordered]@{path='native_assets.json';sha256=('0'*64)})})

        $sameDirAllowed=Copy-Json $plan;$sameDirAllowed.dataDirectory=$install;$sameDirAllowed.stagingDirectory=(Join-Path $work 'same-dir-stage');$sameDirAllowed.protectedPaths=@();[IO.Directory]::CreateDirectory($sameDirAllowed.stagingDirectory)|Out-Null
        $sameDirPath=Join-Path $work 'same-dir-plan.json';Write-Json $sameDirPath $sameDirAllowed
        Assert ((Invoke-Helper 'Prepare' $sameDirPath) -eq 0) '数据目录等于安装目录时应允许替换发行程序文件。'

        # 同一安装目录可以作为数据目录，但受保护文件不得与任一发行目标重合。
        $sameDirPlan=Copy-Json $plan;$sameDirPlan.dataDirectory=$install;$sameDirPlan.stagingDirectory=(Join-Path $work 'same-stage');$sameDirPlan.protectedPaths=@((Join-Path $install 'timecue-core.exe'))
        [IO.Directory]::CreateDirectory($sameDirPlan.stagingDirectory)|Out-Null
        $samePlanPath=Join-Path $work 'same-plan.json';Write-Json $samePlanPath $sameDirPlan
        Write-Output '测试：自定义受保护路径冲突'
        Assert ((Invoke-Helper 'Prepare' $samePlanPath) -ne 0) '受保护程序/数据路径重合必须拒绝。'
        Assert ((Get-Content -Raw (Join-Path $install 'timecue-core.exe')) -eq '旧Go程序') '冲突预检失败后旧程序必须保持。'
        $protectedDirectoryPlan=Copy-Json $plan;$protectedDirectoryPlan.protectedPaths=@((Join-Path $install 'data'));$protectedDirectoryPlan.stagingDirectory=Join-Path $work 'protected-directory-stage';[IO.Directory]::CreateDirectory($protectedDirectoryPlan.stagingDirectory)|Out-Null;$protectedDirectoryPath=Join-Path $work 'protected-directory-plan.json';Write-Json $protectedDirectoryPath $protectedDirectoryPlan
        Assert ((Invoke-Helper 'Prepare' $protectedDirectoryPath) -ne 0) '尚不存在的受保护目录也必须阻止发行文件落入其中。'

        # Cancel marker 必须阻止 Install 修改程序目录。
        [IO.File]::WriteAllText((Join-Path $work 'cancel.marker'),'cancel')
        Write-Output '测试：取消提交握手'
        Assert ((Invoke-Helper 'Install' $planPath) -eq 0) '取消握手应正常结束。'
        Assert ((Get-Content -Raw (Join-Path $install 'timecue-core.exe')) -eq '旧Go程序') '取消后不得替换程序文件。'
        Assert ((Get-Content -Raw (Join-Path $work 'status.json') | ConvertFrom-Json).state -eq 'cancelled') '取消状态应可读取。'
        Remove-Item -LiteralPath (Join-Path $work 'cancel.marker') -Force

        # 成功替换后启动新程序，再显式恢复事务日志验证原文件和用户数据。
        [IO.File]::WriteAllText((Join-Path $work 'commit.marker'),'commit')
        Assert ((Invoke-Helper 'Install' $planPath) -eq 0) '有效安装和程序重启应成功。'
        Assert ((Get-Content -Raw (Join-Path $install 'timecue-core.exe')) -eq 'owned go bytes') 'Install 应替换已知 Go 程序文件。'
        Assert ((Get-Content -Raw (Join-Path $install 'native_assets.json')) -eq '{"nativeAssets":[]}') '根目录 native_assets.json 应允许随发行包替换。'
        Assert (!(Test-Path -LiteralPath (Join-Path $install 'obsolete-plugin.dll'))) 'Install 应移除旧 manifest 明确列出的过期程序文件。'
        Assert ((Get-Content -Raw $database) -eq '用户数据库内容') 'Install 不得修改用户数据库。'
        Assert ((Get-Content -Raw (Join-Path $work 'status.json') | ConvertFrom-Json).state -eq 'installed') 'Install 状态应为 installed。'
        Assert ((Invoke-Helper 'Recover' $null (Join-Path $work 'journal.json')) -eq 0) '成功安装后的事务日志恢复应可运行。'
        Assert ((Get-Content -Raw (Join-Path $install 'timecue-core.exe')) -eq '旧Go程序') 'Recover 应恢复安装前程序。'
        Assert ((Get-Content -Raw (Join-Path $install 'obsolete-plugin.dll')) -eq '旧插件') 'Recover 应还原旧 manifest 列出的程序文件。'
        Assert ((Get-Content -Raw (Join-Path $install 'native_assets.json')) -eq '旧原生资源') 'Recover 应还原更新前的根目录 native_assets.json。'
        Remove-Item -LiteralPath (Join-Path $work 'commit.marker') -Force

        # 锁住目标 Go 文件，强制事务在替换中途失败，再验证自动回滚日志。
        [IO.File]::WriteAllText((Join-Path $work 'commit.marker'),'commit')
        $fileLock=[IO.File]::Open((Join-Path $install 'timecue-core.exe'),[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read)
        try { Assert ((Invoke-InstallWithOwnDialogClosed $planPath) -ne 0) '锁定文件导致的事务应返回失败。' }
        finally { $fileLock.Dispose() }
        Assert ((Get-Content -Raw (Join-Path $install 'timecue-core.exe')) -eq '旧Go程序') '锁定文件失败后必须还原旧程序。'
        Assert ((Get-Content -Raw (Join-Path $work 'status.json') | ConvertFrom-Json).state -eq 'rolledBack') '失败回滚应持久报告 rolledBack。'
        Assert ((Get-Content -Raw $database) -eq '用户数据库内容') '失败回滚不得修改用户数据库。'
        Remove-Item -LiteralPath (Join-Path $work 'commit.marker') -Force

        # 校验损坏包、版本/平台不匹配和 ZIP 路径逃逸均在触及安装目录前失败。
        $bad=New-Bundle $packages '2.4.6+17';$badPlan=Copy-Json $plan;$badPlan.archivePath=$bad.archive;$badPlan.checksumPath=$bad.checksum;$badPlan.stagingDirectory=Join-Path $work 'bad-stage';[IO.Directory]::CreateDirectory($badPlan.stagingDirectory)|Out-Null;$badPath=Join-Path $work 'bad-plan.json';Write-Json $badPath $badPlan
        Write-Output '测试：ZIP损坏'
        $bytes=[IO.File]::ReadAllBytes($bad.archive);$bytes[20]=($bytes[20] -bxor 1);[IO.File]::WriteAllBytes($bad.archive,$bytes)
        Assert ((Invoke-Helper 'Prepare' $badPath) -ne 0) '损坏 ZIP 必须拒绝。'
        $versionBad=New-Bundle $packages '2.4.6+18';$versionPlan=Copy-Json $plan;$versionPlan.archivePath=$versionBad.archive;$versionPlan.checksumPath=$versionBad.checksum;$versionPlan.stagingDirectory=Join-Path $work 'version-stage';$versionPlan.targetVersion='2.4.6+17';[IO.Directory]::CreateDirectory($versionPlan.stagingDirectory)|Out-Null;$versionPath=Join-Path $work 'version-plan.json';Write-Json $versionPath $versionPlan
        Write-Output '测试：版本不匹配'
        Assert ((Invoke-Helper 'Prepare' $versionPath) -ne 0) '版本不匹配必须拒绝。'
        $sameVersion=Copy-Json $plan;$sameVersion.targetVersion=$sameVersion.currentVersion;$sameVersion.stagingDirectory=Join-Path $work 'same-version-stage';[IO.Directory]::CreateDirectory($sameVersion.stagingDirectory)|Out-Null;$sameVersionPath=Join-Path $work 'same-version-plan.json';Write-Json $sameVersionPath $sameVersion
        Assert ((Invoke-Helper 'Prepare' $sameVersionPath) -ne 0) '目标版本等于当前版本时必须拒绝。'
        $olderVersion=Copy-Json $plan;$olderVersion.targetVersion='2.4.4+99';$olderVersion.stagingDirectory=Join-Path $work 'older-version-stage';[IO.Directory]::CreateDirectory($olderVersion.stagingDirectory)|Out-Null;$olderVersionPath=Join-Path $work 'older-version-plan.json';Write-Json $olderVersionPath $olderVersion
        Assert ((Invoke-Helper 'Prepare' $olderVersionPath) -ne 0) '低于当前版本的目标必须拒绝。'
        $platformBad=New-Bundle $packages '2.4.6+17' 'linux-x64';$platformPlan=Copy-Json $plan;$platformPlan.archivePath=$platformBad.archive;$platformPlan.checksumPath=$platformBad.checksum;$platformPlan.stagingDirectory=Join-Path $work 'platform-stage';[IO.Directory]::CreateDirectory($platformPlan.stagingDirectory)|Out-Null;$platformPath=Join-Path $work 'platform-plan.json';Write-Json $platformPath $platformPlan
        Write-Output '测试：平台不匹配'
        Assert ((Invoke-Helper 'Prepare' $platformPath) -ne 0) '平台不匹配必须拒绝。'
        $escape=New-Bundle $packages '2.4.6+17' 'windows-x64' '../outside.dll';$escapePlan=Copy-Json $plan;$escapePlan.archivePath=$escape.archive;$escapePlan.checksumPath=$escape.checksum;$escapePlan.stagingDirectory=Join-Path $work 'escape-stage';[IO.Directory]::CreateDirectory($escapePlan.stagingDirectory)|Out-Null;$escapePath=Join-Path $work 'escape-plan.json';Write-Json $escapePath $escapePlan
        Write-Output '测试：ZIP路径逃逸'
        Assert ((Invoke-Helper 'Prepare' $escapePath) -ne 0) 'ZIP 路径逃逸必须拒绝。'
        $reserved=New-Bundle $packages '2.4.6+17' 'windows-x64' 'data/CON.txt';$reservedPlan=Copy-Json $plan;$reservedPlan.archivePath=$reserved.archive;$reservedPlan.checksumPath=$reserved.checksum;$reservedPlan.stagingDirectory=Join-Path $work 'reserved-stage';[IO.Directory]::CreateDirectory($reservedPlan.stagingDirectory)|Out-Null;$reservedPath=Join-Path $work 'reserved-plan.json';Write-Json $reservedPath $reservedPlan
        Assert ((Invoke-Helper 'Prepare' $reservedPath) -ne 0) 'Windows 保留文件名必须拒绝。'

        # 模拟中断后的“旧文件已备份，新文件已写入”，Recover 只回滚发行清单列出的程序文件。
        $backup=Join-Path $install '.timecue-update-backup-test';[IO.Directory]::CreateDirectory($backup)|Out-Null
        [IO.File]::WriteAllText((Join-Path $backup 'timecue-core.exe'),'旧Go程序')
        [IO.File]::WriteAllText((Join-Path $install 'timecue-core.exe'),'新Go程序')
        $recovery=[ordered]@{schemaVersion=1;installDirectory=$install;backupDirectory=$backup;journalPath=(Join-Path $work 'journal.json');entries=@([ordered]@{path='timecue-core.exe';hadFile=$true;started=$true;sha256=(Get-Sha ([Text.Encoding]::UTF8.GetBytes('新Go程序')));tempPath='';newFile=$true})}
        Write-Output '测试：中断事务恢复'
        Write-Json $recovery.journalPath $recovery
        Assert ((Invoke-Helper 'Recover' $null $recovery.journalPath) -eq 0) '中断安装恢复应成功。'
        Assert ((Get-Content -Raw (Join-Path $install 'timecue-core.exe')) -eq '旧Go程序') 'Recover 应恢复旧程序。'
        Assert ((Get-Content -Raw $database) -eq '用户数据库内容') 'Recover 不得回滚或改写用户数据库。'
        Write-Output '更新器安全、取消、事务恢复和用户数据隔离测试通过。'
    } finally {
        $verifiedRoot = [IO.Path]::GetFullPath($tempRoot)
        $expectedParent = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\')
        if ([IO.Path]::GetDirectoryName($verifiedRoot).TrimEnd('\') -ieq $expectedParent -and [IO.Path]::GetFileName($verifiedRoot) -match '^TimeCue 更新测试 [0-9a-f]{32}$') {
            if (Test-Path -LiteralPath $verifiedRoot) { Remove-Item -LiteralPath $verifiedRoot -Recurse -Force }
        } else { throw "拒绝清理未经验证的临时路径：$verifiedRoot" }
    }
} catch { Write-Error $_; exit 1 }
