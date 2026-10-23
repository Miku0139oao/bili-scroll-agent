$ErrorActionPreference = "Stop"
$repo = Split-Path -Parent $PSScriptRoot
Set-Location $repo

if (-not (Test-Path ".env")) {
    Copy-Item ".env.example" ".env"
    Write-Host "Created .env from .env.example. Review settings before production use."
}

uv sync --extra dev

Push-Location "web"
try {
    bun install --frozen-lockfile
    bun run build
} finally {
    Pop-Location
}

uv run bili-scroll serve
