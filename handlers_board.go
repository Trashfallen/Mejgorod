package main

// API вкладки «Группы» (доска, свои сайты и программы) и обновления программы.

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
)

func (a *App) hBoard(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.board()) }

func (a *App) hBoardAll(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"boards": a.boardAll()})
}

// profileOrActive - profile из запроса, а без него - активный профиль
// (так старый фронт, ещё не знающий про мультипрофильную доску, продолжит работать).
func (a *App) profileOrActive(profile string) string {
	if profile != "" {
		return profile
	}
	return a.settings.Get().ActiveProfile
}

func (a *App) hBoardGroup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Profile string `json:"profile"`
		Name    string `json:"name"`
		Option  string `json:"option"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := a.setGroupChoice(a.profileOrActive(in.Profile), in.Name, in.Option); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"boards": a.boardAll()})
}

var geoNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9!_.-]*$`)

type boardAddReq struct {
	Profile string   `json:"profile"`
	Kind    string   `json:"kind"` // site | app
	Value   string   `json:"value"`
	Route   string   `json:"route"` // vpn | direct
	Domains []string `json:"domains"`
	Geosite string   `json:"geosite"` // имя категории или ""
	Geoip   string   `json:"geoip"`
}

// routeTargetFor: колонка доски -> цель правила, для профиля с текстом src.
func (a *App) routeTargetFor(src, route string) (string, error) {
	switch route {
	case "direct":
		return "DIRECT", nil
	case "vpn":
		if m := mainGroupOf(src, a.servers.Snapshot().Group); m != "" {
			return m, nil
		}
		return "", errors.New("в конфиге нет select-группы для VPN")
	}
	return "", errors.New("неизвестная колонка")
}

func (a *App) hBoardAdd(w http.ResponseWriter, r *http.Request) {
	var in boardAddReq
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	profile := a.profileOrActive(in.Profile)
	src := a.profileConfigText(profile)
	target, err := a.routeTargetFor(src, in.Route)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(src) == "" {
		writeErr(w, 400, "сначала вставьте конфиг или выберите шаблон на вкладке «Конфиг»")
		return
	}
	rules, provs := readDesk(src)
	var rule deskRule
	switch in.Kind {
	case "app":
		name, err := normalizeProcess(in.Value)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		rule = deskRule{Label: name, Body: "PROCESS-NAME," + name, Target: target}
	case "site":
		label, err := normalizeDomain(in.Value)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		domains := []string{label}
		for _, d := range in.Domains {
			if nd, err := normalizeDomain(d); err == nil && !containsStr(domains, nd) {
				domains = append(domains, nd)
			}
		}
		lists := map[string]string{"geosite": in.Geosite, "geoip": in.Geoip}
		names := map[string]string{}
		for kind, cat := range lists {
			if cat == "" {
				continue
			}
			if !geoNameRe.MatchString(cat) {
				writeErr(w, 400, "странное имя списка: "+cat)
				return
			}
			behavior := map[string]string{"geosite": "domain", "geoip": "ipcidr"}[kind]
			url := geoBase + kind + "/" + cat + ".mrs"
			name, added := providerFor(src, provs, cat, behavior, url)
			names[kind] = name
			if added && !hasProvider(provs, name) {
				provs = append(provs, deskProvider{Name: name, Behavior: behavior, URL: url})
			}
		}
		body, err := siteRuleBody(domains, names["geosite"], names["geoip"])
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		rule = deskRule{Label: label, Body: body, Target: target}
	default:
		writeErr(w, 400, "добавить можно сайт или программу")
		return
	}
	replaced := false
	for i := range rules {
		if deskID(rules[i].Label) == deskID(rule.Label) {
			rules[i], replaced = rule, true
		}
	}
	if !replaced {
		rules = append(rules, rule)
	}
	a.saveDesk(w, r, profile, src, rules, provs, "Добавлено: "+rule.Label)
}

func (a *App) hBoardMove(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Profile string `json:"profile"`
		Route   string `json:"route"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	profile := a.profileOrActive(in.Profile)
	src := a.profileConfigText(profile)
	target, err := a.routeTargetFor(src, in.Route)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	rules, provs := readDesk(src)
	id := r.PathValue("id")
	for i := range rules {
		if deskID(rules[i].Label) == id {
			rules[i].Target = target
			a.saveDesk(w, r, profile, src, rules, provs, rules[i].Label+": "+map[string]string{"vpn": "через VPN", "direct": "напрямую"}[in.Route])
			return
		}
	}
	writeErr(w, 404, "нет такого сайта или программы")
}

func (a *App) hBoardDelete(w http.ResponseWriter, r *http.Request) {
	profile := a.profileOrActive(r.URL.Query().Get("profile"))
	src := a.profileConfigText(profile)
	rules, provs := readDesk(src)
	id := r.PathValue("id")
	for i := range rules {
		if deskID(rules[i].Label) == id {
			label := rules[i].Label
			rules = append(rules[:i], rules[i+1:]...)
			a.saveDesk(w, r, profile, src, rules, provs, "Удалено: "+label)
			return
		}
	}
	writeErr(w, 404, "нет такого сайта или программы")
}

// saveDesk пишет свои правила в конфиг профиля profile, проверяет его ядром,
// сохраняет и переподключает VPN, если это активный профиль и он запущен.
func (a *App) saveDesk(w http.ResponseWriter, r *http.Request, profile, src string, rules []deskRule, provs []deskProvider, msg string) {
	// списки, на которые больше не ссылается ни одно правило, убираем
	var keep []deskProvider
	for _, p := range provs {
		for _, rl := range rules {
			if strings.Contains(rl.Body, "RULE-SET,"+p.Name+")") || strings.Contains(rl.Body, "RULE-SET,"+p.Name+",") {
				keep = append(keep, p)
				break
			}
		}
	}
	text, err := writeDesk(src, rules, keep)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	if check := a.checkConfig(ctx, text); !check.OK {
		writeErr(w, 400, "ядро не приняло правило: "+check.Output)
		return
	}
	if err := a.writeProfileConfig(profile, text); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.logs.Add("app", "info", "["+profile+"] "+msg)
	restarted := false
	if profile == a.settings.Get().ActiveProfile {
		restarted = a.restartIfRunning()
	}
	writeJSON(w, 200, map[string]any{"boards": a.boardAll(), "restarted": restarted})
}

func (a *App) hBoardGroupSites(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	profile := a.profileOrActive(r.URL.Query().Get("profile"))
	out, err := a.groupSources(ctx, profile, r.PathValue("name"))
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"sources": out})
}

func (a *App) hLookup(w http.ResponseWriter, r *http.Request) {
	res, err := lookupSite(r.Context(), r.URL.Query().Get("site"))
	if res == nil {
		writeErr(w, 400, err.Error())
		return
	}
	out := map[string]any{"result": res}
	if err != nil {
		out["warning"] = err.Error()
	}
	writeJSON(w, 200, out)
}

func (a *App) hProcesses(w http.ResponseWriter, r *http.Request) {
	list := listProcesses()
	if list == nil {
		list = []ProcInfo{}
	}
	writeJSON(w, 200, list)
}

func (a *App) hAppCheck(w http.ResponseWriter, r *http.Request) {
	_ = a.updater.Check(r.Context())
	writeJSON(w, 200, a.updater.State())
}

func (a *App) hAppUpdate(w http.ResponseWriter, r *http.Request) {
	if u := a.updater.State(); !u.Available {
		writeErr(w, 400, "обновлений нет")
		return
	}
	go a.installAppUpdate()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func hasProvider(list []deskProvider, name string) bool {
	for _, p := range list {
		if p.Name == name {
			return true
		}
	}
	return false
}
