package main

import (
	"embed"
	"encoding/base64"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Lymoos/autolectures/client/internal/app"
	"github.com/Lymoos/autolectures/client/internal/config"
	"github.com/Lymoos/autolectures/client/internal/update"
)

var Version = "1.3.0"

//go:embed web/index.html web/app.css web/app.js
var webFS embed.FS

//go:embed scripts/*.js
var scriptsFS embed.FS

//go:embed assets/deko.jpg
var assetsFS embed.FS

func read(fs embed.FS, name string) string {
	b, err := fs.ReadFile(name)
	if err != nil {
		return "/* " + name + " не найден */"
	}
	return string(b)
}

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "--apply-update" {
		os.Exit(update.Apply(args[1:]))
	}
	smoke := false
	for i, a := range args {
		if a == "--smoke" {
			smoke = true
		}
		// Сброс данных запускает сам клиент: дожидаемся выхода прежнего процесса,
		// иначе профиль браузера ещё занят.
		if a == "--reset" && i+1 < len(args) {
			waitForExit(args[i+1])
			_ = config.WipeStorage()
		}
	}

	order := []string{"bridge.js", "jsQR.js", "qrscan.js", "antiafk.js", "autojoin.js", "popups.js", "participants.js", "nomic.js", "volume.js", "mediadiag.js"}
	var scripts []string
	for _, name := range order {
		scripts = append(scripts, read(scriptsFS, "scripts/"+name))
	}
	if entries, err := scriptsFS.ReadDir("scripts"); err == nil {
		var extra []string
		for _, e := range entries {
			n := e.Name()
			if !strings.HasSuffix(n, ".js") || contains(order, n) {
				continue
			}
			extra = append(extra, n)
		}
		sort.Strings(extra)
		for _, n := range extra {
			scripts = append(scripts, read(scriptsFS, "scripts/"+n))
		}
	}

	code := app.Run(app.Assets{
		IndexHTML: read(webFS, "web/index.html"),
		CSS:       read(webFS, "web/app.css"),
		JS:        read(webFS, "web/app.js"),
		Scripts:   scripts,
		Deko:      dataURI(assetsFS, "assets/deko.jpg", "image/jpeg"),
	}, app.Options{Version: Version, Smoke: smoke})
	os.Exit(code)
}

// dataURI встраивает картинку прямо в стили: отдельных файлов рядом с exe нет.
func dataURI(fs embed.FS, name, mime string) string {
	b, err := fs.ReadFile(name)
	if err != nil {
		return ""
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
}

func waitForExit(pidStr string) {
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 0 {
		return
	}
	for i := 0; i < 80; i++ {
		if p, err := os.FindProcess(pid); err != nil || p == nil {
			return
		} else {
			_ = p.Release()
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
