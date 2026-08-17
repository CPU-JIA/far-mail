[CmdletBinding()]
param(
    [string]$OutputDirectory = (Join-Path (Split-Path -Parent $PSScriptRoot) 'backups')
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$projectRoot = Split-Path -Parent $PSScriptRoot
$utf8NoBom = [System.Text.UTF8Encoding]::new($false)

function Get-ComposeValue {
    param([Parameter(Mandatory)][string]$Name)
    $value = & docker compose exec -T postgres printenv $Name 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Unable to read $Name from the PostgreSQL container: $($value -join [Environment]::NewLine)"
    }
    return (($value -join "`n").Trim())
}

function New-DockerProcess {
    param([Parameter(Mandatory)][string[]]$Arguments)
    $startInfo = [System.Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = 'docker'
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    foreach ($argument in $Arguments) {
        [void]$startInfo.ArgumentList.Add($argument)
    }
    return [System.Diagnostics.Process]::Start($startInfo)
}

Push-Location $projectRoot
try {
    & docker compose config --quiet
    if ($LASTEXITCODE -ne 0) {
        throw 'docker compose configuration is invalid'
    }

    $database = Get-ComposeValue 'POSTGRES_DB'
    $user = Get-ComposeValue 'POSTGRES_USER'
    if ($database -notmatch '^[A-Za-z0-9_]+$' -or $user -notmatch '^[A-Za-z0-9_.-]+$') {
        throw 'PostgreSQL database or user contains unsupported characters'
    }

    [void][System.IO.Directory]::CreateDirectory($OutputDirectory)
    $resolvedOutput = (Resolve-Path $OutputDirectory).Path
    $timestamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssZ')
    $fileName = "far-mail-$database-$timestamp.dump"
    $backupPath = Join-Path $resolvedOutput $fileName
    $partialPath = "$backupPath.partial"

    $process = New-DockerProcess @(
        'compose', 'exec', '-T', 'postgres',
        'pg_dump', "--username=$user", "--dbname=$database",
        '--format=custom', '--compress=6', '--no-owner', '--no-privileges'
    )
    $errorTask = $process.StandardError.ReadToEndAsync()
    try {
        $stream = [System.IO.File]::Open($partialPath, [System.IO.FileMode]::CreateNew, [System.IO.FileAccess]::Write, [System.IO.FileShare]::None)
        try {
            $process.StandardOutput.BaseStream.CopyTo($stream)
        }
        finally {
            $stream.Dispose()
        }
        $process.WaitForExit()
        $stderr = $errorTask.GetAwaiter().GetResult()
        if ($process.ExitCode -ne 0) {
            throw "pg_dump failed with exit code $($process.ExitCode): $stderr"
        }
        Move-Item -LiteralPath $partialPath -Destination $backupPath
    }
    catch {
        if (Test-Path -LiteralPath $partialPath) {
            Remove-Item -LiteralPath $partialPath -Force
        }
        throw
    }
    finally {
        $process.Dispose()
    }

    $hash = (Get-FileHash -LiteralPath $backupPath -Algorithm SHA256).Hash.ToLowerInvariant()
    $checksumPath = "$backupPath.sha256"
    [System.IO.File]::WriteAllText($checksumPath, "$hash  $fileName`n", $utf8NoBom)

    $manifest = [ordered]@{
        format = 'postgresql-custom'
        database = $database
        created_at = [DateTime]::UtcNow.ToString('o')
        file = $fileName
        bytes = (Get-Item -LiteralPath $backupPath).Length
        sha256 = $hash
        redis_included = $false
    } | ConvertTo-Json
    [System.IO.File]::WriteAllText("$backupPath.json", "$manifest`n", $utf8NoBom)

    Write-Host "Backup created: $backupPath"
    Write-Host "SHA-256: $hash"
}
finally {
    Pop-Location
}
