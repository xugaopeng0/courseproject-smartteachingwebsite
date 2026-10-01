$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$configPath = Join-Path $projectRoot '.env'
if (Test-Path -LiteralPath $configPath) {
    Write-Host '.env already exists; existing credentials were preserved.'
    exit 0
}
function New-LocalSecret {
    $bytes = New-Object byte[] 24
    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    $rng.GetBytes($bytes)
    $rng.Dispose()
    return ([BitConverter]::ToString($bytes)).Replace('-', '').ToLowerInvariant()
}
$dbSecret = New-LocalSecret
$dbRootSecret = New-LocalSecret
$adminSecret = New-LocalSecret
$content = @"
DB_PASSWORD=$dbSecret
DB_ROOT_PASSWORD=$dbRootSecret
ADMIN_USERNAME=admin
ADMIN_PASSWORD=$adminSecret
APP_ORIGIN=http://localhost:8080
GOPROXY=https://goproxy.cn,direct
"@
[System.IO.File]::WriteAllText($configPath, $content, (New-Object System.Text.UTF8Encoding $false))
Write-Host 'Created .env with random local credentials. Administrator username: admin. Read ADMIN_PASSWORD in .env to sign in.'
Write-Host 'Next: docker compose up --build -d'
