package main

import (
	"embed"
	"os"
	"sort"
	"strings"

	"github.com/Lymoos/autolectures/client/internal/app"
	"github.com/Lymoos/autolectures/client/internal/update"
)

var Version = "1.0.1"

var webFS embed.FS

var scriptsFS embed.FS

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
	for _, a := range args {
		if a == "--smoke" {
			smoke = true
		}
	}

	order := []string{"bridge.js", "jsQR.js", "qrscan.js", "antiafk.js", "autojoin.js", "volume.js", "mediadiag.js"}
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
	}, app.Options{Version: Version, Smoke: smoke})
	os.Exit(code)
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
