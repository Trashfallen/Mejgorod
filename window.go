package main

// Окно программы - Edge в режиме приложения (--app): отдельное окно без
// вкладок и адресной строки. Edge есть в любой Windows 10/11.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

func findEdge() string {
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		k, err := registry.OpenKey(root, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`, registry.QUERY_VALUE)
		if err == nil {
			p, _, err := k.GetStringValue("")
			k.Close()
			if err == nil && fileExists(p) {
				return p
			}
		}
	}
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LocalAppData"} {
		if base := os.Getenv(env); base != "" {
			p := filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe")
			if fileExists(p) {
				return p
			}
		}
	}
	return ""
}

func (a *App) windowURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/#t=%s", a.port, a.token)
}

// openWindow показывает окно программы. Уже открытое - выводит вперёд,
// окна от прошлого запуска (у них устаревший токен) закрывает.
func (a *App) openWindow() {
	a.winMu.Lock()
	defer a.winMu.Unlock()
	wins := appWindows()
	if len(wins) > 0 && (a.winOwned || a.adoptWindows) {
		a.winOwned = true
		focusWindow(wins[0])
		for _, w := range wins[1:] {
			closeWindow(w)
		}
		return
	}
	for _, w := range wins {
		closeWindow(w)
	}
	a.winOwned = true
	url := a.windowURL()
	if edge := findEdge(); edge != "" {
		cmd := exec.Command(edge,
			"--app="+url,
			"--user-data-dir="+a.paths.WebProfile,
			"--window-size=1180,800",
			"--no-first-run",
			"--no-default-browser-check",
			"--no-context-menu",
			"--disable-features=Translate,msEdgeSidebarV2,msUndersideButton",
		)
		if err := cmd.Start(); err == nil {
			go cmd.Wait()
			go a.tagNewWindows()
			return
		}
	}
	openURL(url)
}

func openURL(url string) {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	_ = cmd.Start()
	go cmd.Wait()
}

func openFolder(path string) {
	cmd := exec.Command("explorer.exe", path)
	_ = cmd.Start()
	go cmd.Wait()
}
