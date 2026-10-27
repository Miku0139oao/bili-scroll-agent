$ErrorActionPreference = "Stop"
$repo = Split-Path -Parent $PSScriptRoot
Set-Location $repo

if (-not (Test-Path ".env")) {
    Copy-Item ".env.example" ".env"
    Write-Host "Created .env from .env.example. Review settings before production use."
}

Push-Location "web"
try {
    bun install --frozen-lockfile
    bun run build
} finally {
    Pop-Location
}

New-Item -ItemType Directory -Force -Path ".\internal\webui\dist", ".\bin", `
    ".\plugins\bili-scroll-agent\bin" | Out-Null
Get-ChildItem -LiteralPath ".\internal\webui\dist" -Force |
    Where-Object { $_.Name -ne ".gitkeep" } | Remove-Item -Recurse -Force
Copy-Item -Path ".\web\dist\*" -Destination ".\internal\webui\dist" -Recurse -Force

go build -trimpath -o ".\bin\bili-scroll-server.exe" ".\cmd\bili-scroll-server"
go build -trimpath -o ".\bin\bili-scroll-usage-sync.exe" ".\cmd\bili-scroll-usage-sync"
go build -trimpath -o ".\plugins\bili-scroll-agent\bin\bili-scroll-mcp.exe" ".\cmd\bili-scroll-mcp"
Copy-Item -LiteralPath ".\plugins\bili-scroll-agent\bin\bili-scroll-mcp.exe" `
  -Destination ".\plugins\bili-scroll-agent\bin\bili-scroll-mcp" -Force

& ".\bin\bili-scroll-server.exe" serve
