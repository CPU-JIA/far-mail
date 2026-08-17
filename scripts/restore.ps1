[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$BackupPath,
    [Parameter(Mandatory)][ValidatePattern('^[A-Za-z0-9_]+$')][string]$TargetDatabase,
    [Parameter(Mandatory)][string]$ConfirmDatabase,
    [switch]$AllowProductionRestore,
    [switch]$DropAfterVerify
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$projectRoot = Split-Path -Parent $PSScriptRoot
$stoppedProduction = $false

function Invoke-Compose {
    param([Parameter(Mandatory)][string[]]$Arguments)
    $output = & docker compose @Arguments 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "docker compose command failed: $($output -join [Environment]::NewLine)"
    }
    return (($output -join "`n").Trim())
}

function Get-ComposeValue {
    param([Parameter(Mandatory)][string]$Name)
    return Invoke-Compose @('exec', '-T', 'postgres', 'printenv', $Name)
}

function Remove-Database {
    param([Parameter(Mandatory)][string]$User, [Parameter(Mandatory)][string]$Database)
    [void](Invoke-Compose @(
        'exec', '-T', 'postgres', 'psql', "--username=$User", '--dbname=postgres',
        '--no-psqlrc', '--set=ON_ERROR_STOP=1', '--command',
        "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$Database' AND pid <> pg_backend_pid();"
    ))
    [void](Invoke-Compose @('exec', '-T', 'postgres', 'dropdb', "--username=$User", '--if-exists', $Database))
}

function New-RestoreProcess {
    param([Parameter(Mandatory)][string]$User, [Parameter(Mandatory)][string]$Database)
    $startInfo = [System.Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = 'docker'
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardInput = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    foreach ($argument in @(
        'compose', 'exec', '-T', 'postgres',
        'pg_restore', "--username=$User", "--dbname=$Database",
        '--exit-on-error', '--no-owner', '--no-privileges'
    )) {
        [void]$startInfo.ArgumentList.Add($argument)
    }
    return [System.Diagnostics.Process]::Start($startInfo)
}

if ($ConfirmDatabase -cne $TargetDatabase) {
    throw 'Explicit confirmation failed: -ConfirmDatabase must exactly match -TargetDatabase'
}
if ($TargetDatabase -in @('postgres', 'template0', 'template1')) {
    throw 'The PostgreSQL maintenance/template databases cannot be restore targets'
}
if ($DropAfterVerify -and $AllowProductionRestore) {
    throw '-DropAfterVerify cannot be combined with -AllowProductionRestore'
}

$resolvedBackup = (Resolve-Path -LiteralPath $BackupPath).Path
$checksumPath = "$resolvedBackup.sha256"
if (-not (Test-Path -LiteralPath $checksumPath)) {
    throw "Checksum file is required: $checksumPath"
}
$checksumLine = [System.IO.File]::ReadAllText($checksumPath).Trim()
if ($checksumLine -notmatch '^([0-9a-fA-F]{64})(?:\s|$)') {
    throw 'Checksum file is malformed'
}
$expectedHash = $Matches[1].ToLowerInvariant()
$actualHash = (Get-FileHash -LiteralPath $resolvedBackup -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualHash -cne $expectedHash) {
    throw "Backup checksum mismatch: expected $expectedHash, got $actualHash"
}

Push-Location $projectRoot
try {
    & docker compose config --quiet
    if ($LASTEXITCODE -ne 0) {
        throw 'docker compose configuration is invalid'
    }
    $productionDatabase = Get-ComposeValue 'POSTGRES_DB'
    $user = Get-ComposeValue 'POSTGRES_USER'
    $isProduction = $TargetDatabase -ceq $productionDatabase
    if ($isProduction -and -not $AllowProductionRestore) {
        throw "Refusing to replace the live database '$productionDatabase' without -AllowProductionRestore"
    }

    if ($isProduction) {
        [void](Invoke-Compose @('stop', 'api', 'postfix', 'pgbouncer'))
        $stoppedProduction = $true
    }

    Remove-Database -User $user -Database $TargetDatabase
    [void](Invoke-Compose @('exec', '-T', 'postgres', 'createdb', "--username=$user", $TargetDatabase))

    $process = New-RestoreProcess -User $user -Database $TargetDatabase
    $outputTask = $process.StandardOutput.ReadToEndAsync()
    $errorTask = $process.StandardError.ReadToEndAsync()
    try {
        $stream = [System.IO.File]::OpenRead($resolvedBackup)
        try {
            $stream.CopyTo($process.StandardInput.BaseStream)
            $process.StandardInput.Close()
        }
        finally {
            $stream.Dispose()
        }
        $process.WaitForExit()
        $stdout = $outputTask.GetAwaiter().GetResult()
        $stderr = $errorTask.GetAwaiter().GetResult()
        if ($process.ExitCode -ne 0) {
            throw "pg_restore failed with exit code $($process.ExitCode): $stderr $stdout"
        }
    }
    finally {
        $process.Dispose()
    }

    $tableCount = Invoke-Compose @(
        'exec', '-T', 'postgres', 'psql', "--username=$user", "--dbname=$TargetDatabase",
        '--no-psqlrc', '--tuples-only', '--no-align', '--set=ON_ERROR_STOP=1', '--command',
        "SELECT COUNT(*) FROM (VALUES (to_regclass('public.accounts')), (to_regclass('public.domains')), (to_regclass('public.mailboxes')), (to_regclass('public.emails')), (to_regclass('public.account_tokens')), (to_regclass('public.domain_donations'))) AS required(table_name) WHERE table_name IS NOT NULL;"
    )
    if ($tableCount.Trim() -ne '6') {
        throw "Restore verification failed: only $($tableCount.Trim()) of 6 required tables exist"
    }

    Write-Host "Restore verified in database: $TargetDatabase"
    Write-Host "SHA-256: $actualHash"

    if ($DropAfterVerify) {
        Remove-Database -User $user -Database $TargetDatabase
        Write-Host "Verification database removed: $TargetDatabase"
    }
}
finally {
    if ($stoppedProduction) {
        try {
            [void](Invoke-Compose @('up', '-d', 'pgbouncer', 'api', 'postfix'))
        }
        catch {
            Write-Error "Restore finished, but production services could not be restarted: $_"
        }
    }
    Pop-Location
}
