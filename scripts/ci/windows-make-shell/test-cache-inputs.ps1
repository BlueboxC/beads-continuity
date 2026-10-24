[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Import-Functions {
    param([string] $Path)
    $tokens = $null; $errors = $null
    $ast = [Management.Automation.Language.Parser]::ParseFile($Path, [ref] $tokens, [ref] $errors)
    if ($errors.Count) { throw ($errors | Out-String) }
    foreach ($function in $ast.FindAll({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] }, $false)) {
        . ([scriptblock]::Create(($function.Extent.Text -replace '^function\s+', 'function script:')))
    }
}

$temporary = Join-Path $env:RUNNER_TEMP ('make-cache-controls-' + [Guid]::NewGuid())
$oldChocolatey = $env:ChocolateyInstall
New-Item -ItemType Directory -Path $temporary | Out-Null
try {
    Import-Functions (Join-Path $PSScriptRoot 'native-make.ps1')
    $ImageMake = Join-Path $temporary 'absent-image.exe'
    $CacheDir = Join-Path $temporary 'cache'
    $Destination = Join-Path $temporary 'destination'
    $ChocolateyMakeSha256 = '0' * 64
    $env:ChocolateyInstall = Join-Path $temporary 'chocolatey'
    $installed = Join-Path $env:ChocolateyInstall 'lib\make\tools\install\bin\make.exe'
    New-Item -ItemType Directory -Force -Path (Split-Path $installed) | Out-Null
    Set-Content -LiteralPath $installed -Value 'fixture, never executed'
    # Isolate the checksum decision from package download and execution.
    function Invoke-ChocolateyInstall {}
    function Get-NativeMakeProblem { param([string] $Make) return $null }
    $refused = $false
    try { $null = Install-NativeMake } catch {
        if ($_.Exception.Message -notlike '*SHA-256*') { throw }
        $refused = $true
    }
    if (-not $refused -or (Test-Path (Join-Path $Destination 'make.exe'))) {
        throw 'A native executable with an unexpected checksum reached the output directory'
    }

    Import-Functions (Join-Path $PSScriptRoot 'cygwin-offline.ps1')
    $SignedIndexFiles = @('setup.zst', 'setup.zst.sig')
    foreach ($case in @('valid', 'root-settings', 'hidden-settings', 'unsigned-index', 'empty-index', 'second-mirror', 'root-index')) {
        $root = Join-Path $temporary $case
        $arch = Join-Path $root 'mirror\x86_64'
        New-Item -ItemType Directory -Force -Path $arch | Out-Null
        Set-Content (Join-Path $arch 'setup.zst') 'index fixture'
        Set-Content (Join-Path $arch 'setup.zst.sig') 'signature fixture'
        switch ($case) {
            'root-settings' { Set-Content (Join-Path $root 'setup.rc') 'extrakeys fixture' }
            'hidden-settings' {
                $hidden = Join-Path $root 'setup.rc'; Set-Content $hidden 'extrakeys fixture'
                (Get-Item $hidden).Attributes = [IO.FileAttributes]::Hidden
            }
            'unsigned-index' { Set-Content (Join-Path $arch 'setup.ini') 'unsigned fixture' }
            'empty-index' { [IO.File]::WriteAllBytes((Join-Path $arch 'setup.zst'), [byte[]] @()) }
            'second-mirror' { New-Item -ItemType Directory -Path (Join-Path $root 'other') | Out-Null }
            'root-index' { New-Item -ItemType Directory -Path (Join-Path $root 'x86_64') | Out-Null }
        }
        $accepted = $false
        try { $null = Get-SignedPackageTree $root; $accepted = $true } catch {
            if ($case -eq 'valid') { throw }
        }
        if ($accepted -ne ($case -eq 'valid')) { throw "Unexpected package-tree result for $case" }
    }
    Write-Output 'PASS: native checksum refusal, valid Cygwin index layout and six invalid package layouts'
} finally {
    $env:ChocolateyInstall = $oldChocolatey
    Remove-Item -LiteralPath $temporary -Recurse -Force
}
