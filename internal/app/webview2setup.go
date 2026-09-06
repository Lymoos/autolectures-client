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
	if ierr := installWebView2(a.cfg.DataDir()); ierr != nil {
		logger.Errorf(src, "Установка WebView2 не удалась: %v", ierr)
		win.MessageBox(a.wnd.HWnd, "Автолекции",
			"Не удалось установить Microsoft Edge WebView2 Runtime.\nПоставьте его вручную и запустите программу снова.")
		return nil, err
	}
	return webview.New(a.wnd, o)
}

func installWebView2(dataDir string) error {
	setup := filepath.Join(dataDir, "MicrosoftEdgeWebview2Setup.exe")
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
	_, err = io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		return err
	}
	defer os.Remove(setup)
	cmd := exec.Command(setup, "/silent", "/install")
	return cmd.Run()
}
