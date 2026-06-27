$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$dist = Join-Path $root "dist"
New-Item -ItemType Directory -Force -Path $dist | Out-Null
Remove-Item -LiteralPath (Join-Path $dist "UVPrinterLogMonitor.exe") -Force -ErrorAction SilentlyContinue
Remove-Item -Path (Join-Path $dist "UVPrinterLogMonitor_Stable_*.exe") -Force -ErrorAction SilentlyContinue

$go = "go"
if (Test-Path (Join-Path $root "..\tools\go\bin\go.exe")) {
    $go = (Resolve-Path (Join-Path $root "..\tools\go\bin\go.exe")).Path
}

Push-Location $root
try {
    & $go test ./...

    $common = "-H windowsgui -s -w"
    & $go build -o (Join-Path $dist "UVPrinterLogMonitor.exe") -ldflags="$common -X main.buildVersion=1.1.0 -X main.uiVariant=classic" .
    Copy-Item -Path (Join-Path $dist "UVPrinterLogMonitor.exe") -Destination (Join-Path $dist "UVPrinterLogMonitor_Stable_1.1.0.exe") -Force
}
finally {
    Pop-Location
}

Write-Host "Build complete:"
Get-ChildItem $dist -Filter *.exe | Select-Object FullName,Length,LastWriteTime
