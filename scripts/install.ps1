#requires -Version 5.1
<# Offline per-user installer. Run only with a binary whose source you trust. #>
[CmdletBinding()]
param([string]$BinaryPath = (Join-Path $PSScriptRoot 'conmesh.exe'))

function Get-ConMeshLocalAppData {
    [Environment]::GetFolderPath([Environment+SpecialFolder]::LocalApplicationData)
}

function Get-ConMeshMachinePath { [Environment]::GetEnvironmentVariable('Path', 'Machine') }

function Test-ConMeshMachineConflict([string]$Directory) {
    foreach ($entry in ((Get-ConMeshMachinePath) -split ';')) {
        if ([string]::IsNullOrWhiteSpace($entry)) { continue }
        $expanded = [Environment]::ExpandEnvironmentVariables($entry.Trim().Trim('"')).TrimEnd('\', '/')
        if ([string]::Equals($expanded, $Directory, [StringComparison]::OrdinalIgnoreCase)) { continue }
        foreach ($extension in @('.exe', '.com', '.cmd', '.bat', '.ps1')) {
            if ([IO.File]::Exists((Join-Path $expanded ('conmesh' + $extension)))) { return $true }
        }
    }
    return $false
}

function Get-ConMeshUserPath {
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment')
    try {
        if ($null -eq $key -or $null -eq $key.GetValue('Path')) {
            return @{ Value = ''; Kind = [Microsoft.Win32.RegistryValueKind]::ExpandString }
        }
        $kind = $key.GetValueKind('Path')
        if ($kind -notin @([Microsoft.Win32.RegistryValueKind]::String, [Microsoft.Win32.RegistryValueKind]::ExpandString)) {
            throw 'The user PATH is not a string; no changes were made.'
        }
        return @{ Value = $key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames); Kind = $kind }
    } finally { if ($null -ne $key) { $key.Dispose() } }
}

function Set-ConMeshUserPath($Value, $Kind) {
    $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
    try { $key.SetValue('Path', $Value, $Kind) } finally { $key.Dispose() }
}

function Add-ConMeshPath([string]$Value, [string]$Directory) {
    $kept = New-Object 'System.Collections.Generic.List[string]'
    foreach ($entry in ($Value -split ';')) {
        $expanded = [Environment]::ExpandEnvironmentVariables($entry.Trim().Trim('"')).TrimEnd('\', '/')
        if (-not [string]::Equals($expanded, $Directory.TrimEnd('\', '/'), [StringComparison]::OrdinalIgnoreCase)) {
            $kept.Add($entry)
        }
    }
    # Prepend once within this PATH value; Windows combines machine PATH first.
    if ([string]::IsNullOrEmpty($Value) -or $kept.Count -eq 0) { return $Directory }
    return $Directory + ';' + ($kept -join ';')
}

function Assert-ConMeshPlainPath([string]$Path) {
    $current = [IO.Path]::GetFullPath($Path)
    while ($current) {
        if (Test-Path -LiteralPath $current) {
            $item = Get-Item -LiteralPath $current -Force -ErrorAction Stop
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                throw 'Installation paths must not contain symbolic links or junctions.'
            }
        }
        $parent = [IO.Path]::GetDirectoryName($current)
        if ($parent -eq $current) { break }
        $current = $parent
    }
}

function Test-ConMeshBinary([string]$Path) {
    $version = & $Path --version 2>&1
    if ($LASTEXITCODE -ne 0 -or ($version -join "`n") -notmatch '^conmesh [^\r\n]+$') {
        throw 'The supplied executable did not pass conmesh --version. Use a trusted native Windows ConMesh build.'
    }
}

function Send-ConMeshEnvironmentChange {
    try {
        if (-not ('ConMesh.EnvironmentNotification' -as [type])) {
            Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
namespace ConMesh {
    public static class EnvironmentNotification {
        [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        public static extern IntPtr SendMessageTimeout(IntPtr window, uint message, UIntPtr wParam,
            string lParam, uint flags, uint timeout, out UIntPtr result);
    }
}
'@
        }
        $result = [UIntPtr]::Zero
        [void][ConMesh.EnvironmentNotification]::SendMessageTimeout([IntPtr]0xffff, 0x1a, [UIntPtr]::Zero, 'Environment', 2, 2000, [ref]$result)
    } catch { Write-Warning 'PATH was saved; sign out and back in if a restarted application still has the old PATH.' }
}

function Install-ConMesh([string]$Source) {
    $ErrorActionPreference = 'Stop'
    if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { throw 'This installer supports Windows only.' }
    $sourcePath = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Source)
    Assert-ConMeshPlainPath $sourcePath
    if (-not [IO.File]::Exists($sourcePath) -or [IO.Path]::GetExtension($sourcePath) -ne '.exe') {
        throw 'Supply a trusted local Windows conmesh.exe with -BinaryPath.'
    }
    $local = Get-ConMeshLocalAppData
    if ([string]::IsNullOrWhiteSpace($local) -or -not [IO.Path]::IsPathRooted($local)) { throw 'Windows LocalApplicationData is unavailable.' }
    $directory = Join-Path $local 'ConMesh\bin'
    $target = Join-Path $directory 'conmesh.exe'
    Assert-ConMeshPlainPath $target
    $oldPath = Get-ConMeshUserPath
    $newPath = Add-ConMeshPath $oldPath.Value $directory
    $processPath = Add-ConMeshPath $env:Path $directory
    if ($newPath.Length -ge 32767 -or $processPath.Length -ge 32767) { throw 'PATH would exceed the Windows environment limit.' }
    Test-ConMeshBinary $sourcePath
    [void][IO.Directory]::CreateDirectory($directory)
    $stage = Join-Path $directory ('conmesh-install-' + [Guid]::NewGuid().ToString('N') + '.exe')
    $backup = $stage + '.previous'
    $promoted = $false
    $hadTarget = [IO.File]::Exists($target)
    try {
        $sameFile = [string]::Equals($sourcePath, $target, [StringComparison]::OrdinalIgnoreCase)
        if (-not $sameFile) {
            [IO.File]::Copy($sourcePath, $stage, $false)
            Test-ConMeshBinary $stage
            if ($hadTarget) { [IO.File]::Replace($stage, $target, $backup) }
            else { [IO.File]::Move($stage, $target) }
            $promoted = $true
        }
        Set-ConMeshUserPath $newPath $oldPath.Kind
    } catch {
        if ($promoted) {
            if ($hadTarget) { [IO.File]::Replace($backup, $target, $stage) }
            else { [IO.File]::Delete($target) }
        }
        throw
    } finally {
        if ([IO.File]::Exists($stage)) { [IO.File]::Delete($stage) }
    }
    if ([IO.File]::Exists($backup)) { [IO.File]::Delete($backup) }
    $env:Path = $processPath
    Send-ConMeshEnvironmentChange
    Write-Host "Installed ConMesh: $target"
    $resolved = Get-Command conmesh -ErrorAction SilentlyContinue
    if ($null -ne $resolved -and $resolved.CommandType -eq 'Application' -and
        [string]::Equals($resolved.Source, $target, [StringComparison]::OrdinalIgnoreCase)) {
        Test-ConMeshBinary $target
        Write-Host 'User PATH saved. conmesh is ready in this PowerShell session.'
    } else {
        Write-Warning 'User PATH saved, but another command shadows conmesh in this session. Use the installed full path or remove that alias/function.'
    }
    if (Test-ConMeshMachineConflict $directory) {
        Write-Warning 'Another conmesh exists on the machine PATH and can take precedence in new terminals. Use the installed full path until that older installation is removed.'
    }
    Write-Host 'For other existing terminals, restart the terminal application (including Windows Terminal or your editor).'
    Write-Host 'If a restarted application still has an old PATH, sign out and back in.'
}

if ($MyInvocation.InvocationName -ne '.') {
    try { Install-ConMesh $BinaryPath }
    catch { Write-Error $_; exit 1 }
}
