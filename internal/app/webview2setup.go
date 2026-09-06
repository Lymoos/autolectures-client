//go:build windows

package app

import (
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Lymoos/autolectures/client/internal/logger"
	"github.com/Lymoos/autolectures/client/internal/webview"
	"github.com/Lymoos/autolectures/client/internal/win"
)

const webview2BootstrapURL = "https://go.microsoft.com/fwlink/p/?LinkId=2124703"

func (a *App) newView(o webview.Options) (*webview.View, error) {
	v, err := webview.New(a.wnd, o)
	if err == nil {
		return v, nil
	}
	logger.Warnf(src, "WebView2 недоступен (%v), ставлю Runtime", err)
	if ierr := installWebView2(a.cfg.DataDir(), a.splash); ierr != nil {
		logger.Errorf(src, "Установка WebView2 не удалась: %v", ierr)
		a.wnd.HideSplash()
		win.MessageBox(a.wnd.HWnd, "Автолекции",
			"Не удалось установить Microsoft Edge WebView2 Runtime.\nПоставьте его вручную и запустите программу снова.")
		return nil, err
	}
	a.splash("Среда установлена, открываю приложение", 100)
	return webview.New(a.wnd, o)
}

func (a *App) splash(text string, percent int) {
	a.wnd.SetSplash("Автолекции", text, percent)
}

// Первый запуск на чистой системе: качаем и ставим Runtime, отчитываясь о
// прогрессе в окно — очередь сообщений ещё не крутится, поэтому перерисовку
// заставляем вручную.
func installWebView2(dataDir string, prog func(text string, percent int)) error {
	setup := filepath.Join(dataDir, "MicrosoftEdgeWebview2Setup.exe")
	prog("Загрузка компонентов Microsoft WebView2", 0)

	c := &http.Client{Timeout: 3 * time.Minute}
	resp, err := c.Get(webview2BootstrapURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("http " + resp.Status)
	}
	f, err := os.Create(setup)
	if err != nil {
		return err
	}
	err = copyWithProgress(f, resp.Body, resp.ContentLength, func(p int) {
		prog("Загрузка компонентов Microsoft WebView2", p)
	})
	f.Close()
	if err != nil {
		return err
	}
	defer os.Remove(setup)

	prog("Установка среды Microsoft WebView2 — это займёт пару минут", -1)
	cmd := exec.Command(setup, "/silent", "/install")
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	tick := time.NewTicker(60 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-tick.C:
			prog("Установка среды Microsoft WebView2 — это займёт пару минут", -1)
		}
	}
}

func copyWithProgress(dst io.Writer, src io.Reader, total int64, prog func(percent int)) error {
	buf := make([]byte, 64<<10)
	var done int64
	last := -1
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
			done += int64(n)
			p := -1
			if total > 0 {
				p = int(done * 100 / total)
			}
			if p != last {
				last = p
				prog(p)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
