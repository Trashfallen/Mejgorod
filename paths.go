package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

// Paths - всё хранится рядом с exe в папке data (программа переносная).
type Paths struct {
	Exe        string
	Data       string
	CoreDir    string
	CoreExe    string
	Home       string // рабочая папка mihomo (-d): кэш, списки правил, zashboard
	CheckHome  string // отдельная папка для проверки конфига, пока ядро работает
	UserConfig string // конфиг как его вставил пользователь
	RunConfig  string // адаптированный под ПК конфиг, с которым запускается ядро
	Settings   string
	Servers    string // серверы, добавленные в программе
	Board      string // выбор в группах сервисов (вкладка «Группы»)
	Instance   string
	WebProfile string // профиль Edge для окна программы
	AppLog     string
}

func newPaths() (Paths, error) {
	exe, err := os.Executable()
	if err != nil {
		return Paths{}, err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	data := filepath.Join(filepath.Dir(exe), "data")
	return Paths{
		Exe:        exe,
		Data:       data,
		CoreDir:    filepath.Join(data, "core"),
		CoreExe:    filepath.Join(data, "core", "mihomo.exe"),
		Home:       filepath.Join(data, "home"),
		CheckHome:  filepath.Join(data, "check"),
		UserConfig: filepath.Join(data, "config.yaml"),
		RunConfig:  filepath.Join(data, "home", "config.yaml"),
		Settings:   filepath.Join(data, "settings.json"),
		Servers:    filepath.Join(data, "servers.json"),
		Board:      filepath.Join(data, "board.json"),
		Instance:   filepath.Join(data, "instance.json"),
		WebProfile: filepath.Join(data, "window"),
		AppLog:     filepath.Join(data, "app.log"),
	}, nil
}

func (p Paths) ensure() error {
	for _, d := range []string{p.Data, p.CoreDir, p.Home, p.CheckHome} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	probe := filepath.Join(p.Data, ".write-test")
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		return fmt.Errorf("папка %s недоступна для записи: %w", p.Data, err)
	}
	return os.Remove(probe)
}

func setupLog(p Paths) {
	if st, err := os.Stat(p.AppLog); err == nil && st.Size() > 2<<20 {
		_ = os.Remove(p.AppLog)
	}
	f, err := os.OpenFile(p.AppLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.SetOutput(io.Discard)
		return
	}
	log.SetOutput(f)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
