$ErrorActionPreference = "Stop"
$repo = Split-Path -Parent $PSScriptRoot
Set-Location $repo

Push-Location "web"
try {
    bun install --frozen-lockfile
    bun test
    bun run build
} finally {
    Pop-Location
}

New-Item -ItemType Directory -Force -Path ".\internal\webui\dist", ".\bin", `
    ".\plugins\bili-scroll-agent\bin" | Out-Null
Get-ChildItem -LiteralPath ".\internal\webui\dist" -Force |
    Where-Object { $_.Name -ne ".gitkeep" } | Remove-Item -Recurse -Force
Copy-Item -Path ".\web\dist\*" -Destination ".\internal\webui\dist" -Recurse -Force

go build -trimpath -ldflags="-s -w" -o ".\bin\bili-scroll-server.exe" ".\cmd\bili-scroll-server"
go build -trimpath -ldflags="-s -w" -o ".\bin\bili-scroll-usage-sync.exe" ".\cmd\bili-scroll-usage-sync"
go build -trimpath -ldflags="-s -w" -o ".\plugins\bili-scroll-agent\bin\bili-scroll-mcp.exe" ".\cmd\bili-scroll-mcp"
Copy-Item -LiteralPath ".\plugins\bili-scroll-agent\bin\bili-scroll-mcp.exe" `
    -Destination ".\plugins\bili-scroll-agent\bin\bili-scroll-mcp" -Force

Write-Host "Built Go server, usage sync, WebUI, and local MCP plugin binaries."
