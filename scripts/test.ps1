$ErrorActionPreference = "Stop"
$repo = Split-Path -Parent $PSScriptRoot
Set-Location $repo

New-Item -ItemType Directory -Force -Path ".\internal\webui\dist", ".\bin", `
    ".\plugins\bili-scroll-agent\bin" | Out-Null
# The plugin binary is a build artifact, so build it before go test and make the
# manifest test strict. Neither artifact is tracked in git.
go build -trimpath -o ".\plugins\bili-scroll-agent\bin\bili-scroll-mcp.exe" ".\cmd\bili-scroll-mcp"

if (-not (Test-Path ".\internal\webui\dist\index.html")) {
    Write-Host "WebUI bundle is not built; server tests will report it as unavailable."
}

$env:BILI_SCROLL_REQUIRE_PLUGIN_BINARY = "1"
try {
    go test ./...
    go vet ./...
} finally {
    Remove-Item Env:\BILI_SCROLL_REQUIRE_PLUGIN_BINARY
}

Push-Location "web"
try {
    bun test
    bun audit
} finally {
    Pop-Location
}
