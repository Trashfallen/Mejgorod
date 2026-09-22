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

func (a *App) hBoardGroup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name   string `json:"name"`
		Option string `json:"option"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := a.setGroupChoice(in.Name, in.Option); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, a.board())
}

var geoNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9!_.-]*$`)

type boardAddReq struct {
	Kind    string   `json:"kind"` // site | app
	Value   string   `json:"value"`
	Route   string   `json:"route"` // vpn | direct
	Domains []string `json:"domains"`
	Geosite string   `json:"geosite"` // имя категории или ""
	Geoip   string   `json:"geoip"`
}

// routeTarget: колонка доски -> цель правила.
func (a *App) routeTarget(route string) (string, error) {
	switch route {
	case "direct":
		return "DIRECT", nil
	case "vpn":
		if m := a.mainGroup(); m != "" {
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
	target, err := a.routeTarget(in.Route)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	src := a.userConfig()
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
	a.saveDesk(w, r, src, rules, provs, "Добавлено: "+rule.Label)
}

func (a *App) hBoardMove(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Route string `json:"route"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	target, err := a.routeTarget(in.Route)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	src := a.userConfig()
	rules, provs := readDesk(src)
	id := r.PathValue("id")
	for i := range rules {
		if deskID(rules[i].Label) == id {
			rules[i].Target = target
			a.saveDesk(w, r, src, rules, provs, rules[i].Label+": "+map[string]string{"vpn": "через VPN", "direct": "напрямую"}[in.Route])
			return
		}
	}
	writeErr(w, 404, "нет такого сайта или программы")
}

func (a *App) hBoardDelete(w http.ResponseWriter, r *http.Request) {
	src := a.userConfig()
	rules, provs := readDesk(src)
	id := r.PathValue("id")
	for i := range rules {
		if deskID(rules[i].Label) == id {
			label := rules[i].Label
			rules = append(rules[:i], rules[i+1:]...)
			a.saveDesk(w, r, src, rules, provs, "Удалено: "+label)
			return
		}
	}
	writeErr(w, 404, "нет такого сайта или программы")
}

// saveDesk пишет свои правила в конфиг, проверяет его ядром, сохраняет
// и переподключает VPN, если он включён.
func (a *App) saveDesk(w http.ResponseWriter, r *http.Request, src string, rules []deskRule, provs []deskProvider, msg string) {
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
	if err := a.writeConfig(text); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.logs.Add("app", "info", msg)
	restarted := a.restartIfRunning()
	writeJSON(w, 200, map[string]any{"board": a.board(), "restarted": restarted})
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
