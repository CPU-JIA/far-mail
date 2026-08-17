[CmdletBinding()]
param(
    [switch]$SkipDocker,
    [switch]$RequireRunning
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot

function Invoke-Checked([string]$Command, [string[]]$Arguments, [string]$WorkingDirectory) {
    Push-Location $WorkingDirectory
    try {
        & $Command @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "$Command failed with exit code $LASTEXITCODE"
        }
    } finally {
        Pop-Location
    }
}

Write-Host 'Go tests'
Invoke-Checked 'go' @('test', './...') (Join-Path $root 'api')
Write-Host 'Go vet'
Invoke-Checked 'go' @('vet', './...') (Join-Path $root 'api')
Write-Host 'Go race tests'
Invoke-Checked 'go' @('test', '-race', './...') (Join-Path $root 'api')

Write-Host 'Frontend typecheck and production build'
Invoke-Checked 'npm' @('run', 'typecheck') (Join-Path $root 'frontend')
Invoke-Checked 'npm' @('run', 'build') (Join-Path $root 'frontend')

if (-not $SkipDocker) {
    Write-Host 'Docker Compose configuration'
    Invoke-Checked 'docker' @('compose', 'config', '--quiet') $root
    if ($RequireRunning) {
        $status = docker compose ps --services --filter 'status=running'
        $required = @('api', 'frontend', 'postfix', 'postgres', 'pgbouncer', 'redis')
        foreach ($service in $required) {
            if ($status -notcontains $service) {
                throw "required service is not running: $service"
            }
        }
    }
}

Write-Host 'Verification passed.'
