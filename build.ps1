# Сборка клиента «Автолекции» в один exe.
param([switch]$Zip, [string]$Version = "")

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot
New-Item -ItemType Directory -Force dist | Out-Null

$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$env:GOARCH = "amd64"

Write-Host "Сборка autolectures.exe..."
$ld = "-s -w -H windowsgui"
if ($Version) { $ld += " -X main.Version=$Version" }
go build -trimpath -ldflags $ld -o dist\autolectures.exe .
if ($LASTEXITCODE -ne 0) { throw "go build завершился с ошибкой" }

$size = [math]::Round((Get-Item dist\autolectures.exe).Length / 1MB, 1)
Write-Host "Готово: dist\autolectures.exe ($size МБ)"

if ($Zip) {
    # не $zip: совпадёт с параметром -Zip
    $zipPath = "dist\autolectures-windows-x64.zip"
    if (Test-Path $zipPath) { Remove-Item $zipPath }
    Compress-Archive -Path dist\autolectures.exe -DestinationPath $zipPath
    $hash = (Get-FileHash $zipPath -Algorithm SHA256).Hash.ToLower()
    Set-Content -Path "$zipPath.sha256" -Value $hash -Encoding ascii
    Write-Host "Архив: $zipPath"
    Write-Host "SHA-256: $hash"
}
