package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"
)

type Settings struct {
	ConnectOnLaunch bool   `json:"connectOnLaunch"`
	TunStack        string `json:"tunStack"`       // mixed | gvisor | system
	StrictRoute     bool   `json:"strictRoute"`    // tun.strict-route: защита от утечек DNS
	TunRoute        string `json:"tunRoute"`       // auto | full | split (см. splitroute.go)
	ControllerPort  int    `json:"controllerPort"` // порт API ядра и zashboard
	UIPort          int    `json:"uiPort"`         // порт окна программы
	Secret          string `json:"secret"`         // secret для API, если его нет в конфиге
}

func defaultSettings() Settings {
	return Settings{
		TunStack:       "mixed",
		TunRoute:       "auto",
		ControllerPort: 9090,
		UIPort:         17890,
		Secret:         randomHex(12),
	}
}

type SettingsStore struct {
	mu   sync.Mutex
	path string
	s    Settings
}

func loadSettings(path string) *SettingsStore {
	st := &SettingsStore{path: path, s: defaultSettings()}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &st.s)
	}
	switch st.s.TunStack {
	case "mixed", "gvisor", "system":
	default:
		st.s.TunStack = "mixed"
	}
	switch st.s.TunRoute {
	case "auto", "full", "split":
	default:
		st.s.TunRoute = "auto"
	}
	if st.s.ControllerPort <= 0 || st.s.ControllerPort > 65535 {
		st.s.ControllerPort = 9090
	}
	if st.s.UIPort <= 0 || st.s.UIPort > 65535 {
		st.s.UIPort = 17890
	}
	if st.s.Secret == "" {
		st.s.Secret = randomHex(12)
	}
	_ = st.saveLocked()
	return st
}

func (st *SettingsStore) Get() Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s
}

func (st *SettingsStore) Update(fn func(*Settings)) (Settings, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	fn(&st.s)
	return st.s, st.saveLocked()
}

func (st *SettingsStore) saveLocked() error {
	b, _ := json.MarshalIndent(st.s, "", "  ")
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
