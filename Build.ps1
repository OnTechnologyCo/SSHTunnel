param([string]$OutputDir = (Join-Path $PSScriptRoot 'output'))
$ErrorActionPreference = 'Stop'
$windres = Get-Command windres.exe -ErrorAction SilentlyContinue
if (-not $windres -and (Test-Path 'C:\mingw64\bin\windres.exe')) { $windres = Get-Item 'C:\mingw64\bin\windres.exe' }
if (-not $windres) { throw 'MinGW windres.exe is required to embed the icon and Windows manifest.' }
$compiler = $windres.Source
if (-not $compiler) { $compiler = $windres.FullName }
$resource = Join-Path $PSScriptRoot 'cmd\sshtunnel\resource_windows_amd64.syso'
Push-Location (Join-Path $PSScriptRoot 'assets')
try {
    & $compiler --target pe-x86-64 -i server.rc -o $resource -O coff
    if ($LASTEXITCODE -ne 0) { throw 'Windows resource compilation failed.' }
} finally { Pop-Location }

Set-Location $PSScriptRoot
& go test ./... -count=1 -timeout 120s
if ($LASTEXITCODE -ne 0) { throw 'Tests failed.' }
& go vet ./...
if ($LASTEXITCODE -ne 0) { throw 'Static analysis failed.' }
New-Item -ItemType Directory -Path $OutputDir -Force | Out-Null
& go build -trimpath -ldflags '-s -w -H=windowsgui' -o (Join-Path $OutputDir 'SSHTunnelServer.exe') ./cmd/sshtunnel
if ($LASTEXITCODE -ne 0) { throw 'Build failed.' }
foreach ($name in @('Start-Server.cmd', 'Start-Server.vbs', 'Open-Admin.cmd', 'README.md')) {
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot $name) -Destination $OutputDir -Force
}
foreach ($name in @('scripts', 'docs')) {
    $destination = Join-Path $OutputDir $name
    New-Item -ItemType Directory -Path $destination -Force | Out-Null
    Copy-Item -Path (Join-Path $PSScriptRoot "$name\*") -Destination $destination -Force
}
$licenses = Join-Path $OutputDir 'licenses'
New-Item -ItemType Directory -Path $licenses -Force | Out-Null
$goRoot = (& go env GOROOT).Trim()
Copy-Item -LiteralPath (Join-Path $goRoot 'LICENSE') -Destination (Join-Path $licenses 'Go-LICENSE.txt') -Force
foreach ($module in @('golang.org/x/crypto', 'golang.org/x/sys')) {
    $moduleDir = (& go list -m -f '{{.Dir}}' $module).Trim()
    if ($LASTEXITCODE -ne 0) { throw "Cannot resolve license: $module" }
    $name = ($module -split '/')[-1]
    Copy-Item -LiteralPath (Join-Path $moduleDir 'LICENSE') -Destination (Join-Path $licenses "$name-LICENSE.txt") -Force
}
# SDK source bundle: embedding in an existing server requires no extra client EXE.
$sdk = Join-Path $OutputDir 'SDK'
New-Item -ItemType Directory -Path $sdk -Force | Out-Null
foreach ($name in @('pkg','examples')) {
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot $name) -Destination $sdk -Recurse -Force
}
foreach ($name in @('go.mod','go.sum')) {
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot $name) -Destination $sdk -Force
}
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'docs\TUNNEL-API.md') -Destination (Join-Path $sdk 'README.md') -Force
Compress-Archive -Path (Join-Path $sdk '*') -DestinationPath (Join-Path $OutputDir 'SSHTunnel-GoSDK-1.2.0.zip') -Force

$dist = Join-Path $PSScriptRoot 'dist'
New-Item -ItemType Directory -Path $dist -Force | Out-Null
# Include only distributable files. Never package a runtime data directory.
$files = @('SSHTunnelServer.exe','Start-Server.cmd','Start-Server.vbs','Open-Admin.cmd','README.md','scripts','docs','licenses','SDK') | ForEach-Object { Join-Path $OutputDir $_ }
$archive = Join-Path $dist 'SSHTunnelServer-windows-amd64.zip'
Compress-Archive -LiteralPath $files -DestinationPath $archive -Force
Write-Host "Built: $OutputDir"
Write-Host "Package: $archive"
Get-FileHash -LiteralPath (Join-Path $OutputDir 'SSHTunnelServer.exe') -Algorithm SHA256 | Format-List
