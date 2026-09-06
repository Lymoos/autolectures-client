package update

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Lymoos/autolectures/client/internal/logger"
)

const (
	src         = "Обновление"
	repo        = "Lymoos/autolectures-client"
	releasesURL = "https:
	platformKey = "windows-x64"
)


type Updater struct {
	http    *http.Client
	version string
	dataDir string

	mu        sync.Mutex
	available bool
	latest    string
	notes     string
	assetURL  string
	assetSHA  string

	OnFound    func(version, notes string)
	OnProgress func(percent int)
	OnFailed   func(err string)
	OnRestart  func()
}


func New(h *http.Client, version, dataDir string) *Updater {
	return &Updater{http: h, version: version, dataDir: dataDir}
}

func (u *Updater) Current() string { return u.version }
func (u *Updater) Latest() string  { u.mu.Lock(); defer u.mu.Unlock(); return u.latest }
func (u *Updater) Available() bool { u.mu.Lock(); defer u.mu.Unlock(); return u.available }


func CompareVersions(a, b string) int {
	parts := func(v string) [3]int {
		var out [3]int
		v = strings.TrimPrefix(strings.TrimSpace(v), "v")
		for i, p := range strings.SplitN(v, ".", 3) {
			if i >= 3 {
				break
			}
			n, _ := strconv.Atoi(strings.SplitN(p, "-", 2)[0])
			out[i] = n
		}
		return out
	}
	pa, pb := parts(a), parts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func (u *Updater) get(url string, accept string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Autolectures/"+u.version)
	req.Header.Set("Cache-Control", "no-cache")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := u.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 200<<20))
}


func (u *Updater) Check() { u.checkReleases() }

func (u *Updater) checkReleases() {
	raw, err := u.get(releasesURL, "application/vnd.github+json")
	if err != nil {
		return
	}
	var rel struct {
		Tag    string `json:"tag_name"`
		Body   string `json:"body"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if json.Unmarshal(raw, &rel) != nil || rel.Tag == "" {
		return
	}
	assetURL, shaURL := "", ""
	for _, a := range rel.Assets {
		if !strings.Contains(a.Name, platformKey) {
			continue
		}
		if strings.HasSuffix(a.Name, ".zip") {
			assetURL = a.URL
		} else if strings.HasSuffix(a.Name, ".sha256") {
			shaURL = a.URL
		}
	}
	sha := ""
	if shaURL != "" {
		if raw, err := u.get(shaURL, ""); err == nil {
			sha = strings.ToLower(strings.Fields(string(raw) + " ")[0])
		}
	}
	latest := strings.TrimPrefix(rel.Tag, "v")
	u.mu.Lock()
	u.latest, u.notes, u.assetURL, u.assetSHA = latest, rel.Body, assetURL, sha
	found := CompareVersions(latest, u.version) > 0 && assetURL != ""
	u.available = found
	u.mu.Unlock()
	if found && u.OnFound != nil {
		u.OnFound(latest, rel.Body)
	}
}


func (u *Updater) DownloadAndInstall() {
	u.mu.Lock()
	url, sha, latest, ok := u.assetURL, u.assetSHA, u.latest, u.available
	u.mu.Unlock()
	if !ok || url == "" {
		return
	}
	fail := func(msg string) {
		logger.Errorf(src, "%s", msg)
		if u.OnFailed != nil {
			u.OnFailed(msg)
		}
	}
	logger.Infof(src, "Скачиваю %s", url)
	if u.OnProgress != nil {
		u.OnProgress(10)
	}
	data, err := u.get(url, "")
	if err != nil {
		fail("Ошибка загрузки: " + err.Error())
		return
	}
	if sha != "" {
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != sha {
			fail("Контрольная сумма архива не совпадает — обновление отменено")
			return
		}
	}
	if u.OnProgress != nil {
		u.OnProgress(70)
	}
	exeBytes, err := extractExe(data)
	if err != nil {
		fail(err.Error())
		return
	}
	self, err := os.Executable()
	if err != nil {
		fail("Не удалось определить путь к приложению")
		return
	}
	staging := filepath.Join(u.dataDir, "update")
	_ = os.MkdirAll(staging, 0o755)
	newExe := filepath.Join(staging, "autolectures-"+latest+".exe")
	if err := os.WriteFile(newExe, exeBytes, 0o755); err != nil {
		fail("Не удалось сохранить обновление: " + err.Error())
		return
	}
	if u.OnProgress != nil {
		u.OnProgress(95)
	}

	helper := filepath.Join(staging, "apply-"+latest+".exe")
	if err := copyFile(self, helper); err != nil {
		fail("Не удалось подготовить модуль обновления: " + err.Error())
		return
	}
	cmd := exec.Command(helper, "--apply-update", strconv.Itoa(os.Getpid()), newExe, self)
	if err := cmd.Start(); err != nil {
		fail("Не удалось запустить модуль обновления: " + err.Error())
		return
	}
	logger.Infof(src, "Обновление подготовлено, перезапуск приложения")
	if u.OnProgress != nil {
		u.OnProgress(100)
	}
	if u.OnRestart != nil {
		u.OnRestart()
	}
}

func extractExe(zipData []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, errors.New("Архив обновления повреждён")
	}
	var best *zip.File
	for _, f := range zr.File {
		name := strings.ToLower(filepath.Base(f.Name))
		if strings.HasSuffix(name, ".exe") && !strings.Contains(name, "apply") && (best == nil || strings.HasPrefix(name, "autolectures")) {
			best = f
		}
	}
	if best == nil {
		return nil, errors.New("В архиве обновления нет исполняемого файла")
	}
	rc, err := best.Open()
	if err != nil {
		return nil, errors.New("Не удалось распаковать обновление")
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o755)
}


func Apply(args []string) int {
	if len(args) < 3 {
		return 2
	}
	pid, _ := strconv.Atoi(args[0])
	newExe, target := args[1], args[2]
	logPath := filepath.Join(filepath.Dir(newExe), "update.log")
	log := func(s string) {
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), s)
			f.Close()
		}
	}
	log(fmt.Sprintf("ожидание завершения процесса %d", pid))
	for i := 0; i < 120 && processAlive(pid); i++ {
		time.Sleep(500 * time.Millisecond)
	}
	var lastErr error
	for i := 0; i < 30; i++ {
		if lastErr = copyFile(newExe, target); lastErr == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if lastErr != nil {
		log("не удалось заменить файл: " + lastErr.Error())
		_ = exec.Command(target).Start()
		return 1
	}
	_ = os.Remove(newExe)
	log("файл заменён, запуск новой версии")
	cmd := exec.Command(target)
	cmd.Dir = filepath.Dir(target)
	_ = cmd.Start()
	return 0
}

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	_ = p.Release()
	return true
}

