# Сборка клиента «Автолекции» в один exe.
param([switch]$Release, [string]$Version = "")

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot
New-Item -ItemType Directory -Force dist | Out-Null

$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$env:GOARCH = "amd64"

& (Join-Path $PSScriptRoot "scripts\versioninfo.ps1") -Version $Version

Write-Host "Сборка autolectures.exe..."
$ld = "-s -w -H windowsgui"
if ($Version) { $ld += " -X main.Version=$Version" }
go build -trimpath -ldflags $ld -o dist\autolectures.exe .
if ($LASTEXITCODE -ne 0) { throw "go build завершился с ошибкой" }

$size = [math]::Round((Get-Item dist\autolectures.exe).Length / 1MB, 1)
Write-Host "Готово: dist\autolectures.exe ($size МБ)"

if ($Release) {
    $relExe = "dist\autolectures-windows-x64.exe"
    Copy-Item dist\autolectures.exe $relExe -Force
    $hash = (Get-FileHash $relExe -Algorithm SHA256).Hash.ToLower()
    Set-Content -Path "$relExe.sha256" -Value $hash -Encoding ascii -NoNewline
    Write-Host "Файлы релиза: $relExe + .sha256"
    Write-Host "SHA-256: $hash"
}
