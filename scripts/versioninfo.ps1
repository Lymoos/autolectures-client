# Готовит resource_windows_amd64.syso: иконку, манифест и свойства файла.
# Без них Проводник показывает безымянный exe без издателя, а SmartScreen
# ругается ещё охотнее. Версия подставляется та же, что уходит в -X main.Version.
param([string]$Version = "")

$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")

$argv = @("-64", "-o", "resource_windows_amd64.syso")
if ($Version) {
    $p = ($Version.TrimStart("v") -split "[.\-+]")
    $ma = [int]$p[0]; $mi = 0; $pa = 0
    if ($p.Count -gt 1) { $mi = [int]$p[1] }
    if ($p.Count -gt 2) { $pa = [int]$p[2] }
    $argv += @(
        "-ver-major", $ma, "-ver-minor", $mi, "-ver-patch", $pa, "-ver-build", 0,
        "-product-ver-major", $ma, "-product-ver-minor", $mi, "-product-ver-patch", $pa, "-product-ver-build", 0,
        "-file-version", "$ma.$mi.$pa.0", "-product-version", "$ma.$mi.$pa.0"
    )
}
$argv += "versioninfo.json"

$exe = Join-Path (go env GOPATH) "bin\goversioninfo.exe"
if (Test-Path $exe) {
    & $exe @argv
} else {
    go run github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.4.1 @argv
}
if ($LASTEXITCODE -ne 0) { throw "goversioninfo завершился с ошибкой" }
Write-Host "Ресурсы собраны: resource_windows_amd64.syso"
