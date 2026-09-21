package main

// Скачивание ядра mihomo с официального GitHub (MetaCubeX/mihomo).

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

const releasesAPI = "https://api.github.com/repos/MetaCubeX/mihomo/releases/latest"

// compatible - сборка без требований к набору инструкций CPU, работает везде.
var coreAssetRes = []*regexp.Regexp{
	regexp.MustCompile(`^mihomo-windows-amd64-compatible-v[\d.]+\.zip$`),
	regexp.MustCompile(`^mihomo-windows-amd64-v1-v[\d.]+\.zip$`),
	regexp.MustCompile(`^mihomo-windows-amd64-v[\d.]+\.zip$`),
}

// ErrUpToDate - на GitHub та же версия, что уже стоит.
var ErrUpToDate = errors.New("уже последняя версия")

type DownloadState struct {
	Active   bool    `json:"active"`
	Progress float64 `json:"progress"` // 0..1, -1 если размер неизвестен
	Message  string  `json:"message"`
	Error    string  `json:"error"`
}

type Fetcher struct {
	mu sync.Mutex
	st DownloadState
}

func (f *Fetcher) State() DownloadState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st
}

func (f *Fetcher) set(fn func(*DownloadState)) {
	f.mu.Lock()
	fn(&f.st)
	f.mu.Unlock()
}

// Download качает последнюю версию ядра в dst+".new" и возвращает путь к нему.
// Если current совпадает с последней версией, ничего не качает и вернёт ErrUpToDate.
// Установка (замена файла) - забота вызывающего: ядро может быть запущено.
func (f *Fetcher) Download(ctx context.Context, dst, current string) (string, error) {
	f.mu.Lock()
	if f.st.Active {
		f.mu.Unlock()
		return "", errors.New("ядро уже скачивается")
	}
	f.st = DownloadState{Active: true, Progress: -1, Message: "Ищу последнюю версию ядра"}
	f.mu.Unlock()

	path, err := f.download(ctx, dst, current)
	f.set(func(s *DownloadState) {
		s.Active = false
		if errors.Is(err, ErrUpToDate) {
			s.Error, s.Message, s.Progress = "", "Уже последняя версия: "+current, 1
		} else if err != nil {
			s.Error = err.Error()
			s.Message = ""
		} else {
			s.Error = ""
			s.Progress = 1
		}
	})
	return path, err
}

func (f *Fetcher) download(ctx context.Context, dst, current string) (string, error) {
	client := &http.Client{}
	req, _ := http.NewRequestWithContext(ctx, "GET", releasesAPI, nil)
	req.Header.Set("User-Agent", appName+"/"+appVersion)
	req.Header.Set("Accept", "application/vnd.github+json")
	ctxAPI, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := client.Do(req.WithContext(ctxAPI))
	if err != nil {
		return "", fmt.Errorf("GitHub недоступен: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub ответил %s", resp.Status)
	}
	var rel struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", fmt.Errorf("не разобрал ответ GitHub: %w", err)
	}
	if current != "" && rel.Tag == current {
		return "", ErrUpToDate
	}
	var url, name string
	var size int64
	for _, re := range coreAssetRes {
		for _, a := range rel.Assets {
			if re.MatchString(a.Name) {
				url, name, size = a.URL, a.Name, a.Size
				break
			}
		}
		if url != "" {
			break
		}
	}
	if url == "" {
		return "", fmt.Errorf("в релизе %s нет сборки для Windows amd64", rel.Tag)
	}

	f.set(func(s *DownloadState) { s.Message = "Скачиваю " + name; s.Progress = 0 })
	zipPath := dst + ".zip.part"
	defer os.Remove(zipPath)
	if err := f.fetchFile(ctx, client, url, zipPath, size); err != nil {
		return "", err
	}

	f.set(func(s *DownloadState) { s.Message = "Распаковываю"; s.Progress = -1 })
	newPath := dst + ".new"
	if err := extractExe(zipPath, newPath); err != nil {
		return "", err
	}
	if _, err := coreVersion(newPath); err != nil {
		os.Remove(newPath)
		return "", fmt.Errorf("скачанное ядро не запускается: %w", err)
	}
	return newPath, nil
}

func (f *Fetcher) fetchFile(ctx context.Context, client *http.Client, url, path string, size int64) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", appName+"/"+appVersion)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("не удалось скачать ядро: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("скачивание ядра: %s", resp.Status)
	}
	if resp.ContentLength > 0 {
		size = resp.ContentLength
	}
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	var got int64
	buf := make([]byte, 64<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				return err
			}
			got += int64(n)
			if size > 0 {
				p := float64(got) / float64(size)
				f.set(func(s *DownloadState) { s.Progress = p })
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("обрыв при скачивании ядра: %w", rerr)
		}
	}
	return out.Close()
}

func extractExe(zipPath, dst string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("архив ядра повреждён: %w", err)
	}
	defer zr.Close()
	for _, zf := range zr.File {
		if !strings.HasSuffix(strings.ToLower(zf.Name), ".exe") {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(dst)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		return err
	}
	return errors.New("в архиве нет mihomo.exe")
}

var coreVerRe = regexp.MustCompile(`v\d+\.\d+\.\d+\S*`)

func coreVersion(exe string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-v")
	cmd.SysProcAttr = hiddenProc()
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	if v := coreVerRe.FindString(string(out)); v != "" {
		return v, nil
	}
	return strings.TrimSpace(string(out)), nil
}
