package main

// Обновление самой программы из релизов GitHub.
//
// При запуске программа смотрит последний релиз репозитория. Если он новее,
// в окне появляется кнопка «Обновить»: exe качается рядом (.new), текущий
// переименовывается в .old (запущенный exe переименовать можно, удалить нельзя),
// новый ставится на его место и запускается с тем же токеном окна. Старый
// процесс выходит, новый дожидается этого и продолжает (и подключает VPN,
// если он был включён). Права администратора наследуются, UAC не спрашивается.

import (
	"context"
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

	"golang.org/x/sys/windows"
)

const (
	updateRepo  = "Trashfallen/mihomo-desk"
	updateAsset = "MihomoDesk.exe"
)

var (
	updateAPI    = "https://api.github.com/repos/" + updateRepo + "/releases/latest"
	noElevateRun bool // запущены с --no-elevate: так же перезапускаемся после обновления
)

type AppUpdate struct {
	Checked   bool    `json:"checked"`
	Available bool    `json:"available"`
	Version   string  `json:"version"`
	Notes     string  `json:"notes"`
	URL       string  `json:"url"` // страница релиза
	Busy      bool    `json:"busy"`
	Progress  float64 `json:"progress"` // 0..1, -1 - размер неизвестен
	Message   string  `json:"message"`
	Error     string  `json:"error"`
}

type Updater struct {
	mu    sync.Mutex
	st    AppUpdate
	asset string // ссылка на exe в релизе
	size  int64
}

func (u *Updater) State() AppUpdate {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.st
}

func (u *Updater) set(fn func(*AppUpdate)) {
	u.mu.Lock()
	fn(&u.st)
	u.mu.Unlock()
}

// newerVersion: «v1.2.0» новее «1.1.9».
func newerVersion(tag, cur string) bool {
	parse := func(s string) []int {
		s = strings.TrimPrefix(strings.TrimSpace(s), "v")
		s = strings.SplitN(s, "-", 2)[0]
		var out []int
		for _, p := range strings.Split(s, ".") {
			n, err := strconv.Atoi(p)
			if err != nil {
				return nil
			}
			out = append(out, n)
		}
		return out
	}
	a, b := parse(tag), parse(cur)
	if a == nil || b == nil {
		return false
	}
	for i := 0; i < max(len(a), len(b)); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// Check смотрит последний релиз. Нет релизов - не ошибка, просто нечего ставить.
func (u *Updater) Check(ctx context.Context) error {
	u.set(func(s *AppUpdate) { s.Error, s.Message = "", "" })
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", updateAPI, nil)
	req.Header.Set("User-Agent", appName+"/"+appVersion)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		err = fmt.Errorf("GitHub недоступен: %w", err)
		u.set(func(s *AppUpdate) { s.Checked, s.Error = true, err.Error() })
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		u.set(func(s *AppUpdate) { s.Checked, s.Available, s.Message = true, false, "Релизов пока нет" })
		return nil
	}
	if resp.StatusCode != 200 {
		err := fmt.Errorf("GitHub ответил %s", resp.Status)
		u.set(func(s *AppUpdate) { s.Checked, s.Error = true, err.Error() })
		return err
	}
	var rel struct {
		Tag    string `json:"tag_name"`
		Body   string `json:"body"`
		Page   string `json:"html_url"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		err = fmt.Errorf("не разобрал ответ GitHub: %w", err)
		u.set(func(s *AppUpdate) { s.Checked, s.Error = true, err.Error() })
		return err
	}
	asset, size := "", int64(0)
	for _, a := range rel.Assets {
		if strings.EqualFold(a.Name, updateAsset) {
			asset, size = a.URL, a.Size
		}
	}
	avail := asset != "" && newerVersion(rel.Tag, appVersion)
	u.mu.Lock()
	u.asset, u.size = asset, size
	u.st.Checked, u.st.Available, u.st.Version, u.st.Notes, u.st.URL = true, avail, rel.Tag, rel.Body, rel.Page
	if !avail {
		u.st.Message = "Установлена последняя версия"
	}
	u.mu.Unlock()
	return nil
}

// download качает exe релиза в path и проверяет, что это программа Windows.
func (u *Updater) download(ctx context.Context, path string) error {
	u.mu.Lock()
	url, size := u.asset, u.size
	u.mu.Unlock()
	if url == "" {
		return errors.New("в релизе нет " + updateAsset)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", appName+"/"+appVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("не удалось скачать: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("скачивание: %s", resp.Status)
	}
	if resp.ContentLength > 0 {
		size = resp.ContentLength
	}
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	var got int64
	buf := make([]byte, 64<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				out.Close()
				return err
			}
			got += int64(n)
			if size > 0 {
				p := float64(got) / float64(size)
				u.set(func(s *AppUpdate) { s.Progress = p })
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			return fmt.Errorf("обрыв при скачивании: %w", rerr)
		}
	}
	if err := out.Close(); err != nil {
		return err
	}
	head := make([]byte, 2)
	if f, err := os.Open(path); err == nil {
		_, _ = io.ReadFull(f, head)
		f.Close()
	}
	if got < 1<<20 || string(head) != "MZ" {
		return errors.New("скачанный файл не похож на программу")
	}
	return nil
}

// installAppUpdate скачивает новую версию, ставит её на место текущей
// и перезапускает программу.
func (a *App) installAppUpdate() {
	u := a.updater
	u.mu.Lock()
	if u.st.Busy {
		u.mu.Unlock()
		return
	}
	u.st.Busy, u.st.Error, u.st.Progress, u.st.Message = true, "", -1, "Скачиваю обновление"
	u.mu.Unlock()
	fail := func(err error) {
		a.logs.Add("app", "error", "Обновление: "+err.Error())
		u.set(func(s *AppUpdate) { s.Busy, s.Error, s.Message = false, err.Error(), "" })
	}

	exe, err := os.Executable()
	if err != nil {
		fail(err)
		return
	}
	newExe, oldExe := exe+".new", exe+".old"
	if err := u.download(context.Background(), newExe); err != nil {
		os.Remove(newExe)
		fail(err)
		return
	}
	u.set(func(s *AppUpdate) { s.Message, s.Progress = "Устанавливаю", 1 })
	os.Remove(oldExe)
	if err := os.Rename(exe, oldExe); err != nil {
		os.Remove(newExe)
		fail(fmt.Errorf("не удалось заменить программу: %w", err))
		return
	}
	if err := os.Rename(newExe, exe); err != nil {
		_ = os.Rename(oldExe, exe)
		fail(fmt.Errorf("не удалось заменить программу: %w", err))
		return
	}

	args := []string{"--after-update", strconv.Itoa(os.Getpid()), "--token", a.token, "--no-window"}
	if st := a.core.Status(); st == stRunning || st == stStarting {
		args = append(args, "--connect")
	}
	if noElevateRun {
		args = append(args, "--no-elevate")
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = filepath.Dir(exe)
	if err := cmd.Start(); err != nil {
		_ = os.Rename(exe, newExe)
		_ = os.Rename(oldExe, exe)
		fail(fmt.Errorf("новая версия не запустилась: %w", err))
		return
	}
	a.logs.Add("app", "info", "Обновление установлено, перезапускаюсь: "+u.State().Version)
	u.set(func(s *AppUpdate) { s.Message = "Перезапускаюсь" })
	time.Sleep(300 * time.Millisecond) // окно успеет увидеть статус
	a.quit()
}

// waitForExit ждёт завершения старой копии после обновления.
func waitForExit(pid int, timeout time.Duration) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return // уже вышла
	}
	defer windows.CloseHandle(h)
	_, _ = windows.WaitForSingleObject(h, uint32(timeout.Milliseconds()))
}

// cleanupOldExe убирает MihomoDesk.exe.old/.new после обновления.
func cleanupOldExe() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	for i := 0; i < 20; i++ {
		errOld := os.Remove(exe + ".old")
		if errOld == nil || errors.Is(errOld, os.ErrNotExist) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	_ = os.Remove(exe + ".new")
}
