param(
    [Parameter(Mandatory = $true)][ValidateSet('Prepare','Install','Recover')][string]$Mode,
    [string]$PlanPath,
    [string]$JournalPath
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2
Add-Type -AssemblyName System.IO.Compression.FileSystem

$script:MaxExpandedBytes = 1GB
$script:VersionPattern = '^\d+\.\d+\.\d+\+\d+$'
$script:TimestampPattern = '^\d{8}-\d{6}-\d{7}$'
$script:FinalState = ''

function Get-FullPath([string]$Path) {
    $full = [IO.Path]::GetFullPath($Path)
    $root = [IO.Path]::GetPathRoot($full)
    if ([string]::Equals($full,$root,[StringComparison]::OrdinalIgnoreCase)) { return $root }
    return $full.TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
}

function Test-SamePath([string]$Left, [string]$Right) {
    return [string]::Equals((Get-FullPath $Left), (Get-FullPath $Right), [StringComparison]::OrdinalIgnoreCase)
}

function Test-ChildPath([string]$Path, [string]$Parent) {
    $child = Get-FullPath $Path
    $parent = Get-FullPath $Parent
    if ($child.Length -le $parent.Length) { return $false }
    $prefix = $parent
    if (!$prefix.EndsWith([IO.Path]::DirectorySeparatorChar) -and !$prefix.EndsWith([IO.Path]::AltDirectorySeparatorChar)) { $prefix += [IO.Path]::DirectorySeparatorChar }
    return $child.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)
}

function Move-AtomicFile([string]$Source,[string]$Destination) {
    if (Test-Path -LiteralPath $Destination) {
        $backup = $Destination + '.replace-backup-' + [Guid]::NewGuid().ToString('N')
        [IO.File]::Replace($Source,$Destination,$backup)
        if (Test-Path -LiteralPath $backup) { [IO.File]::Delete($backup) }
    } else { [IO.File]::Move($Source,$Destination) }
}

function Test-WindowsSegment([string]$Segment) {
    if ([string]::IsNullOrWhiteSpace($Segment) -or $Segment -in @('.', '..')) { return $false }
    if ($Segment -match '[<>:"/\\|?*]' -or $Segment.EndsWith('.') -or $Segment.EndsWith(' ')) { return $false }
    $base = ($Segment.Split('.')[0]).ToUpperInvariant()
    if ($base -in @('CON','PRN','AUX','NUL')) { return $false }
    if ($base -match '^(COM|LPT)[1-9]$') { return $false }
    return $true
}

function ConvertTo-SafeRelative([string]$RelativePath) {
    if ([string]::IsNullOrWhiteSpace($RelativePath) -or $RelativePath.StartsWith('/') -or $RelativePath -match '^[A-Za-z]:') {
        throw "更新包包含绝对路径：$RelativePath"
    }
    $parts = $RelativePath.Replace('\','/').Split('/')
    foreach ($part in $parts) { if (!(Test-WindowsSegment $part)) { throw "更新包路径不安全：$RelativePath" } }
    return [string]::Join('/', $parts)
}

function Assert-NoReparsePath([string]$Path) {
    $full = Get-FullPath $Path
    $root = [IO.Path]::GetPathRoot($full)
    $current = $root
    $rest = $full.Substring($root.Length)
    foreach ($part in $rest.Split([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)) {
        if (!$part) { continue }
        $current = Join-Path $current $part
        if (Test-Path -LiteralPath $current) {
            $item = Get-Item -LiteralPath $current -Force
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw "路径包含重解析点，已拒绝操作：$current" }
        }
    }
}

function Get-StatusPath([string]$Path, $Plan) {
    if ($Plan.statusPath -and [string]$Plan.statusPath) { return Get-FullPath ([string]$Plan.statusPath) }
    return Join-Path (Split-Path -Parent (Get-FullPath $Path)) 'status.json'
}

function Write-Status([string]$Path, $Plan, [string]$State, [string]$Message, [string]$Journal = '') {
	$script:FinalState = $State
	$status = [ordered]@{ state = $State; message = $Message; targetVersion = [string]$Plan.targetVersion; currentVersion = if ($State -eq 'installed') { [string]$Plan.targetVersion } else { [string]$Plan.currentVersion }; updatedAt = [DateTime]::UtcNow.ToString('o') }
    if ($Journal) { $status.journalPath = $Journal }
    $json = $status | ConvertTo-Json -Depth 5
    $destinations = @((Get-StatusPath $Path $Plan), (Join-Path (Split-Path -Parent (Get-FullPath $Path)) 'status.json')) | Select-Object -Unique
    foreach ($destination in $destinations) {
        $parent = Split-Path -Parent $destination
        [IO.Directory]::CreateDirectory($parent) | Out-Null
        Assert-NoReparsePath $parent
        $temporary = $destination + '.tmp'
        [IO.File]::WriteAllText($temporary, $json, (New-Object Text.UTF8Encoding($false)))
        Move-AtomicFile $temporary $destination
    }
}

function Read-Plan([string]$Path) {
    if (!(Test-Path -LiteralPath $Path -PathType Leaf)) { throw '找不到更新计划文件。' }
    $plan = [IO.File]::ReadAllText((Get-FullPath $Path)) | ConvertFrom-Json
    if ($plan.schemaVersion -ne 1) { throw '不支持的更新计划版本。' }
    foreach ($field in @('archivePath','checksumPath','stagingDirectory','installDirectory','dataDirectory','targetVersion')) {
        if ([string]::IsNullOrWhiteSpace([string]$plan.$field)) { throw "更新计划缺少字段：$field" }
    }
    if ([string]$plan.targetVersion -notmatch $script:VersionPattern) { throw '目标版本格式无效。' }
    if ([string]$plan.currentVersion -notmatch $script:VersionPattern) { throw '当前版本格式无效。' }
    $currentParts = ([string]$plan.currentVersion).Split([char[]]@('.','+'))
    $targetParts = ([string]$plan.targetVersion).Split([char[]]@('.','+'))
    for ($versionIndex=0; $versionIndex -lt 4; $versionIndex++) {
        [Numerics.BigInteger]$currentValue = 0; [Numerics.BigInteger]$targetValue = 0
        if (![Numerics.BigInteger]::TryParse($currentParts[$versionIndex],[ref]$currentValue) -or ![Numerics.BigInteger]::TryParse($targetParts[$versionIndex],[ref]$targetValue)) { throw '版本号超出支持范围。' }
        if ($targetValue -gt $currentValue) { break }
        if ($targetValue -lt $currentValue) { throw '发行版本不高于当前版本。' }
        if ($versionIndex -eq 3) { throw '目标版本与当前版本相同。' }
    }
    foreach ($pathValue in @($plan.archivePath,$plan.checksumPath,$plan.stagingDirectory,$plan.installDirectory,$plan.dataDirectory)) {
        if (![IO.Path]::IsPathRooted([string]$pathValue)) { throw '更新计划中的路径必须是绝对路径。' }
    }
    if ($plan.statusPath -and ![IO.Path]::IsPathRooted([string]$plan.statusPath)) { throw 'statusPath 必须是绝对路径。' }
    foreach ($field in @('statusPath','protectedPaths','parentPid','ownedGoPid','restartArguments')) {
        if (!$plan.PSObject.Properties[$field]) { $plan | Add-Member -NotePropertyName $field -NotePropertyValue $null }
    }
    foreach ($protected in @($plan.protectedPaths)) { if (![IO.Path]::IsPathRooted([string]$protected)) { throw '受保护路径必须是绝对路径。' } }
    return $plan
}

function Test-AllowedReleaseFile([string]$RelativePath) {
    $name = $RelativePath.Replace('\','/').ToLowerInvariant()
    $base = [IO.Path]::GetFileName($name)
    if ($name -match '(^|/)(config[^/]*|.*\.(db|sqlite|sqlite3|log|yaml|yml)|.*database.*)$' -or $name -match '(^|/)logs?(/|$)') { return $false }
    if ($name -in @('data/app.so','data/icudtl.dat','data/native_assets.json')) { return $true }
    if ($name.StartsWith('data/flutter_assets/')) { return $true }
    if ($name.Contains('/')) { return $false }
    return $base -in @('readme.md','timecue-updater.ps1','native_assets.json') -or $base.EndsWith('.exe') -or $base.EndsWith('.dll')
}

function Get-EntryHash($Entry) {
    $stream = $Entry.Open()
    try { $sha = [Security.Cryptography.SHA256]::Create(); try { return ([BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-','').ToLowerInvariant() } finally { $sha.Dispose() } }
    finally { $stream.Dispose() }
}

function Get-FileHashHex([string]$Path) {
    $stream = [IO.File]::OpenRead($Path)
    try { $sha = [Security.Cryptography.SHA256]::Create(); try { return ([BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-','').ToLowerInvariant() } finally { $sha.Dispose() } }
    finally { $stream.Dispose() }
}

function Get-ArchiveManifest($Archive, [string]$Root) {
    $manifestEntry = $Archive.Entries | Where-Object { $_.FullName.Replace('\','/') -ieq ($Root + '/manifest.json') }
    if (@($manifestEntry).Count -ne 1) { throw '更新包缺少根目录 manifest.json。' }
    if ($manifestEntry.Length -gt 4MB) { throw '发行清单超过大小限制。' }
    $reader = New-Object IO.StreamReader($manifestEntry.Open(), [Text.Encoding]::UTF8, $true)
    try { return $reader.ReadToEnd() | ConvertFrom-Json } finally { $reader.Dispose() }
}

function Test-Checksum($Plan) {
    $lines = [IO.File]::ReadAllLines([string]$Plan.checksumPath)
    if ($lines.Count -lt 1 -or $lines[0] -notmatch '^([A-Fa-f0-9]{64})\s+\*?(.+)$') { throw 'ZIP SHA-256 文件格式无效。' }
    if (![string]::Equals($Matches[2].Trim(), [IO.Path]::GetFileName([string]$Plan.archivePath), [StringComparison]::Ordinal)) { throw 'ZIP SHA-256 文件名不匹配。' }
    $actual = Get-FileHashHex ([string]$Plan.archivePath)
    if ($actual -ne $Matches[1].ToLowerInvariant()) { throw '更新包 SHA-256 校验失败。' }
}

function Test-Package($Plan) {
    Test-Checksum $Plan
    $archive = [IO.Compression.ZipFile]::OpenRead([string]$Plan.archivePath)
    try {
        if ($archive.Entries.Count -eq 0) { throw '更新包为空。' }
        $seen = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
        $roots = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
        [long]$expanded = 0
        foreach ($entry in $archive.Entries) {
            $entryName = $entry.FullName.Replace('\','/')
            if ($entryName.StartsWith('/') -or $entryName -match '^[A-Za-z]:') { throw "更新包路径不安全：$entryName" }
            $parts = $entryName.TrimEnd('/').Split('/')
            if ($parts.Count -eq 1 -and $entryName.EndsWith('/')) {
                $rootPrefix = 'TimeCue-' + [string]$Plan.targetVersion + '-windows-x64-'
                $rootStamp = $parts[0].Substring([Math]::Min($rootPrefix.Length,$parts[0].Length))
                if (!$parts[0].StartsWith($rootPrefix,[StringComparison]::Ordinal) -or $rootStamp -notmatch $script:TimestampPattern) { throw '发行目录名称与目标版本不匹配。' }
                [void]$roots.Add($parts[0]); continue
            }
            if ($parts.Count -lt 2) { throw '更新包必须只包含一个顶层发行目录。' }
            $prefix = 'TimeCue-' + [string]$Plan.targetVersion + '-windows-x64-'
            $stamp = $parts[0].Substring([Math]::Min($prefix.Length,$parts[0].Length))
            if (!$parts[0].StartsWith($prefix,[StringComparison]::Ordinal) -or $stamp -notmatch $script:TimestampPattern) { throw '发行目录名称与目标版本不匹配。' }
            [void]$roots.Add($parts[0])
            foreach ($part in $parts[1..($parts.Count-1)]) { if (!(Test-WindowsSegment $part)) { throw "更新包包含不安全路径：$entryName" } }
            $normalized = $parts -join '/'
            if (!$seen.Add($normalized)) { throw '更新包存在重复或大小写冲突路径。' }
            $attributes = [long]$entry.ExternalAttributes
            $unixType = ($attributes -shr 16) -band 0xF000
            if ($unixType -eq 0xA000 -or ($attributes -band 0x400) -ne 0) { throw '更新包包含链接或重解析点。' }
            if (!$entryName.EndsWith('/')) { $expanded += $entry.Length; if ($expanded -gt $script:MaxExpandedBytes) { throw '更新包解压后超过大小限制。' } }
        }
        if ($roots.Count -ne 1) { throw '更新包必须只包含一个顶层发行目录。' }
        $root = @($roots)[0]
        $manifest = Get-ArchiveManifest $archive $root
        if ($manifest.platform -ne 'windows-x64' -or $manifest.version -ne $Plan.targetVersion -or [string]$manifest.version -notmatch $script:VersionPattern) { throw '发行清单平台或版本不匹配。' }
        $listed = New-Object 'System.Collections.Generic.Dictionary[string,string]' ([StringComparer]::OrdinalIgnoreCase)
        foreach ($item in @($manifest.files)) {
            $rel = ConvertTo-SafeRelative ([string]$item.path)
            if ([string]$item.sha256 -notmatch '^[A-Fa-f0-9]{64}$') { throw '发行清单包含无效 SHA-256。' }
            if ($listed.ContainsKey($rel)) { throw '发行清单包含重复文件路径。' }
            if (!(Test-AllowedReleaseFile $rel)) { throw "发行清单包含不允许的文件：$rel" }
            $listed.Add($rel,[string]$item.sha256)
        }
        $actual = New-Object 'System.Collections.Generic.Dictionary[string,object]' ([StringComparer]::OrdinalIgnoreCase)
        foreach ($entry in $archive.Entries) {
            if ($entry.FullName.Replace('\','/').EndsWith('/')) { continue }
            $relative = $entry.FullName.Replace('\','/').Substring($root.Length + 1)
            if ($relative -ieq 'manifest.json') { continue }
            $relative = ConvertTo-SafeRelative $relative
            if (!(Test-AllowedReleaseFile $relative)) { throw "发行包包含不允许的文件：$relative" }
            if (!$listed.ContainsKey($relative)) { throw "发行清单缺少文件：$relative" }
            if ($actual.ContainsKey($relative)) { throw '发行包存在大小写冲突。' }
            $actual.Add($relative,$entry)
            if ((Get-EntryHash $entry) -ne $listed[$relative].ToLowerInvariant()) { throw "发行文件校验失败：$relative" }
        }
        if ($actual.Count -ne $listed.Count) { throw '发行清单包含包内不存在的文件。' }
        foreach ($required in @('timecue_desktop.exe','timecue-core.exe','timecue-updater.ps1','flutter_windows.dll','data/app.so','data/icudtl.dat')) {
            if (!$actual.ContainsKey($required)) { throw "发行包缺少必需文件：$required；发现文件：$($actual.Keys -join ',')" }
        }
        $manifestHash = (Get-EntryHash ($archive.Entries | Where-Object { $_.FullName.Replace('\','/') -ieq ($root + '/manifest.json') }))
        $files = [ordered]@{}
        foreach ($key in $listed.Keys) { $files[$key] = $listed[$key].ToLowerInvariant() }
        $files['manifest.json'] = $manifestHash
        return @{ archive = $archive; root = $root; manifest = $manifest; files = $files; expandedBytes = $expanded }
    } catch { $archive.Dispose(); throw }
}

function Assert-NoProtectedTarget($Plan, [string[]]$RelativePaths) {
    foreach ($relative in $RelativePaths) {
        $target = [IO.Path]::GetFullPath((Join-Path ([string]$Plan.installDirectory) $relative))
        foreach ($protectedRaw in @($Plan.protectedPaths)) {
            if (!$protectedRaw) { continue }
            $protected = Get-FullPath ([string]$protectedRaw)
            if (Test-SamePath $target $protected) { throw "更新文件与用户数据路径冲突：$protected" }
            if (Test-ChildPath $protected $target) { throw "更新文件会覆盖受保护路径：$protected" }
            $protectedIsDirectory = (Test-Path -LiteralPath $protected -PathType Container) -or [string]::IsNullOrEmpty([IO.Path]::GetExtension($protected))
            if ($protectedIsDirectory -and (Test-ChildPath $target $protected)) { throw "更新目标落入受保护目录：$protected" }
        }
        if ($Plan.statusPath -and (Test-SamePath $target ([string]$Plan.statusPath))) { throw '安装路径与更新状态文件冲突。' }
    }
}

function Get-PreparedPath([string]$Path) { return Join-Path (Split-Path -Parent (Get-FullPath $Path)) 'prepared.json' }

function Write-Prepared([string]$Path, $Plan, [string]$Version, $Files) {
    $record = [ordered]@{ schemaVersion = 1; version = $Version; files = $Files }
    $destination = Get-PreparedPath $Path
    $temp = $destination + '.tmp'
    [IO.File]::WriteAllText($temp, ($record | ConvertTo-Json -Depth 8), (New-Object Text.UTF8Encoding($false)))
    Move-AtomicFile $temp $destination
}

function Invoke-Prepare([string]$Path, $Plan) {
    $package = Test-Package $Plan
    try {
        $stage = Get-FullPath ([string]$Plan.stagingDirectory)
        $install = Get-FullPath ([string]$Plan.installDirectory)
        $data = Get-FullPath ([string]$Plan.dataDirectory)
        if ((Test-SamePath $stage $install) -or (Test-SamePath $stage $data) -or (Test-ChildPath $stage $data) -or (Test-ChildPath $data $stage)) { throw '暂存目录与安装目录或用户数据目录冲突。' }
        Assert-NoReparsePath $stage
        if (Test-Path -LiteralPath $stage) {
            if (@(Get-ChildItem -LiteralPath $stage -Force).Count -gt 0) { throw '暂存目录必须是新建的空目录。' }
        } else { [IO.Directory]::CreateDirectory($stage) | Out-Null }
        Assert-NoReparsePath $install
        [IO.Directory]::CreateDirectory($install) | Out-Null
        Assert-NoProtectedTarget $Plan @($package.files.Keys)
        $probe = Join-Path $install ('.timecue-update-probe-' + [Guid]::NewGuid().ToString('N'))
        try { $stream = [IO.File]::Open($probe,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None); $stream.Dispose() } finally { if (Test-Path -LiteralPath $probe) { [IO.File]::Delete($probe) } }
        $drive = New-Object IO.DriveInfo([IO.Path]::GetPathRoot($install))
        if ($drive.AvailableFreeSpace -lt [long]$package.expandedBytes) { throw '安装磁盘空间不足，旧版本将保持可用。' }
        foreach ($entry in $package.archive.Entries) {
            if ($entry.FullName.Replace('\','/').EndsWith('/')) { continue }
            $relative = $entry.FullName.Replace('\','/').Substring($package.root.Length + 1)
            $safe = ConvertTo-SafeRelative $relative
            $destination = [IO.Path]::GetFullPath((Join-Path $stage $safe))
            if (!(Test-ChildPath $destination $stage)) { throw '解包路径越出暂存目录。' }
            [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($destination)) | Out-Null
            $inputStream = $entry.Open()
            try {
                $output = [IO.File]::Open($destination,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
                try { $inputStream.CopyTo($output); $output.Flush($true) } finally { $output.Dispose() }
            } finally { $inputStream.Dispose() }
        }
        Write-Prepared $Path $Plan ([string]$package.manifest.version) $package.files
        Write-Status $Path $Plan 'prepared' '更新包验证及准备完成。'
    } finally { if ($package.archive) { $package.archive.Dispose() } }
}

function Get-OldManifestPaths([string]$InstallDirectory) {
    $manifestPath = Join-Path $InstallDirectory 'manifest.json'
    if (!(Test-Path -LiteralPath $manifestPath -PathType Leaf)) { return @() }
    try {
        $old = [IO.File]::ReadAllText($manifestPath) | ConvertFrom-Json
        $paths = @()
        foreach ($item in @($old.files)) {
            try { $relative = ConvertTo-SafeRelative ([string]$item.path); if (Test-AllowedReleaseFile $relative) { $paths += $relative } } catch { }
        }
        return $paths
    } catch { return @() }
}

function Write-Journal($Journal) {
    $path = [string]$Journal.journalPath
    $bytes = (New-Object Text.UTF8Encoding($false)).GetBytes(($Journal | ConvertTo-Json -Depth 10))
    $temp = $path + '.tmp'
    $stream = [IO.File]::Open($temp,[IO.FileMode]::Create,[IO.FileAccess]::Write,[IO.FileShare]::None)
    try { $stream.Write($bytes,0,$bytes.Length); $stream.Flush($true) } finally { $stream.Dispose() }
    Move-AtomicFile $temp $path
}

function Invoke-Recover([string]$Path) {
    if (!(Test-Path -LiteralPath $Path -PathType Leaf)) { throw '找不到安装恢复日志。' }
    $journal = [IO.File]::ReadAllText((Get-FullPath $Path)) | ConvertFrom-Json
    if ($journal.schemaVersion -ne 1) { throw '安装恢复日志版本无效。' }
    if (!$journal.PSObject.Properties['protectedPaths']) { $journal | Add-Member -NotePropertyName protectedPaths -NotePropertyValue @() }
    $install = Get-FullPath ([string]$journal.installDirectory)
    $backup = Get-FullPath ([string]$journal.backupDirectory)
    if (!(Test-ChildPath $backup $install) -or !(Split-Path -Leaf $backup).StartsWith('.timecue-update-backup-')) { throw '恢复日志备份路径不安全。' }
    Assert-NoReparsePath $install
    for ($index = @($journal.entries).Count - 1; $index -ge 0; $index--) {
        $entry = $journal.entries[$index]
        $relative = ConvertTo-SafeRelative ([string]$entry.path)
        if ($relative -ne 'manifest.json' -and !(Test-AllowedReleaseFile $relative)) { throw "恢复日志包含不允许的程序路径：$relative" }
        $destination = [IO.Path]::GetFullPath((Join-Path $install $relative))
        if (!(Test-ChildPath $destination $install)) { throw '恢复路径越出安装目录。' }
        Assert-NoReparsePath ([IO.Path]::GetDirectoryName($destination))
        foreach ($protected in @($journal.protectedPaths)) {
            if ((Test-SamePath $destination ([string]$protected)) -or (Test-ChildPath ([string]$protected) $destination)) { throw "恢复路径与用户数据路径冲突：$protected" }
            $protectedIsDirectory = (Test-Path -LiteralPath $protected -PathType Container) -or [string]::IsNullOrEmpty([IO.Path]::GetExtension([string]$protected))
            if ($protectedIsDirectory -and (Test-ChildPath $destination ([string]$protected))) { throw "恢复路径落入受保护目录：$protected" }
        }
        if (Test-Path -LiteralPath $destination) { Assert-NoReparsePath $destination }
        if ($entry.tempPath) { $temp = [IO.Path]::GetFullPath((Join-Path $install ([string]$entry.tempPath))); if ((Test-ChildPath $temp $install) -and (Test-Path -LiteralPath $temp)) { [IO.File]::Delete($temp) } }
        if (!$entry.started) { continue }
        $oldPath = [IO.Path]::GetFullPath((Join-Path $backup $relative))
        if (!(Test-ChildPath $oldPath $backup)) { throw '恢复备份路径越界。' }
        Assert-NoReparsePath ([IO.Path]::GetDirectoryName($oldPath))
        if ($entry.hadFile -and (Test-Path -LiteralPath $oldPath -PathType Leaf)) {
            if (Test-Path -LiteralPath $destination) { [IO.File]::Delete($destination) }
            [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($destination)) | Out-Null
            [IO.File]::Move($oldPath,$destination)
        } elseif (!$entry.hadFile -and (Test-Path -LiteralPath $destination -PathType Leaf) -and (Get-FileHashHex $destination) -eq [string]$entry.sha256) {
            [IO.File]::Delete($destination)
        }
    }
}

function Add-Win32Types {
    if ('TimeCueUpdateNative' -as [type]) { return }
    Add-Type @'
using System;
using System.Text;
using System.Runtime.InteropServices;
public static class TimeCueUpdateNative {
  [DllImport("kernel32.dll", SetLastError=true)] public static extern IntPtr OpenProcess(uint access, bool inherit, uint pid);
  [DllImport("kernel32.dll", SetLastError=true)] public static extern bool CloseHandle(IntPtr handle);
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)] public static extern bool QueryFullProcessImageName(IntPtr handle, uint flags, StringBuilder name, ref uint size);
  [DllImport("kernel32.dll", SetLastError=true)] public static extern uint WaitForSingleObject(IntPtr handle, uint milliseconds);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int MessageBox(IntPtr hwnd, string text, string caption, uint type);
}
'@
}

function Open-ExpectedProcess([uint32]$taskProcessId, [string]$ExpectedImage) {
    if ($taskProcessId -eq 0 -or $taskProcessId -eq $PID) { return [IntPtr]::Zero }
    Add-Win32Types
    $handle = [TimeCueUpdateNative]::OpenProcess(0x00100000 -bor 0x1000, $false, $taskProcessId)
    if ($handle -eq [IntPtr]::Zero) {
        $code = [Runtime.InteropServices.Marshal]::GetLastWin32Error()
        if ($code -eq 87) { return [IntPtr]::Zero }
        throw "无法绑定进程 $taskProcessId：Win32错误 $code"
    }
    $name = New-Object Text.StringBuilder(32768); [uint32]$length = $name.Capacity
    if (![TimeCueUpdateNative]::QueryFullProcessImageName($handle,0,$name,[ref]$length)) { [void][TimeCueUpdateNative]::CloseHandle($handle); throw "无法校验进程 $taskProcessId 的程序路径。" }
    if (!(Test-SamePath $name.ToString() $ExpectedImage)) { [void][TimeCueUpdateNative]::CloseHandle($handle); throw "进程 $taskProcessId 与预期的 TimeCue 程序不匹配。" }
    return $handle
}

function Wait-ForDecisionAndExit([string]$Path, $Plan) {
    $handles = @()
    try {
        $handles += Open-ExpectedProcess ([uint32]$Plan.parentPid) (Join-Path ([string]$Plan.installDirectory) 'timecue_desktop.exe')
        $handles += Open-ExpectedProcess ([uint32]$Plan.ownedGoPid) (Join-Path ([string]$Plan.installDirectory) 'timecue-core.exe')
        $handles = @($handles | Where-Object { $_ -ne [IntPtr]::Zero })
        Write-Status $Path $Plan 'ready' '安装器已就绪，等待退出确认。'
        $folder = Split-Path -Parent (Get-FullPath $Path)
        $commit = Join-Path $folder 'commit.marker'; $cancel = Join-Path $folder 'cancel.marker'
        $deadline = [DateTime]::UtcNow.AddSeconds(60)
        while (!(Test-Path -LiteralPath $commit)) {
            if (Test-Path -LiteralPath $cancel) { return $false }
            if ([DateTime]::UtcNow -ge $deadline) { return $false }
            Start-Sleep -Milliseconds 200
        }
        foreach ($handle in $handles) {
            while ($true) {
                if (Test-Path -LiteralPath $cancel) { return $false }
                $wait = [TimeCueUpdateNative]::WaitForSingleObject($handle,250)
                if ($wait -eq 0) { break }
                if ($wait -ne 258) { throw '等待 TimeCue 进程退出失败。' }
                if ([DateTime]::UtcNow -ge $deadline) { throw '等待 TimeCue 进程退出超时。' }
            }
        }
        return $true
    } finally { foreach ($handle in $handles) { [void][TimeCueUpdateNative]::CloseHandle($handle) } }
}

function Quote-Argument([string]$Argument) {
    if ($Argument -notmatch '[\s"]') { return $Argument }
    $builder = New-Object Text.StringBuilder
    [void]$builder.Append('"')
    $slashes = 0
    foreach ($character in $Argument.ToCharArray()) {
        if ($character -eq '\') { $slashes++; continue }
        if ($character -eq '"') { [void]$builder.Append(('\' * (2 * $slashes + 1))); [void]$builder.Append('"'); $slashes = 0; continue }
        if ($slashes) { [void]$builder.Append(('\' * $slashes)); $slashes = 0 }
        [void]$builder.Append($character)
    }
    if ($slashes) { [void]$builder.Append(('\' * (2 * $slashes))) }
    [void]$builder.Append('"')
    return $builder.ToString()
}

function Start-InstalledApplication($Plan, [string[]]$Arguments) {
    $args = New-Object 'System.Collections.Generic.List[string]'
    foreach ($argument in $Arguments) { $args.Add([string]$argument) }
    if (!($args | Where-Object { $_ -eq '--backend=managed' })) { $args.Insert(0,'--backend=managed') }
    if (!($args | Where-Object { $_ -like '--data-dir=*' })) { $args.Add('--data-dir=' + [string]$Plan.dataDirectory) }
    $quoted = @($args | ForEach-Object { Quote-Argument $_ }) -join ' '
    $start = New-Object Diagnostics.ProcessStartInfo
    $start.FileName = Join-Path ([string]$Plan.installDirectory) 'timecue_desktop.exe'
    $start.WorkingDirectory = [string]$Plan.installDirectory
    $start.Arguments = $quoted
    $start.UseShellExecute = $false
    $process = [Diagnostics.Process]::Start($start)
    if (!$process) { throw 'Windows 未能启动更新后的 TimeCue。' }
    $process.Dispose()
}

function Invoke-Install([string]$Path, $Plan) {
    $package = Test-Package $Plan
    try {
        $preparedPath = Get-PreparedPath $Path
        if (!(Test-Path -LiteralPath $preparedPath -PathType Leaf)) { throw '缺少 Prepare 阶段的验证记录。' }
        $prepared = [IO.File]::ReadAllText($preparedPath) | ConvertFrom-Json
        if ($prepared.schemaVersion -ne 1 -or $prepared.version -ne $Plan.targetVersion) { throw '已验证更新记录与计划不匹配。' }
        if (@($prepared.files.PSObject.Properties).Count -ne $package.files.Count) { throw '已验证文件清单与更新包不一致。' }
        foreach ($key in $package.files.Keys) {
            $preparedEntry = $prepared.files.PSObject.Properties[$key]
            if (!$preparedEntry -or $preparedEntry.Value -ne $package.files[$key]) { throw '已验证文件清单与更新包不一致。' }
            $source = [IO.Path]::GetFullPath((Join-Path ([string]$Plan.stagingDirectory) $key))
            if (!(Test-ChildPath $source ([string]$Plan.stagingDirectory)) -or !(Test-Path -LiteralPath $source -PathType Leaf)) { throw "暂存文件缺失：$key" }
            Assert-NoReparsePath $source
            if ((Get-FileHashHex $source) -ne $package.files[$key]) { throw "暂存文件校验失败：$key" }
        }
        Assert-NoProtectedTarget $Plan @($package.files.Keys)
        if (!(Wait-ForDecisionAndExit $Path $Plan)) { Write-Status $Path $Plan 'cancelled' '更新已取消，程序文件未更改。'; return }
        $install = Get-FullPath ([string]$Plan.installDirectory)
        $backup = Join-Path $install ('.timecue-update-backup-' + [Guid]::NewGuid().ToString('N'))
        [IO.Directory]::CreateDirectory($backup) | Out-Null
        $journalPath = Join-Path (Split-Path -Parent (Get-FullPath $Path)) 'journal.json'
        $oldPaths = @(Get-OldManifestPaths $install)
        $newPaths = @($package.files.Keys)
        $obsolete = @($oldPaths | Where-Object { $newPaths -notcontains $_ } | Select-Object -Unique)
        $allPaths = @($newPaths + $obsolete | Select-Object -Unique | Sort-Object)
        Assert-NoProtectedTarget $Plan $allPaths
        $entries = @()
        foreach ($relative in $allPaths) {
            $target = Join-Path $install $relative
            $has = Test-Path -LiteralPath $target -PathType Leaf
            $tempName = [IO.Path]::Combine([IO.Path]::GetDirectoryName($relative),('.timecue-update-' + [Guid]::NewGuid().ToString('N') + '.tmp'))
            if ($tempName.StartsWith('.\')) { $tempName = $tempName.Substring(2) }
            $digest = ''; $newFile = [bool]$package.files.Contains($relative)
            if ($newFile) { $digest = $package.files[$relative] }
            $entries += [ordered]@{ path=$relative; hadFile=[bool]$has; started=$false; sha256=$digest; tempPath=$tempName; newFile=$newFile }
        }
        $journal = [ordered]@{ schemaVersion=1; installDirectory=$install; backupDirectory=$backup; journalPath=$journalPath; protectedPaths=@($Plan.protectedPaths); dataDirectory=[string]$Plan.dataDirectory; entries=$entries }
        Write-Journal $journal
        Write-Status $Path $Plan 'installing' '正在安装更新，请勿启动程序。' $journalPath
        try {
            foreach ($entry in $journal.entries) {
                $relative = [string]$entry.path
                $target = [IO.Path]::GetFullPath((Join-Path $install $relative))
                $backupTarget = [IO.Path]::GetFullPath((Join-Path $backup $relative))
                if (!(Test-ChildPath $target $install) -or !(Test-ChildPath $backupTarget $backup)) { throw '安装事务路径越界。' }
                Assert-NoReparsePath ([IO.Path]::GetDirectoryName($target))
                $entry.started = $true
                Write-Journal $journal
                if ($entry.hadFile) {
                    Assert-NoReparsePath $target
                    [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($backupTarget)) | Out-Null
                    [IO.File]::Move($target,$backupTarget)
                }
                if ($entry.newFile) {
                    $source = Join-Path ([string]$Plan.stagingDirectory) $relative
                    $temporary = Join-Path $install ([string]$entry.tempPath)
                    [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($target)) | Out-Null
                    $input = [IO.File]::OpenRead($source)
                    try {
                        $output = [IO.File]::Open($temporary,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
                        try { $input.CopyTo($output); $output.Flush($true) } finally { $output.Dispose() }
                    } finally { $input.Dispose() }
                    [IO.File]::Move($temporary,$target)
                }
            }
        } catch {
            try {
                Invoke-Recover $journalPath
                Write-Status $Path $Plan 'rolledBack' ('安装失败，已恢复旧程序。恢复日志：' + $journalPath) $journalPath
                try { Start-InstalledApplication $Plan @($Plan.restartArguments) } catch { Show-InstallError ('旧程序已恢复，但无法重新启动：' + $_.Exception.Message + "`n恢复日志：" + $journalPath) }
            } catch {
                Write-Status $Path $Plan 'recoveryRequired' ('自动恢复失败，请运行恢复命令。恢复日志：' + $journalPath + '；错误：' + $_.Exception.Message) $journalPath
                Show-InstallError ('TimeCue 更新失败且自动恢复未完成。请先备份用户数据，再运行：powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "' + $PSCommandPath + '" -Mode Recover -JournalPath "' + $journalPath + '"')
            }
            throw
        }
        Write-Status $Path $Plan 'installed' '更新已安装。' $journalPath
        try { Start-InstalledApplication $Plan @($Plan.restartArguments) }
        catch { Write-Status $Path $Plan 'restartFailed' ('更新已安装，但程序启动失败：' + $_.Exception.Message + '；恢复日志：' + $journalPath) $journalPath; Show-InstallError ('更新已安装，但 TimeCue 未能启动。请检查恢复日志：' + $journalPath); throw }
    } finally { if ($package.archive) { $package.archive.Dispose() } }
}

function Show-InstallError([string]$Message) {
    try { Add-Win32Types; [void][TimeCueUpdateNative]::MessageBox([IntPtr]::Zero,$Message,'TimeCue 更新失败',0x10) } catch { }
}

try {
    if ($Mode -eq 'Recover') { Invoke-Recover $JournalPath; exit 0 }
    if (!$PlanPath) { throw '必须提供 PlanPath。' }
    $plan = Read-Plan $PlanPath
    try {
        if ($Mode -eq 'Prepare') { Invoke-Prepare $PlanPath $plan }
        else { Invoke-Install $PlanPath $plan }
    } catch {
        $state = if ($Mode -eq 'Prepare') { 'failed' } else { 'failed' }
        $journal = Join-Path (Split-Path -Parent (Get-FullPath $PlanPath)) 'journal.json'
        $message = '更新失败：' + $_.Exception.Message
        if ($Mode -eq 'Install' -and (Test-Path -LiteralPath $journal)) { $message += '；恢复日志：' + $journal }
        if ($script:FinalState -notin @('rolledBack','recoveryRequired','restartFailed','cancelled')) { try { Write-Status $PlanPath $plan $state $message $journal } catch { } }
        if ($Mode -eq 'Install') { Show-InstallError ($message + $(if (Test-Path -LiteralPath $journal) { "`n恢复日志：$journal" } else { '' })) }
        [Console]::Error.WriteLine($message)
        exit 1
    }
} catch {
    [Console]::Error.WriteLine('更新器失败：' + $_.Exception.Message)
    exit 1
}
