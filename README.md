# Автолекции — клиент

Один exe без установки. Интерфейс и трансляция крутятся во встроенном Edge (WebView2), поэтому видео MTS-Link играет без плясок с кодеками. Если WebView2 Runtime вдруг нет, клиент сам его докачает.

## Сборка

Нужен Go 1.23+:

```powershell
.\build.ps1                  # dist\autolectures.exe
.\build.ps1 -Zip -Version 2.0.1
```

## Релиз

Пуш тега `v*` — GitHub Actions собирает exe и прикладывает `autolectures-windows-x64.zip` + `.sha256` к релизу. Клиент сам находит новый релиз и обновляется.

## Данные

`%LOCALAPPDATA%\Autolectures\` — config.json, профиль браузера, logs\app.log.

`autolectures.exe --smoke` — проверочный запуск: поднять всё и выйти.
