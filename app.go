package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type App struct {
	paths    Paths
	settings *SettingsStore
	servers  *ServerStore
	logs     *LogBuf
	core     *Core
	fetcher  *Fetcher
	boardSt  *BoardStore
	updater  *Updater

	token  string
	port   int
	server *http.Server

	mu           sync.Mutex
	coreVersion  string
	cfgCache     cfgSnapshot
	winMu        sync.Mutex
	winOwned     bool         // окно открыто этим запуском программы
	adoptWindows bool         // запущены после обновления: открытое окно - наше
	lastUI       atomic.Int64 // когда окно последний раз спрашивало состояние (unix ns)
	quitOnce     sync.Once
	quitFn       func()
}

// newApp: token задаётся при перезапуске после обновления, чтобы открытое
// окно продолжило работать; иначе пустой - будет случайный.
func newApp(p Paths, token string) (*App, error) {
	if token == "" {
		token = randomHex(16)
	}
	a := &App{
		paths:    p,
		settings: loadSettings(p.Settings),
		servers:  loadServers(p.Servers),
		boardSt:  loadBoard(p.Board),
		updater:  &Updater{},
		logs:     newLogBuf(3000),
		fetcher:  &Fetcher{},
		token:    token,
	}
	a.core = newCore(a)
	// недокачанное обновление ядра с прошлого раза
	_ = os.Remove(p.CoreExe + ".new")
	if fileExists(p.CoreExe) {
		go a.refreshCoreVersion()
	}
	a.logs.Add("app", "info", fmt.Sprintf("%s %s запущен", appName, appVersion))
	return a, nil
}

func (a *App) refreshCoreVersion() {
	v, err := coreVersion(a.paths.CoreExe)
	if err != nil {
		v = ""
	}
	a.mu.Lock()
	a.coreVersion = v
	a.mu.Unlock()
}

func (a *App) CoreVersion() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.coreVersion
}

// installCore скачивает ядро и ставит его на место. Ядро в этот момент не запущено.
func (a *App) installCore(ctx context.Context) error {
	tmp, err := a.fetcher.Download(ctx, a.paths.CoreExe, "")
	if err != nil {
		return fmt.Errorf("не удалось скачать ядро: %w", err)
	}
	if err := os.Rename(tmp, a.paths.CoreExe); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("не удалось установить ядро: %w", err)
	}
	a.refreshCoreVersion()
	a.logs.Add("app", "info", "Ядро mihomo установлено: "+a.CoreVersion())
	return nil
}

// updateCore - кнопка «Обновить ядро»: качаем, при необходимости
// останавливаем VPN на время замены файла и поднимаем обратно.
func (a *App) updateCore() {
	tmp, err := a.fetcher.Download(context.Background(), a.paths.CoreExe, a.CoreVersion())
	if errors.Is(err, ErrUpToDate) {
		return
	}
	if err != nil {
		a.logs.Add("app", "error", "Обновление ядра: "+err.Error())
		return
	}
	wasOn := a.core.Status() == stRunning || a.core.Status() == stStarting
	if wasOn {
		a.core.Disconnect()
	}
	if err := os.Rename(tmp, a.paths.CoreExe); err != nil {
		os.Remove(tmp)
		a.fetcher.set(func(s *DownloadState) { s.Error = "не удалось заменить ядро: " + err.Error() })
	} else {
		a.refreshCoreVersion()
		a.fetcher.set(func(s *DownloadState) { s.Message = "Установлено ядро " + a.CoreVersion() })
		a.logs.Add("app", "info", "Ядро обновлено: "+a.CoreVersion())
	}
	if wasOn {
		a.core.Connect()
	}
}

type ConfigInfo struct {
	Exists   bool  `json:"exists"`
	Modified int64 `json:"modified"`
	Proxies  int   `json:"proxies"`
	Groups   int   `json:"groups"`
}

func (a *App) configInfo() ConfigInfo {
	c := a.cfg()
	if strings.TrimSpace(c.text) == "" {
		return ConfigInfo{}
	}
	return ConfigInfo{Exists: true, Modified: c.mod.UnixMilli(), Proxies: c.proxies, Groups: c.groups}
}

// cfgSnapshot - конфиг пользователя и то, что из него часто нужно.
// Перечитывается, только когда файл изменился.
type cfgSnapshot struct {
	mod             time.Time
	size            int64
	text            string
	proxies, groups int
	selects         []string
}

func (a *App) cfg() cfgSnapshot {
	st, err := os.Stat(a.paths.UserConfig)
	if err != nil {
		return cfgSnapshot{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if c := a.cfgCache; c.mod.Equal(st.ModTime()) && c.size == st.Size() {
		return c
	}
	b, err := os.ReadFile(a.paths.UserConfig)
	if err != nil {
		return cfgSnapshot{}
	}
	c := cfgSnapshot{mod: st.ModTime(), size: st.Size(), text: string(b)}
	c.proxies, c.groups = configStats(c.text)
	c.selects = selectGroups(c.text)
	a.cfgCache = c
	return c
}

func (a *App) writeInstance() error {
	b, _ := json.Marshal(map[string]any{"port": a.port, "token": a.token, "pid": os.Getpid()})
	return os.WriteFile(a.paths.Instance, b, 0o644)
}

// showExisting - второй запуск программы: просим первую копию открыть окно.
func showExisting(p Paths) error {
	allowForeground()
	var lastErr error
	for i := 0; i < 15; i++ {
		b, err := os.ReadFile(p.Instance)
		if err == nil {
			var inst struct {
				Port  int    `json:"port"`
				Token string `json:"token"`
			}
			if err = json.Unmarshal(b, &inst); err == nil {
				req, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/internal/show", inst.Port), nil)
				req.Header.Set("X-Desk-Token", inst.Token)
				cl := http.Client{Timeout: 2 * time.Second}
				var resp *http.Response
				if resp, err = cl.Do(req); err == nil {
					resp.Body.Close()
					if resp.StatusCode == 200 {
						return nil
					}
					err = fmt.Errorf("HTTP %d", resp.StatusCode)
				}
			}
		}
		lastErr = err
		time.Sleep(300 * time.Millisecond)
	}
	return lastErr
}

func (a *App) quit() {
	a.quitOnce.Do(func() {
		if a.quitFn != nil {
			a.quitFn()
		}
	})
}

func (a *App) shutdown() {
	a.logs.Add("app", "info", "Выход")
	a.core.Disconnect()
	if a.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = a.server.Shutdown(ctx)
		cancel()
	}
	_ = os.Remove(a.paths.Instance)
}

// ---------- серверы ----------

func (a *App) userConfig() string { return a.cfg().text }

// mainGroup - группа, через которую переключаются серверы: выбранная
// пользователем или первая select-группа конфига.
func (a *App) mainGroup() string {
	groups := a.cfg().selects
	want := a.servers.Snapshot().Group
	for _, g := range groups {
		if g == want {
			return g
		}
	}
	if len(groups) > 0 {
		return groups[0]
	}
	return ""
}

// coreAPI - запрос к API работающего ядра.
func (a *App) coreAPI(method, path string, body any) (*http.Response, error) {
	st := a.core.State()
	if st.Status != stRunning {
		return nil, errors.New("VPN не подключён")
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", st.Port, path), rd)
	req.Header.Set("Authorization", "Bearer "+st.Secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	cl := http.Client{Timeout: 5 * time.Second}
	return cl.Do(req)
}

// groupState - что сейчас выбрано в группе и из чего можно выбирать.
func (a *App) groupState(group string) (now string, all []string, err error) {
	resp, err := a.coreAPI("GET", "/proxies/"+url.PathEscape(group), nil)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	var g struct {
		Now string   `json:"now"`
		All []string `json:"all"`
	}
	if resp.StatusCode != 200 {
		return "", nil, fmt.Errorf("группа %q не найдена", group)
	}
	err = json.NewDecoder(resp.Body).Decode(&g)
	return g.Now, g.All, err
}

// selectInGroup ставит сервер в основную группу работающего ядра.
// name == "" - вернуть выбор по умолчанию (первый вариант группы).
func (a *App) selectInGroup(name string) error {
	group := a.mainGroup()
	if group == "" {
		return errors.New("в конфиге нет select-группы для переключения")
	}
	_, all, err := a.groupState(group)
	if err != nil {
		return err
	}
	if name == "" {
		if len(all) == 0 {
			return nil
		}
		name = all[0]
	}
	found := false
	for _, n := range all {
		if n == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("в группе %q нет сервера %q: у группы должен быть include-all: true", group, name)
	}
	resp, err := a.coreAPI("PUT", "/proxies/"+url.PathEscape(group), map[string]string{"name": name})
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ядро не переключило группу: HTTP %d", resp.StatusCode)
	}
	a.logs.Add("app", "info", fmt.Sprintf("Сервер: %s (группа %s)", name, group))
	return nil
}

// applyActiveServer - после подключения ставим сервер, выбранный на вкладке «Серверы».
func (a *App) applyActiveServer() {
	active := a.servers.Snapshot().Active
	if active == "" {
		return
	}
	if err := a.selectInGroup(active); err != nil {
		a.logs.Add("app", "warning", "Не удалось выбрать сервер "+active+": "+err.Error())
	}
}

// currentServer - что сейчас выбрано в основной группе (для главной страницы).
func (a *App) currentServer() string {
	if a.core.Status() != stRunning {
		return ""
	}
	group := a.mainGroup()
	if group == "" {
		return ""
	}
	now, _, err := a.groupState(group)
	if err != nil {
		return ""
	}
	return now
}

// validateWithServers - прогон конфига с новым списком серверов через "mihomo -t".
func (a *App) validateWithServers(servers []Server) error {
	src := a.userConfig()
	if strings.TrimSpace(src) == "" || !fileExists(a.paths.CoreExe) {
		// без конфига или ядра проверяем только разбор ссылок
		for _, s := range servers {
			if _, err := s.entryLines(); err != nil {
				return fmt.Errorf("%s: %w", s.Name, err)
			}
		}
		return nil
	}
	ad, err := adaptConfig(src, a.settings.Get(), servers)
	if err != nil {
		return err
	}
	path := a.paths.CheckHome + string(os.PathSeparator) + "servers.yaml"
	if err := os.WriteFile(path, []byte(ad.Text), 0o644); err != nil {
		return err
	}
	if out, err := testConfig(context.Background(), a.paths.CoreExe, a.paths.CheckHome, path); err != nil {
		return errors.New(out)
	}
	return nil
}

// restartIfRunning - список серверов поменялся: ядру нужен новый конфиг.
func (a *App) restartIfRunning() bool {
	switch a.core.Status() {
	case stRunning, stStarting:
		go a.core.Restart()
		return true
	}
	return false
}
