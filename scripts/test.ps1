$ErrorActionPreference = "Stop"
$repo = Split-Path -Parent $PSScriptRoot
Set-Location $repo

go test ./...
go vet ./...
Push-Location "web"
try {
    bun test
    bun audit
} finally {
    Pop-Location
}
