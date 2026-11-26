# SPDX-License-Identifier: GPL-3.0-or-later
# Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.
<#
.SYNOPSIS
Deploys the WowMcp addon and the wow-mcp server with a single command.

.DESCRIPTION
Copies the addon to <WowDir>\<Client>\Interface\AddOns\WowMcp (with a backup in the project folder
and removal of obsolete files), replaces bin\wow-mcp.exe (the old file is renamed to .old.exe,
because a running exe cannot be overwritten, but it can be renamed) and runs the dump check.
The script writes only in the project folder and in the addon folder in the game. It does not touch WTF or SavedVariables.
Go is not required (a ready bin\wow-mcp.exe is in the project); -Build uses Go if it is in PATH.

.PARAMETER WowDir
The game folder. Defaults to the WOW_DIR environment variable, and when it is not set: C:\Program Files (x86)\World of Warcraft.

.PARAMETER Client
The client subfolder in the game folder. Defaults to _forever_.

.PARAMETER Build
Build the server from the server\ folder into bin\wow-mcp.new.exe (requires Go in PATH).

.PARAMETER Rollback
Return to the previous version: the last addon copy from backup\ and the newest .old.exe file.

.PARAMETER KeepOld
How many recent addon copies and .old.exe files to keep. Defaults to 2.

.PARAMETER Slots
Also install this many request-slot addons (WowMcp_S0001..) in the AddOns folder: the
inbound channel the server writes requests into (waypoints, map focus). 600 is the usual
count; slot folders above the number are removed. Defaults to 0 (no slots are touched).

.PARAMETER SkipCheck
Skip running wow-mcp.exe --check at the end.

.EXAMPLE
powershell -ExecutionPolicy Bypass -File .\install.ps1

.EXAMPLE
.\install.ps1 -Build -WhatIf

.EXAMPLE
.\install.ps1 -Rollback

#>
[CmdletBinding(SupportsShouldProcess)]
param(
    [string]$WowDir = $(if ($env:WOW_DIR) { $env:WOW_DIR } else { 'C:\Program Files (x86)\World of Warcraft' }),
    [string]$Client = '_forever_',
    [switch]$Build,
    [switch]$Rollback,
    [int]$KeepOld = 2,
    [int]$Slots = 0,
    [switch]$SkipCheck
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:ProjectRoot = $PSScriptRoot
$script:AddonName = 'WowMcp'

function Write-Step([string]$Text) { Write-Host ('==> ' + $Text) -ForegroundColor Cyan }
function Write-Ok([string]$Text) { Write-Host ('    ' + $Text) -ForegroundColor Green }
function Write-Warn([string]$Text) { Write-Host ('    WARNING: ' + $Text) -ForegroundColor Yellow }

function Stop-Install([string]$Text) {
    Write-Host ('ERROR: ' + $Text) -ForegroundColor Red
    exit 1
}

function Get-Paths {
    $addonsDir = Join-Path (Join-Path $WowDir $Client) 'Interface\AddOns'
    return [pscustomobject]@{
        AddonSource = Join-Path (Join-Path $script:ProjectRoot 'addon') $script:AddonName
        AddonTarget = Join-Path $addonsDir $script:AddonName
        AddonsDir   = $addonsDir
        Toc         = Join-Path (Join-Path (Join-Path $script:ProjectRoot 'addon') $script:AddonName) 'WowMcp.toc'
        BackupDir   = Join-Path $script:ProjectRoot 'backup'
        BinDir      = Join-Path $script:ProjectRoot 'bin'
        Exe         = Join-Path (Join-Path $script:ProjectRoot 'bin') 'wow-mcp.exe'
        NewExe      = Join-Path (Join-Path $script:ProjectRoot 'bin') 'wow-mcp.new.exe'
        ServerDir   = Join-Path $script:ProjectRoot 'server'
    }
}

function Test-Prerequisites($Paths) {
    if (-not (Test-Path -LiteralPath $Paths.Toc)) {
        Stop-Install ('File not found: ' + $Paths.Toc + '. Run the script from the wow-mcp project folder.')
    }
    if (-not (Test-Path -LiteralPath $WowDir -PathType Container)) {
        Stop-Install ('The game folder does not exist: ' + $WowDir + '. Provide the right one with -WowDir or the WOW_DIR variable.')
    }
    if (-not (Test-Path -LiteralPath $Paths.AddonsDir -PathType Container)) {
        Stop-Install ('The AddOns folder does not exist: ' + $Paths.AddonsDir + '. Check -Client (currently: ' + $Client + ').')
    }
    if ($KeepOld -lt 1) {
        Stop-Install '-KeepOld must be a number >= 1.'
    }
    if (($Slots -lt 0) -or ($Slots -gt 9999)) {
        Stop-Install '-Slots must be between 0 and 9999 (the WowMcp_SNNNN naming).'
    }
}

# -Client must name one folder inside the game folder ("..\.." would move the addon target out of it).
function Test-ClientName {
    if ([string]::IsNullOrWhiteSpace($Client) -or ($Client -match '[\\/:]') -or ($Client -eq '.') -or ($Client -eq '..')) {
        Stop-Install ('-Client must be a single folder name such as _forever_ (got: ' + $Client + ').')
    }
}

# Safeguard: we copy/delete only inside the ...\AddOns\WowMcp folder.
function Assert-SafeAddonTarget($Paths) {
    $leaf = Split-Path -Leaf $Paths.AddonTarget
    $parent = Split-Path -Parent $Paths.AddonTarget
    $parentLeaf = Split-Path -Leaf $parent
    if (($leaf -ne $script:AddonName) -or ($parentLeaf -ne 'AddOns')) {
        Stop-Install ('Unsafe addon target folder: ' + $Paths.AddonTarget)
    }
    # robocopy /MIR deletes what is not in the source: through a junction or symlink (a common developer setup that
    # links the repository folder) it would delete files outside the AddOns folder.
    $item = Get-Item -LiteralPath $Paths.AddonTarget -Force -ErrorAction SilentlyContinue
    if (($null -ne $item) -and ($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint)) {
        Stop-Install ('The addon folder is a link (junction or symlink): ' + $Paths.AddonTarget +
            '. The script mirrors only into a real folder; remove the link or update the linked folder yourself.')
    }
}

function Get-TocValue([string]$TocPath, [string]$Key) {
    $pattern = '^\s*##\s*' + [regex]::Escape($Key) + '\s*:\s*(.+?)\s*$'
    foreach ($line in (Get-Content -LiteralPath $TocPath)) {
        $m = [regex]::Match($line, $pattern)
        if ($m.Success) { return $m.Groups[1].Value }
    }
    return $null
}

function Get-ServerVersion([string]$ExePath) {
    if (-not (Test-Path -LiteralPath $ExePath)) { return $null }
    $saved = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $out = & $ExePath --version 2>&1 | Out-String
    }
    catch {
        $out = ''
    }
    finally {
        $ErrorActionPreference = $saved
    }
    $m = [regex]::Match($out, 'wow-mcp\s+(\S+)')
    if ($m.Success) { return $m.Groups[1].Value }
    return $null
}

function ConvertTo-SafeFileToken([string]$Text) {
    return ($Text -replace '[^0-9A-Za-z._-]', '_')
}

function Copy-DirectoryMirror([string]$Source, [string]$Destination) {
    if (-not $PSCmdlet.ShouldProcess($Destination, 'Mirror the folder (copy and remove unneeded files)')) { return }
    $null = New-Item -ItemType Directory -Force -Path $Destination
    $roboArgs = @($Source, $Destination, '/MIR', '/NJH', '/NJS', '/NP', '/NFL', '/NDL', '/R:2', '/W:1')
    $null = & robocopy @roboArgs
    if ($LASTEXITCODE -ge 8) {
        throw ('robocopy failed (code ' + $LASTEXITCODE + ').')
    }
    $global:LASTEXITCODE = 0
}

function Backup-InstalledAddon($Paths) {
    if (-not (Test-Path -LiteralPath $Paths.AddonTarget)) {
        Write-Ok 'The addon was not installed yet - no backup needed.'
        return
    }
    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
    $dest = Join-Path $Paths.BackupDir ('addon-' + $stamp)
    if ($PSCmdlet.ShouldProcess($dest, 'Back up the installed addon')) {
        $null = New-Item -ItemType Directory -Force -Path $dest
        Copy-Item -Path (Join-Path $Paths.AddonTarget '*') -Destination $dest -Recurse -Force
        Write-Ok ('Addon backup: ' + $dest)
    }
}

function Get-AddonBackups($Paths) {
    if (-not (Test-Path -LiteralPath $Paths.BackupDir)) { return @() }
    return @(Get-ChildItem -LiteralPath $Paths.BackupDir -Directory -Filter 'addon-*' |
            Sort-Object LastWriteTime -Descending)
}

function Remove-OldAddonBackups($Paths) {
    $old = @(Get-AddonBackups $Paths | Select-Object -Skip $KeepOld)
    foreach ($dir in $old) {
        if ($PSCmdlet.ShouldProcess($dir.FullName, 'Remove an old addon backup')) {
            try { Remove-Item -LiteralPath $dir.FullName -Recurse -Force }
            catch { Write-Warn ('Could not remove ' + $dir.FullName + ': ' + $_.Exception.Message) }
        }
    }
}

function Install-Addon($Paths) {
    Write-Step 'Addon: backup and installation'
    Assert-SafeAddonTarget $Paths
    Backup-InstalledAddon $Paths
    Copy-DirectoryMirror $Paths.AddonSource $Paths.AddonTarget
    Remove-OldAddonBackups $Paths
    Write-Ok ('Addon installed: ' + $Paths.AddonTarget)
}

# Generates the request-slot addons WowMcp_S0001..WowMcp_S$Slots: the inbound channel the
# server writes requests into (the same files server/internal/slots.Install creates). It
# writes ONLY folders named WowMcp_SNNNN directly under Interface\AddOns, and removes slot
# folders above $Slots so an older, larger install leaves no stale bodies.
function Install-RequestSlots($Paths) {
    if ($Slots -lt 1) { return }
    Write-Step ('Request slots: ' + $Slots + ' folders WowMcp_S0001..WowMcp_S' + $Slots.ToString('0000'))
    $addonsLeaf = Split-Path -Leaf $Paths.AddonsDir
    $interfaceLeaf = Split-Path -Leaf (Split-Path -Parent $Paths.AddonsDir)
    if (($addonsLeaf -ne 'AddOns') -or ($interfaceLeaf -ne 'Interface')) {
        Stop-Install ('Request slots go only under Interface\AddOns: ' + $Paths.AddonsDir)
    }
    if (-not $PSCmdlet.ShouldProcess($Paths.AddonsDir, ('Install ' + $Slots + ' request slots'))) { return }
    $t = [System.DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
    $body = 'WowMcpInbox = {["t"]=' + $t + ',["reqs"]={},["got"]={}}' + "`n"
    for ($n = 1; $n -le $Slots; $n++) {
        $name = 'WowMcp_S' + $n.ToString('0000')
        $dir = Join-Path $Paths.AddonsDir $name
        $null = New-Item -ItemType Directory -Force -Path $dir
        $toc = "## Interface: 16001`n## Title: WowMcp slot " + $n.ToString('0000') +
            "`n## Notes: Request slot for WowMcp.`n## Group: WowMcp`n## LoadOnDemand: 1`n`nbody.lua`n"
        [System.IO.File]::WriteAllText((Join-Path $dir ($name + '.toc')), $toc)
        [System.IO.File]::WriteAllText((Join-Path $dir 'body.lua'), $body)
    }
    $stale = @(Get-ChildItem -LiteralPath $Paths.AddonsDir -Directory |
            Where-Object { ($_.Name -cmatch '^WowMcp_S\d{4}$') -and ([int]$_.Name.Substring(8) -gt $Slots) })
    foreach ($dir in $stale) {
        Remove-Item -LiteralPath $dir.FullName -Recurse -Force
        Write-Ok ('Removed slot above ' + $Slots + ': ' + $dir.Name)
    }
    Write-Ok ($Slots.ToString() + ' request slots installed (the server writes requests into their body.lua files).')
}

# "1.15.7.63000" -> 11507 (Interface number format: major*10000 + minor*100 + patch)
function ConvertTo-InterfaceNumber([string]$Version) {
    $m = [regex]::Match($Version, '^(\d+)\.(\d+)\.(\d+)')
    if (-not $m.Success) { return $null }
    return ([int]$m.Groups[1].Value * 10000) + ([int]$m.Groups[2].Value * 100) + [int]$m.Groups[3].Value
}

function Get-ClientVersionFromBuildInfo {
    $candidates = @((Join-Path $WowDir '.build.info'), (Join-Path (Join-Path $WowDir $Client) '.build.info'))
    foreach ($file in $candidates) {
        if (-not (Test-Path -LiteralPath $file)) { continue }
        $lines = @(Get-Content -LiteralPath $file)
        if ($lines.Count -lt 2) { continue }
        $columns = $lines[0].Split('|')
        $index = -1
        for ($i = 0; $i -lt $columns.Count; $i++) {
            if ($columns[$i] -like 'Version!*') { $index = $i; break }
        }
        if ($index -lt 0) { continue }
        $values = $lines[1].Split('|')
        if ($index -lt $values.Count -and $values[$index]) { return $values[$index] }
    }
    return $null
}

function Get-ClientVersionFromExe {
    $dir = Join-Path $WowDir $Client
    if (-not (Test-Path -LiteralPath $dir)) { return $null }
    $exe = Get-ChildItem -LiteralPath $dir -Filter '*.exe' -File |
        Where-Object { $_.Name -match '^(Wow|WowClassic|WowB|WowT)[^\\]*\.exe$' } |
        Select-Object -First 1
    if ($null -eq $exe) { return $null }
    $ver = $exe.VersionInfo.ProductVersion
    if (-not $ver) { $ver = $exe.VersionInfo.FileVersion }
    if ($ver) { return ($ver -replace ',\s*', '.') }
    return $null
}

function Test-InterfaceVersion($Paths) {
    Write-Step 'Interface number check'
    try {
        $tocInterface = Get-TocValue $Paths.Toc 'Interface'
        if (-not $tocInterface) { Write-Warn 'The .toc has no ## Interface line.'; return }
        $clientVersion = Get-ClientVersionFromBuildInfo
        if (-not $clientVersion) { $clientVersion = Get-ClientVersionFromExe }
        if (-not $clientVersion) { Write-Ok 'Could not read the client version - skipping the check.'; return }
        $clientInterface = ConvertTo-InterfaceNumber $clientVersion
        if ($null -eq $clientInterface) { Write-Ok ('Unknown client version format: ' + $clientVersion); return }
        if ([string]$clientInterface -eq $tocInterface.Trim()) {
            Write-Ok ('Interface matches: .toc ' + $tocInterface + ', client ' + $clientVersion)
        }
        else {
            Write-Warn ('Interface in .toc = ' + $tocInterface + ', and client ' + $clientVersion + ' suggests ' + $clientInterface +
                '. The addon may be marked "out of date" (in the game tick "Load out of date AddOns").')
        }
    }
    catch {
        Write-Warn ('The Interface check failed (skipping): ' + $_.Exception.Message)
    }
}

function Build-Server($Paths) {
    Write-Step 'Server: building from the server\ folder'
    $go = Get-Command go -ErrorAction SilentlyContinue
    if ($null -eq $go) {
        Write-Warn 'Go was not found in PATH - skipping the build and using the ready binary.'
        return
    }
    if (-not (Test-Path -LiteralPath $Paths.ServerDir)) {
        Write-Warn ('Missing folder ' + $Paths.ServerDir + ' - skipping the build.')
        return
    }
    if (-not $PSCmdlet.ShouldProcess($Paths.NewExe, 'go build')) { return }
    $null = New-Item -ItemType Directory -Force -Path $Paths.BinDir
    Push-Location $Paths.ServerDir
    try {
        & $go.Source build -trimpath -ldflags '-s -w' -o $Paths.NewExe .
        if ($LASTEXITCODE -ne 0) { throw ('go build failed (code ' + $LASTEXITCODE + ').') }
    }
    finally {
        Pop-Location
    }
    Write-Ok ('Built: ' + $Paths.NewExe)
}

function Get-UniqueOldExePath([string]$BinDir, [string]$VersionToken) {
    $path = Join-Path $BinDir ('wow-mcp-' + $VersionToken + '.old.exe')
    if (-not (Test-Path -LiteralPath $path)) { return $path }
    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
    $path = Join-Path $BinDir ('wow-mcp-' + $VersionToken + '-' + $stamp + '.old.exe')
    $n = 1
    while (Test-Path -LiteralPath $path) {
        $path = Join-Path $BinDir ('wow-mcp-' + $VersionToken + '-' + $stamp + '-' + $n + '.old.exe')
        $n++
    }
    return $path
}

# Renames the current exe to .old.exe (with the version); returns the new path or $null.
function Rename-CurrentExeToOld($Paths) {
    if (-not (Test-Path -LiteralPath $Paths.Exe)) { return $null }
    $version = Get-ServerVersion $Paths.Exe
    if ($version) { $token = ConvertTo-SafeFileToken $version } else { $token = Get-Date -Format 'yyyyMMdd-HHmmss' }
    $target = Get-UniqueOldExePath $Paths.BinDir $token
    if (-not $PSCmdlet.ShouldProcess($Paths.Exe, ('Rename to ' + (Split-Path -Leaf $target)))) { return $null }
    Rename-Item -LiteralPath $Paths.Exe -NewName (Split-Path -Leaf $target)
    # "newest" is counted from the moment of renaming, not from the build date
    try { (Get-Item -LiteralPath $target).LastWriteTime = Get-Date } catch { }
    Write-Ok ('Old server kept as ' + (Split-Path -Leaf $target))
    return $target
}

# Replaces bin\wow-mcp.exe with the file $NewPath; restores the old one on error.
function Swap-ServerExe($Paths, [string]$NewPath) {
    $oldPath = Rename-CurrentExeToOld $Paths
    if (-not $PSCmdlet.ShouldProcess($Paths.Exe, ('Put in place ' + (Split-Path -Leaf $NewPath)))) { return }
    try {
        Move-Item -LiteralPath $NewPath -Destination $Paths.Exe
    }
    catch {
        if ($oldPath -and (Test-Path -LiteralPath $oldPath)) {
            Move-Item -LiteralPath $oldPath -Destination $Paths.Exe
        }
        throw
    }
}

function Get-OldExes($Paths) {
    if (-not (Test-Path -LiteralPath $Paths.BinDir)) { return @() }
    return @(Get-ChildItem -LiteralPath $Paths.BinDir -File -Filter '*.old.exe' |
            Sort-Object LastWriteTime -Descending)
}

function Remove-OldExes($Paths) {
    $stale = @(Get-OldExes $Paths | Select-Object -Skip $KeepOld)
    foreach ($file in $stale) {
        if ($PSCmdlet.ShouldProcess($file.FullName, 'Remove an old server')) {
            try { Remove-Item -LiteralPath $file.FullName -Force }
            catch { Write-Warn ('Could not remove ' + $file.Name + ' (locked?): ' + $_.Exception.Message) }
        }
    }
}

function Install-Server($Paths) {
    if ($Build) { Build-Server $Paths }
    Write-Step 'Server: replacing bin\wow-mcp.exe'
    if (Test-Path -LiteralPath $Paths.NewExe) {
        Swap-ServerExe $Paths $Paths.NewExe
        Write-Ok 'The new server is in place.'
    }
    elseif (Test-Path -LiteralPath $Paths.Exe) {
        Write-Ok 'No bin\wow-mcp.new.exe - bin\wow-mcp.exe is already the new version, nothing to replace.'
    }
    else {
        Stop-Install ('No server: neither ' + $Paths.Exe + ' nor ' + $Paths.NewExe + ' exists. Use -Build or put the binary in bin\.')
    }
    Remove-OldExes $Paths
}

function Invoke-ServerCheck($Paths) {
    Write-Step 'Dump check (wow-mcp --check)'
    if (-not (Test-Path -LiteralPath $Paths.Exe)) { Write-Warn 'No bin\wow-mcp.exe - skipping.'; return }
    if ($WhatIfPreference) { Write-Ok '-WhatIf mode: skipping the run.'; return }
    $saved = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $output = & $Paths.Exe --wow-dir $WowDir --check 2>&1 | Out-String
        $code = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $saved
    }
    Write-Host $output.TrimEnd()
    if ($code -ne 0) {
        Write-Warn ('--check finished with code ' + $code + '. The dump will appear only after /mcp reload in the game (on a first installation this is normal).')
    }
    else {
        Write-Ok 'Dump read successfully.'
    }
}

function Restore-PreviousAddon($Paths) {
    Write-Step 'Addon rollback'
    $backups = @(Get-AddonBackups $Paths)
    if ($backups.Count -eq 0) {
        Write-Warn 'No addon copies in backup\ - nothing to go back to.'
        return $false
    }
    Assert-SafeAddonTarget $Paths
    $latest = $backups[0]
    Backup-InstalledAddon $Paths   # the current version also goes to backup\, so the rollback is reversible
    Copy-DirectoryMirror $latest.FullName $Paths.AddonTarget
    Write-Ok ('Addon restored from ' + $latest.Name)
    return $true
}

function Restore-PreviousServer($Paths) {
    Write-Step 'Server rollback'
    $olds = @(Get-OldExes $Paths)
    if ($olds.Count -eq 0) {
        Write-Warn 'No *.old.exe files in bin\ - nothing to go back to.'
        return $false
    }
    $latest = $olds[0]
    $null = Rename-CurrentExeToOld $Paths
    if ($PSCmdlet.ShouldProcess($latest.FullName, 'Restore as bin\wow-mcp.exe')) {
        Move-Item -LiteralPath $latest.FullName -Destination $Paths.Exe
        Write-Ok ('Server restored from ' + $latest.Name)
    }
    return $true
}

function Invoke-Rollback($Paths) {
    $addonDone = Restore-PreviousAddon $Paths
    $serverDone = Restore-PreviousServer $Paths
    if (-not ($addonDone -or $serverDone)) {
        Stop-Install 'Nothing to restore: no addon copies and no .old.exe files.'
    }
}

function Write-Summary($Paths) {
    $addonVersion = Get-TocValue $Paths.Toc 'Version'
    $serverVersion = Get-ServerVersion $Paths.Exe
    if (-not $addonVersion) { $addonVersion = '?' }
    if (-not $serverVersion) { $serverVersion = '?' }
    Write-Host ''
    Write-Host 'Done.' -ForegroundColor Green
    Write-Host ('  Addon WowMcp: ' + $addonVersion)
    Write-Host ('  wow-mcp server:     ' + $serverVersion)
    Write-Host ''
    Write-Host 'Next steps:'
    Write-Host '  1. Add this entry under "mcpServers" in your MCP client configuration, then restart the client completely:'
    $entry = [ordered]@{ wow = [ordered]@{ command = $Paths.Exe; args = @('--wow-dir', $WowDir) } }
    Write-Host ($entry | ConvertTo-Json -Depth 4)
    Write-Host '  2. In the game type /reload, then /mcp reload.'
    Write-Host '  3. Ask your assistant about wow_status.'
}

function Invoke-Install($Paths) {
    Install-Addon $Paths
    Install-RequestSlots $Paths
    Test-InterfaceVersion $Paths
    Install-Server $Paths
    if (-not $SkipCheck) { Invoke-ServerCheck $Paths }
}

function Main {
    Test-ClientName
    $paths = Get-Paths
    Test-Prerequisites $paths
    if ($Rollback) {
        Invoke-Rollback $paths
    }
    else {
        Invoke-Install $paths
    }
    if ($Rollback -and -not $SkipCheck) { Invoke-ServerCheck $paths }
    Write-Summary $paths
}

try {
    Main
}
catch {
    Write-Host ('ERROR: ' + $_.Exception.Message) -ForegroundColor Red
    exit 1
}
