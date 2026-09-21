package main

// Локальный HTTP-сервер: отдаёт интерфейс и API для него.
// Слушает только 127.0.0.1; API требует токен, который знает лишь окно программы,
// а проверка Host закрывает атаки через DNS rebinding.

import (
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mihomodesk/internal/icon"
)

//go:embed ui
var uiFS embed.FS

var faviconPNG = icon.PNG(64, icon.On)

func (a *App) startServer() error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", a.settings.Get().UIPort))
	if err != nil {
		if ln, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			return err
		}
	}
	a.port = ln.Addr().(*net.TCPAddr).Port

	sub, _ := fs.Sub(uiFS, "ui")
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServerFS(sub))
	mux.HandleFunc("GET /favicon.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(faviconPNG)
	})
	mux.HandleFunc("POST /internal/show", a.auth(func(w http.ResponseWriter, r *http.Request) {
		go a.openWindow()
		writeJSON(w, 200, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("GET /api/state", a.auth(a.hState))
	mux.HandleFunc("POST /api/connect", a.auth(a.hConnect))
	mux.HandleFunc("POST /api/disconnect", a.auth(a.hDisconnect))
	mux.HandleFunc("POST /api/restart", a.auth(a.hRestart))
	mux.HandleFunc("GET /api/config", a.auth(a.hGetConfig))
	mux.HandleFunc("PUT /api/config", a.auth(a.hPutConfig))
	mux.HandleFunc("POST /api/config/check", a.auth(a.hCheckConfig))
	mux.HandleFunc("POST /api/config/preview", a.auth(a.hPreviewConfig))
	mux.HandleFunc("GET /api/logs", a.auth(a.hLogs))
	mux.HandleFunc("POST /api/logs/clear", a.auth(a.hLogsClear))
	mux.HandleFunc("GET /api/settings", a.auth(a.hGetSettings))
	mux.HandleFunc("PUT /api/settings", a.auth(a.hPutSettings))
	mux.HandleFunc("GET /api/templates", a.auth(a.hTemplates))
	mux.HandleFunc("GET /api/template", a.auth(a.hTemplate))
	mux.HandleFunc("GET /api/servers", a.auth(a.hServers))
	mux.HandleFunc("POST /api/servers", a.auth(a.hAddServers))
	mux.HandleFunc("PUT /api/servers/active", a.auth(a.hServerActive))
	mux.HandleFunc("PUT /api/servers/group", a.auth(a.hServerGroup))
	mux.HandleFunc("PUT /api/servers/{id}", a.auth(a.hRenameServer))
	mux.HandleFunc("DELETE /api/servers/{id}", a.auth(a.hDeleteServer))
	mux.HandleFunc("POST /api/core/update", a.auth(a.hCoreUpdate))
	mux.HandleFunc("POST /api/open", a.auth(a.hOpen))
	mux.HandleFunc("POST /api/quit", a.auth(a.hQuit))
	mux.HandleFunc("/api/mihomo/", a.auth(a.hMihomo))

	a.server = &http.Server{Handler: a.guard(mux), ReadHeaderTimeout: 10 * time.Second}
	go a.server.Serve(ln)
	return a.writeInstance()
}

func (a *App) guard(next http.Handler) http.Handler {
	allowed := map[string]bool{
		fmt.Sprintf("127.0.0.1:%d", a.port): true,
		fmt.Sprintf("localhost:%d", a.port): true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.Host] {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' https: data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (a *App) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Desk-Token")), []byte(a.token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(v)
}

// ---------- состояние и кнопка ----------

type stateResp struct {
	CoreState
	Server   string `json:"server"` // что выбрано в основной группе
	Elevated bool   `json:"elevated"`
	Core     struct {
		Installed bool   `json:"installed"`
		Version   string `json:"version"`
	} `json:"core"`
	Download DownloadState `json:"download"`
	Config   ConfigInfo    `json:"config"`
	App      struct {
		Version string `json:"version"`
		DataDir string `json:"dataDir"`
	} `json:"app"`
}

func (a *App) state() stateResp {
	var s stateResp
	s.CoreState = a.core.State()
	if s.Status == stRunning {
		s.Server = a.currentServer()
	} else {
		s.Server = a.servers.Snapshot().Active
	}
	s.Elevated = isElevated()
	s.Core.Installed = fileExists(a.paths.CoreExe)
	s.Core.Version = a.CoreVersion()
	s.Download = a.fetcher.State()
	s.Config = a.configInfo()
	s.App.Version = appVersion
	s.App.DataDir = a.paths.Data
	return s
}

func (a *App) hState(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.state()) }

func (a *App) hConnect(w http.ResponseWriter, r *http.Request) {
	a.core.Connect()
	writeJSON(w, 200, a.state())
}

func (a *App) hDisconnect(w http.ResponseWriter, r *http.Request) {
	go a.core.Disconnect()
	time.Sleep(50 * time.Millisecond)
	writeJSON(w, 200, a.state())
}

func (a *App) hRestart(w http.ResponseWriter, r *http.Request) {
	go a.core.Restart()
	time.Sleep(50 * time.Millisecond)
	writeJSON(w, 200, a.state())
}

// ---------- конфиг ----------

type checkResult struct {
	OK        bool     `json:"ok"`
	Partial   bool     `json:"partial"` // ядра ещё нет, проверена только структура
	Output    string   `json:"output"`
	Changes   []string `json:"changes"`
	NoServers bool     `json:"noServers"`
}

func (a *App) checkConfig(ctx context.Context, text string) checkResult {
	servers := a.servers.Snapshot().Servers
	ad, err := adaptConfigEnv(text, a.settings.Get(), servers, a.buildEnv(text, servers))
	if err != nil {
		return checkResult{Output: err.Error()}
	}
	res := checkResult{Changes: ad.Changes, NoServers: ad.NoServers}
	if !fileExists(a.paths.CoreExe) {
		res.OK, res.Partial = true, true
		res.Output = "Структура в порядке. Полная проверка - после скачивания ядра (при первом подключении)."
		return res
	}
	path := filepath.Join(a.paths.CheckHome, "config.yaml")
	if err := os.WriteFile(path, []byte(ad.Text), 0o644); err != nil {
		res.Output = err.Error()
		return res
	}
	out, err := testConfig(ctx, a.paths.CoreExe, a.paths.CheckHome, path)
	res.OK, res.Output = err == nil, out
	if res.OK && res.Output == "" {
		res.Output = "Конфиг в порядке"
	}
	if res.OK && ad.NoServers {
		res.Output = "Конфиг в порядке, но серверов нет: добавьте ссылку vless:// на вкладке «Серверы»"
	}
	return res
}

func (a *App) hGetConfig(w http.ResponseWriter, r *http.Request) {
	b, err := os.ReadFile(a.paths.UserConfig)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"text": string(b), "exists": err == nil})
}

func (a *App) hPutConfig(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text  string `json:"text"`
		Apply bool   `json:"apply"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(in.Text) == "" {
		writeErr(w, 400, "конфиг пустой")
		return
	}
	tmp := a.paths.UserConfig + ".tmp"
	if err := os.WriteFile(tmp, []byte(in.Text), 0o644); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if err := os.Rename(tmp, a.paths.UserConfig); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.logs.Add("app", "info", "Конфиг сохранён")
	check := a.checkConfig(r.Context(), in.Text)
	applied := false
	if in.Apply && check.OK {
		applied = true
		switch a.core.Status() {
		case stRunning, stStarting:
			go a.core.Restart()
		default:
			a.core.Connect()
		}
	}
	writeJSON(w, 200, map[string]any{"saved": true, "check": check, "applied": applied})
}

func (a *App) hCheckConfig(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, a.checkConfig(r.Context(), in.Text))
}

func (a *App) hPreviewConfig(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	servers := a.servers.Snapshot().Servers
	ad, err := adaptConfigEnv(in.Text, a.settings.Get(), servers, a.buildEnv(in.Text, servers))
	if err != nil {
		writeJSON(w, 200, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, ad)
}

// ---------- логи ----------

func (a *App) hLogs(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	lines, last := a.logs.After(after)
	writeJSON(w, 200, map[string]any{"lines": lines, "last": last})
}

func (a *App) hLogsClear(w http.ResponseWriter, r *http.Request) {
	a.logs.Clear()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- настройки ----------

func (a *App) settingsResp() map[string]any {
	s := a.settings.Get()
	return map[string]any{
		"connectOnLaunch": s.ConnectOnLaunch,
		"tunStack":        s.TunStack,
		"strictRoute":     s.StrictRoute,
		"tunRoute":        s.TunRoute,
		"controllerPort":  s.ControllerPort,
		"autostart":       autostartEnabled(),
		"exe":             a.paths.Exe,
	}
}

func (a *App) hGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.settingsResp())
}

func (a *App) hPutSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ConnectOnLaunch *bool   `json:"connectOnLaunch"`
		TunStack        *string `json:"tunStack"`
		StrictRoute     *bool   `json:"strictRoute"`
		TunRoute        *string `json:"tunRoute"`
		ControllerPort  *int    `json:"controllerPort"`
		Autostart       *bool   `json:"autostart"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if in.TunRoute != nil {
		switch *in.TunRoute {
		case "auto", "full", "split":
		default:
			writeErr(w, 400, "неизвестный режим маршрутов TUN")
			return
		}
	}
	if in.TunStack != nil {
		switch *in.TunStack {
		case "mixed", "gvisor", "system":
		default:
			writeErr(w, 400, "неизвестный стек TUN")
			return
		}
	}
	if in.ControllerPort != nil && (*in.ControllerPort < 1024 || *in.ControllerPort > 65535) {
		writeErr(w, 400, "порт должен быть от 1024 до 65535")
		return
	}
	if in.Autostart != nil {
		if err := setAutostart(*in.Autostart, a.paths.Exe); err != nil {
			writeErr(w, 500, "автозапуск: "+err.Error())
			return
		}
		a.logs.Add("app", "info", fmt.Sprintf("Автозапуск: %v", *in.Autostart))
	}
	before := a.settings.Get()
	after, err := a.settings.Update(func(s *Settings) {
		if in.ConnectOnLaunch != nil {
			s.ConnectOnLaunch = *in.ConnectOnLaunch
		}
		if in.TunStack != nil {
			s.TunStack = *in.TunStack
		}
		if in.TunRoute != nil {
			s.TunRoute = *in.TunRoute
		}
		if in.StrictRoute != nil {
			s.StrictRoute = *in.StrictRoute
		}
		if in.ControllerPort != nil {
			s.ControllerPort = *in.ControllerPort
		}
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	coreAffected := before.TunStack != after.TunStack || before.StrictRoute != after.StrictRoute || before.TunRoute != after.TunRoute ||
		before.ControllerPort != after.ControllerPort
	st := a.core.Status()
	resp := a.settingsResp()
	resp["restartRequired"] = coreAffected && (st == stRunning || st == stStarting)
	writeJSON(w, 200, resp)
}

// ---------- ядро, окна, выход ----------

func (a *App) hCoreUpdate(w http.ResponseWriter, r *http.Request) {
	if a.fetcher.State().Active {
		writeErr(w, 409, "ядро уже скачивается")
		return
	}
	go a.updateCore()
	time.Sleep(50 * time.Millisecond)
	writeJSON(w, 200, a.state())
}

func (a *App) hOpen(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Target string `json:"target"`
	}
	_ = readJSON(r, &in)
	switch in.Target {
	case "data":
		openFolder(a.paths.Data)
		writeJSON(w, 200, map[string]bool{"ok": true})
	case "zashboard":
		st := a.core.State()
		if st.Status != stRunning {
			writeErr(w, 409, "сначала подключитесь")
			return
		}
		u := fmt.Sprintf("http://127.0.0.1:%d/ui/#/setup?hostname=127.0.0.1&port=%d&secret=%s",
			st.Port, st.Port, url.QueryEscape(st.Secret))
		openURL(u)
		writeJSON(w, 200, map[string]bool{"ok": true})
	default:
		writeErr(w, 400, "unknown target")
	}
}

func (a *App) hQuit(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]bool{"ok": true})
	go func() {
		time.Sleep(200 * time.Millisecond)
		a.quit()
	}()
}

// hMihomo проксирует /api/mihomo/* в API ядра (группы, пинг, уровень логов),
// подставляя secret, чтобы он не жил в браузере.
func (a *App) hMihomo(w http.ResponseWriter, r *http.Request) {
	st := a.core.State()
	if st.Status != stRunning {
		writeErr(w, 503, "VPN не подключён")
		return
	}
	// выбор в основной группе на вкладке «Группы» = выбор сервера
	if r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/api/mihomo/proxies/") {
		group := strings.TrimPrefix(r.URL.Path, "/api/mihomo/proxies/")
		if group == a.mainGroup() {
			body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			r.Body = io.NopCloser(bytes.NewReader(body))
			var in struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(body, &in) == nil && in.Name != "" {
				_ = a.servers.Update(func(f *serversFile) error { f.Active = in.Name; return nil })
			}
		}
	}
	target := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", st.Port)}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			esc := strings.TrimPrefix(pr.In.URL.EscapedPath(), "/api/mihomo")
			p, err := url.PathUnescape(esc)
			if err != nil {
				p = esc
			}
			pr.Out.URL.Path, pr.Out.URL.RawPath = p, esc
			pr.Out.Header.Del("X-Desk-Token")
			pr.Out.Header.Del("Origin")
			pr.Out.Header.Set("Authorization", "Bearer "+st.Secret)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			writeErr(w, 502, "ядро не отвечает: "+err.Error())
		},
	}
	rp.ServeHTTP(w, r)
}

// ---------- серверы ----------

func (a *App) serversResp() map[string]any {
	snap := a.servers.Snapshot()
	src := a.userConfig()
	desk := make([]ServerInfo, 0, len(snap.Servers))
	for _, s := range snap.Servers {
		desk = append(desk, s.info())
	}
	cfg := configServers(src)
	if cfg == nil {
		cfg = []ServerInfo{}
	}
	groups := selectGroups(src)
	if groups == nil {
		groups = []string{}
	}
	resp := map[string]any{
		"servers": desk,
		"config":  cfg,
		"groups":  groups,
		"group":   a.mainGroup(),
		"active":  snap.Active,
		"running": a.core.Status() == stRunning,
	}
	if a.core.Status() == stRunning {
		if now, _, err := a.groupState(a.mainGroup()); err == nil {
			resp["current"] = now
		}
	}
	return resp
}

func (a *App) hServers(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.serversResp()) }

func (a *App) hAddServers(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	parsed, errs := parseServersInput(in.Text)
	if errs == nil {
		errs = []string{} // в JSON массив, а не null
	}
	if len(parsed) == 0 {
		writeJSON(w, 200, map[string]any{"added": []string{}, "errors": errs})
		return
	}
	snap := a.servers.Snapshot()
	taken := map[string]bool{}
	for _, cs := range configServers(a.userConfig()) {
		taken[cs.Name] = true
	}
	for _, s := range snap.Servers {
		taken[s.Name] = true
	}
	var added []string
	next := append([]Server(nil), snap.Servers...)
	for _, s := range parsed {
		s.ID = randomHex(6)
		s.Name = uniqueName(s.Name, taken)
		taken[s.Name] = true
		next = append(next, s)
		added = append(added, s.Name)
	}
	if err := a.validateWithServers(next); err != nil {
		writeJSON(w, 200, map[string]any{"added": []string{}, "errors": append(errs, "ядро не приняло серверы: "+err.Error())})
		return
	}
	if err := a.servers.Update(func(f *serversFile) error {
		f.Servers = next
		if !hasServer(next, f.Active) {
			f.Active = added[0]
		}
		return nil
	}); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.logs.Add("app", "info", "Добавлены серверы: "+strings.Join(added, ", "))
	writeJSON(w, 200, map[string]any{"added": added, "errors": errs, "restarted": a.restartIfRunning()})
}

func (a *App) hRenameServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeErr(w, 400, "пустое имя")
		return
	}
	name := strings.TrimSpace(in.Name)
	err := a.servers.Update(func(f *serversFile) error {
		taken := map[string]bool{}
		for _, cs := range configServers(a.userConfig()) {
			taken[cs.Name] = true
		}
		idx := -1
		for i, s := range f.Servers {
			if s.ID == id {
				idx = i
			} else {
				taken[s.Name] = true
			}
		}
		if idx < 0 {
			return errors.New("сервер не найден")
		}
		if taken[name] {
			return fmt.Errorf("имя %q уже занято", name)
		}
		if f.Active == f.Servers[idx].Name {
			f.Active = name
		}
		f.Servers[idx].Name = name
		return nil
	})
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "restarted": a.restartIfRunning()})
}

func (a *App) hDeleteServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var name string
	err := a.servers.Update(func(f *serversFile) error {
		for i, s := range f.Servers {
			if s.ID == id {
				name = s.Name
				f.Servers = append(f.Servers[:i], f.Servers[i+1:]...)
				if f.Active == name {
					// выбран был удалённый: берём первый оставшийся, иначе как в конфиге
					f.Active = ""
					if len(f.Servers) > 0 {
						f.Active = f.Servers[0].Name
					}
				}
				return nil
			}
		}
		return errors.New("сервер не найден")
	})
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	a.logs.Add("app", "info", "Удалён сервер "+name)
	writeJSON(w, 200, map[string]any{"ok": true, "restarted": a.restartIfRunning()})
}

func (a *App) hServerActive(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if a.core.Status() == stRunning {
		if err := a.selectInGroup(in.Name); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}
	if err := a.servers.Update(func(f *serversFile) error { f.Active = in.Name; return nil }); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, a.serversResp())
}

func (a *App) hServerGroup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Group string `json:"group"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := a.servers.Update(func(f *serversFile) error { f.Group = in.Group; return nil }); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if a.core.Status() == stRunning {
		a.applyActiveServer()
	}
	writeJSON(w, 200, a.serversResp())
}

// ---------- шаблоны ----------

func (a *App) hTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"templates": a.listTemplates(), "dir": a.templatesDir()})
}

func (a *App) hTemplate(w http.ResponseWriter, r *http.Request) {
	text, err := a.templateText(r.URL.Query().Get("id"))
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"text": text})
}
