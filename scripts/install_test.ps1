#requires -Version 5.1
# Self-contained regression checks: no administrator, Pester or real user PATH writes.
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'install.ps1')

function Assert($Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}
function Assert-Throws([scriptblock]$Action, [string]$Message) {
    $failed = $false
    try { & $Action } catch { $failed = $true }
    Assert $failed $Message
}

$scratch = Join-Path ([IO.Path]::GetTempPath()) ('aikit installer test ' + [Guid]::NewGuid().ToString('N'))
$originalPath = $env:Path
$originalLocation = Get-Location
[void][IO.Directory]::CreateDirectory($scratch)
try {
    $script:testLocal = Join-Path $scratch 'local app data'
    $fixture = Join-Path $scratch 'trusted native fixture.exe'
    Add-Type -TypeDefinition @'
public static class InstallerFixture {
    public static void Main(string[] args) { System.Console.WriteLine("aikit installer-test"); }
}
'@ -OutputAssembly $fixture -OutputType ConsoleApplication

    # Replace only OS boundaries; exercise actual filesystem, native launch and PATH logic.
    function Get-AikitLocalAppData { $script:testLocal }
    function Get-AikitMachinePath { '' }
    $script:storedPath = @{ Value = '%USERPROFILE%\my tools;;C:\existing'; Kind = [Microsoft.Win32.RegistryValueKind]::ExpandString }
    $script:writes = 0
    $script:failWrite = $false
    function Get-AikitUserPath { $script:storedPath.Clone() }
    function Set-AikitUserPath($Value, $Kind) {
        if ($script:failWrite) { throw 'simulated registry failure' }
        $script:storedPath = @{ Value = $Value; Kind = $Kind }
        $script:writes++
    }
    function Send-AikitEnvironmentChange { }

    $bin = Join-Path $script:testLocal 'AIKIT\bin'
    $target = Join-Path $bin 'aikit.exe'
    $env:Path = 'C:\process-only;' + $originalPath
    Install-Aikit $fixture
    Assert ([IO.File]::Exists($target)) 'Binary was not installed into the known folder.'
    Assert ($script:storedPath.Value -ceq ($bin + ';%USERPROFILE%\my tools;;C:\existing')) 'User PATH entries were expanded, removed or reordered.'
    Assert ($script:storedPath.Kind -eq [Microsoft.Win32.RegistryValueKind]::ExpandString) 'Registry value kind changed.'
    Assert ($env:Path.StartsWith($bin + ';C:\process-only;')) 'Current PowerShell PATH was not updated.'
    Assert ((Get-Command aikit -CommandType Application).Source -eq $target) 'Installed command cannot be discovered.'
    Assert ((& aikit --version) -eq 'aikit installer-test') 'Installed command cannot run by name.'

    $saved = $script:storedPath.Value
    Install-Aikit $fixture
    Assert ($script:storedPath.Value -ceq $saved) 'Repeated install duplicated PATH.'
    Install-Aikit $target
    Assert ($script:storedPath.Value -ceq $saved) 'In-place repair changed PATH.'
    Set-Location -LiteralPath $scratch
    Install-Aikit '.\trusted native fixture.exe'
    Set-Location -LiteralPath $originalLocation.Path
    Assert ($script:storedPath.Value -ceq $saved) 'Relative binary path install changed PATH.'
    $duplicated = '"' + $bin.ToUpperInvariant() + '\";C:\keep;' + $bin
    Assert ((Add-AikitPath $duplicated $bin) -eq ($bin + ';C:\keep')) 'Quoted/case/trailing-slash duplicates were not removed.'
    $env:AIKIT_INSTALL_TEST_LOCAL = $script:testLocal
    Assert ((Add-AikitPath '%AIKIT_INSTALL_TEST_LOCAL%\AIKIT\bin;C:\keep' $bin) -eq ($bin + ';C:\keep')) 'Expandable duplicate was not removed.'
    Assert ((Add-AikitPath '' $bin) -eq $bin) 'Empty PATH was not handled.'
    Assert ((Add-AikitPath $bin $bin) -eq $bin) 'Singleton PATH gained an empty entry.'
    Set-Alias -Name aikit -Value Write-Output
    try {
        $warnings = @(& { Install-Aikit $target } 3>&1 | Where-Object { $_ -is [Management.Automation.WarningRecord] })
        Assert (@($warnings | Where-Object { $_.Message -like '*shadows aikit*' }).Count -eq 1) 'Alias conflict was not reported.'
    } finally { Remove-Item Alias:\aikit }
    function Get-AikitMachinePath { $scratch }
    [IO.File]::Copy($fixture, (Join-Path $scratch 'aikit.exe'))
    Assert (Test-AikitMachineConflict $bin) 'Conflicting machine PATH executable was not detected.'
    function Get-AikitMachinePath { '' }

    $script:storedPath = @{ Value = 'C:\plain'; Kind = [Microsoft.Win32.RegistryValueKind]::String }
    Install-Aikit $fixture
    Assert ($script:storedPath.Kind -eq [Microsoft.Win32.RegistryValueKind]::String) 'REG_SZ type was changed.'
    $before = (Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash
    $saved = $script:storedPath.Value
    $processBefore = $env:Path
    $invalid = Join-Path $scratch 'invalid.exe'
    [IO.File]::WriteAllText($invalid, 'not a native executable')
    Assert-Throws { Install-Aikit $invalid } 'Invalid executable was accepted.'
    Assert-Throws { Install-Aikit (Join-Path $scratch 'missing.exe') } 'Missing executable was accepted.'
    Assert ($script:storedPath.Value -ceq $saved) 'Validation failure wrote user PATH.'
    Assert ((Get-FileHash -LiteralPath $target).Hash -eq $before) 'Validation failure replaced installed binary.'

    # A failed persistent PATH write must restore the previous executable.
    [IO.File]::AppendAllText($fixture, 'replacement fixture')
    $script:failWrite = $true
    Assert-Throws { Install-Aikit $fixture } 'Registry write failure was swallowed.'
    $script:failWrite = $false
    Assert ((Get-FileHash -LiteralPath $target).Hash -eq $before) 'Registry failure lost the old binary.'
    Assert ($env:Path -ceq $processBefore) 'Failure changed the calling process PATH.'
    Assert (@(Get-ChildItem -LiteralPath $bin -Filter 'aikit-install-*').Count -eq 0) 'Staging files remain after rollback.'

    $lock = [IO.File]::Open($target, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::None)
    try { Assert-Throws { Install-Aikit $fixture } 'Locked executable replacement was accepted.' }
    finally { $lock.Dispose() }
    Assert ((Get-FileHash -LiteralPath $target).Hash -eq $before) 'Locked executable was changed.'
    Assert ($script:storedPath.Value -ceq $saved) 'Locked executable failure wrote user PATH.'

    $link = Join-Path $scratch 'junction'
    [void](New-Item -ItemType Junction -Path $link -Target $script:testLocal)
    Assert-Throws { Assert-AikitPlainPath (Join-Path $link 'AIKIT\bin\aikit.exe') } 'Junction in installation path was accepted.'
    # Remove only the verified test junction itself, never recurse through it.
    [IO.Directory]::Delete($link)
    Write-Host 'PASS: Windows installer regression checks (Windows PowerShell 5.1).'
} finally {
    Set-Location -LiteralPath $originalLocation.Path
    $env:Path = $originalPath
    Remove-Item Env:\AIKIT_INSTALL_TEST_LOCAL -ErrorAction SilentlyContinue
    # This absolute random directory was created above, beneath the OS temp directory.
    $resolved = [IO.Path]::GetFullPath($scratch)
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    if ($resolved.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase)) {
        Remove-Item -LiteralPath $resolved -Recurse -Force
    }
}
