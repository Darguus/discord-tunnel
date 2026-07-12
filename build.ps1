#Requires -Version 5.1
<#
.SYNOPSIS
    Builds DiscordTunnel.exe into .\build.

.DESCRIPTION
    Produces a single self-contained executable. The only file that must sit
    next to it at runtime is wintun.dll, the driver that provides the virtual
    network adapter; this script copies it in if it can find one, and tells you
    where to get it if it cannot.
#>
[CmdletBinding()]
param(
    # Strip debug info and the symbol table. Roughly halves the binary.
    [switch]$Release
)

$ErrorActionPreference = 'Stop'
Set-Location -Path $PSScriptRoot

$outDir = Join-Path $PSScriptRoot 'build'
$outExe = Join-Path $outDir 'DiscordTunnel.exe'
New-Item -ItemType Directory -Force -Path $outDir | Out-Null

# sing-box gates whole features behind build tags. Without these the binary
# compiles but fails at runtime: with_utls is what REALITY needs to look like a
# real browser, with_gvisor is the userspace network stack the TUN adapter uses,
# and with_quic lets the sniffer see Discord's QUIC traffic. This list is the
# minimum for this app — sing-box's own default set is far larger.
$tags = 'with_gvisor,with_utls,with_quic'

# -H=windowsgui suppresses the console window. Without it, the tray app would
# park a black rectangle on the taskbar for its entire life.
$ldflags = '-H=windowsgui'
if ($Release) { $ldflags = "$ldflags -s -w" }

Write-Host 'Running tests...' -ForegroundColor Cyan
go test -tags $tags ./...
if ($LASTEXITCODE -ne 0) { throw 'tests failed - not building' }

Write-Host 'Building...' -ForegroundColor Cyan
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'
go build -trimpath -tags $tags -ldflags $ldflags -o $outExe ./cmd/discord-tunnel
if ($LASTEXITCODE -ne 0) { throw 'build failed' }

# wintun.dll is licensed separately and is not committed to this repository.
$wintun = Join-Path $outDir 'wintun.dll'
if (-not (Test-Path $wintun)) {
    $candidates = @(
        (Join-Path $PSScriptRoot 'wintun.dll'),
        "$env:USERPROFILE\xray-discord\wintun.dll"
    )
    $found = $candidates | Where-Object { Test-Path $_ } | Select-Object -First 1
    if ($found) {
        Copy-Item $found $wintun
        Write-Host "Copied wintun.dll from $found" -ForegroundColor DarkGray
    }
    else {
        Write-Warning 'wintun.dll not found. Download the amd64 build from https://www.wintun.net and place it next to DiscordTunnel.exe.'
    }
}

$size = [math]::Round((Get-Item $outExe).Length / 1MB, 1)
Write-Host "Built $outExe ($size MB)" -ForegroundColor Green
