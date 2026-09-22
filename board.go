package main

// Вкладка «Группы»: доска «Через VPN» / «Напрямую».
//
// Группа сервиса переезжает между колонками выбором варианта в ней: основная
// группа VPN или DIRECT (PASS, если DIRECT нет). Выбор хранится в
// data/board.json и применяется при подключении, поэтому группы видны
// и настраиваются и без VPN. Свои сайты и программы - правила в конфиге
// (deskrules.go).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type boardFile struct {
	Choices map[string]string `json:"choices"` // группа -> выбранный вариант
}

type BoardStore struct {
	mu   sync.Mutex
	path string
	f    boardFile
}

func loadBoard(path string) *BoardStore {
	st := &BoardStore{path: path}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &st.f)
	}
	if st.f.Choices == nil {
		st.f.Choices = map[string]string{}
	}
	return st
}

func (st *BoardStore) Choices() map[string]string {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make(map[string]string, len(st.f.Choices))
	for k, v := range st.f.Choices {
		out[k] = v
	}
	return out
}

func (st *BoardStore) SetChoice(group, option string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.f.Choices[group] == option {
		return nil
	}
	st.f.Choices[group] = option
	b, _ := json.MarshalIndent(st.f, "", "  ")
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

type cfgGroupFull struct {
	Name       string   `yaml:"name"`
	Type       string   `yaml:"type"`
	Icon       string   `yaml:"icon"`
	Proxies    []string `yaml:"proxies"`
	IncludeAll bool     `yaml:"include-all"`
	Hidden     bool     `yaml:"hidden"`
}

func parseGroupsFull(src string) []cfgGroupFull {
	var doc struct {
		Groups []cfgGroupFull `yaml:"proxy-groups"`
	}
	_ = yaml.Unmarshal([]byte(src), &doc)
	return doc.Groups
}

type BoardGroup struct {
	Name    string   `json:"name"`
	Icon    string   `json:"icon"`
	Options []string `json:"options"`
	Now     string   `json:"now"`
	Route   string   `json:"route"`  // vpn | direct | pass | block
	VPN     string   `json:"vpn"`    // что выбрать для колонки «Через VPN», "" - нельзя
	Direct  string   `json:"direct"` // DIRECT или PASS, "" - нельзя
}

type BoardItem struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"` // site | app
	Label  string `json:"label"`
	Route  string `json:"route"` // vpn | direct
	Detail string `json:"detail"`
}

type boardResp struct {
	Main    string       `json:"main"`
	Running bool         `json:"running"`
	Groups  []BoardGroup `json:"groups"`
	Items   []BoardItem  `json:"items"`
}

// ProfileBoard - доска одного профиля на вкладке «Шаблоны»: показываются
// сразу все профили, менять группы можно у любого, не переключая активный.
type ProfileBoard struct {
	Profile string       `json:"profile"`
	Active  bool         `json:"active"`
	Running bool         `json:"running"`
	Groups  []BoardGroup `json:"groups"`
	Items   []BoardItem  `json:"items"`
}

func routeOf(option string) string {
	switch option {
	case "DIRECT":
		return "direct"
	case "PASS":
		return "pass"
	case "REJECT", "REJECT-DROP":
		return "block"
	}
	return "vpn"
}

func vpnTarget(opts []string, main string) string {
	if slices.Contains(opts, main) {
		return main
	}
	for _, o := range opts {
		if routeOf(o) == "vpn" {
			return o
		}
	}
	return ""
}

func directTarget(opts []string) string {
	for _, o := range []string{"DIRECT", "PASS"} {
		if slices.Contains(opts, o) {
			return o
		}
	}
	return ""
}

type liveGroup struct {
	Now string   `json:"now"`
	All []string `json:"all"`
}

// liveGroups - группы работающего ядра: текущий выбор и все варианты.
func (a *App) liveGroups() map[string]liveGroup {
	resp, err := a.coreAPI("GET", "/proxies", nil)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var d struct {
		Proxies map[string]liveGroup `json:"proxies"`
	}
	if json.NewDecoder(resp.Body).Decode(&d) != nil {
		return nil
	}
	return d.Proxies
}

func mainGroupOf(src, want string) string {
	groups := selectGroups(src)
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

// boardOf строит доску по тексту конфига. mayBeLive - профиль активен и с
// ним можно свериться с работающим ядром (для остальных профилей доска
// строится только по тексту, ядро их не запускало).
func (a *App) boardOf(src string, mayBeLive bool) boardResp {
	main := mainGroupOf(src, a.servers.Snapshot().Group)
	res := boardResp{Main: main, Running: mayBeLive && a.core.Status() == stRunning, Groups: []BoardGroup{}}
	var live map[string]liveGroup
	if res.Running {
		live = a.liveGroups()
	}
	choices := a.boardSt.Choices()
	for _, g := range parseGroupsFull(src) {
		if g.Type != "select" || g.Hidden || g.Name == main {
			continue
		}
		opts, now := g.Proxies, ""
		if l, ok := live[g.Name]; ok && len(l.All) > 0 {
			opts, now = l.All, l.Now
		}
		if now == "" {
			if c := choices[g.Name]; slices.Contains(opts, c) {
				now = c
			} else if len(opts) > 0 {
				now = opts[0]
			} else if g.IncludeAll {
				now = "первый сервер"
			}
		}
		res.Groups = append(res.Groups, BoardGroup{
			Name: g.Name, Icon: g.Icon, Options: opts, Now: now, Route: routeOf(now),
			VPN: vpnTarget(opts, main), Direct: directTarget(opts),
		})
	}
	res.Items = deskItemsView(src)
	return res
}

func (a *App) board() boardResp { return a.boardOf(a.userConfig(), true) }

// boardAll - доски всех профилей сразу, для вкладки «Шаблоны»: видно и
// правится всё, не переключая активный профиль. Активный - всегда первым.
func (a *App) boardAll() []ProfileBoard {
	active := a.settings.Get().ActiveProfile
	out := []ProfileBoard{}
	for _, name := range a.profileNames() {
		if name == active {
			continue
		}
		b := a.boardOf(a.profileConfigText(name), false)
		out = append(out, ProfileBoard{Profile: name, Active: false, Running: b.Running, Groups: b.Groups, Items: b.Items})
	}
	if active != "" && fileExists(a.profilePath(active)) {
		b := a.boardOf(a.profileConfigText(active), true)
		out = append([]ProfileBoard{{Profile: active, Active: true, Running: b.Running, Groups: b.Groups, Items: b.Items}}, out...)
	}
	return out
}

// setGroupChoice выбирает вариант в группе указанного профиля: в работающем
// ядре сразу (если профиль активен и ядро запущено), и всегда запоминает,
// чтобы применить при следующем подключении с этим профилем.
func (a *App) setGroupChoice(profile, group, option string) error {
	b := a.boardOf(a.profileConfigText(profile), profile == a.settings.Get().ActiveProfile)
	var g *BoardGroup
	for i := range b.Groups {
		if b.Groups[i].Name == group {
			g = &b.Groups[i]
		}
	}
	if g == nil {
		return fmt.Errorf("группы %q нет в профиле %q", group, profile)
	}
	if !slices.Contains(g.Options, option) {
		return fmt.Errorf("в группе %q нет варианта %q", group, option)
	}
	if b.Running {
		resp, err := a.coreAPI("PUT", "/proxies/"+url.PathEscape(group), map[string]string{"name": option})
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("ядро не переключило группу: HTTP %d", resp.StatusCode)
		}
	}
	return a.boardSt.SetChoice(group, option)
}

// GroupSource - откуда в группу попадает трафик: свои домены (kind domain)
// или список geosite/geoip по имени категории (kind geosite | geoip).
type GroupSource struct {
	Kind   string   `json:"kind"`
	Name   string   `json:"name"`
	Count  int      `json:"count"`
	Sample []string `json:"sample"`
}

func ruleSetCategory(providerName string) string {
	if i := strings.Index(providerName, "@"); i > 0 {
		return providerName[:i]
	}
	return providerName
}

// groupSources - сайты и домены, из-за которых правило в rules: ведёт в
// группу name. Списки geosite/geoip качаются по надобности (cachedGeoList),
// поэтому первый наведение на подсказку может быть небыстрым.
func (a *App) groupSources(ctx context.Context, profile, name string) ([]GroupSource, error) {
	var doc cfgDoc
	if err := yaml.Unmarshal([]byte(a.profileConfigText(profile)), &doc); err != nil {
		return nil, err
	}
	var out []GroupSource
	var literals []string
	seenLit, seenSet := map[string]bool{}, map[string]bool{}
	for _, rule := range doc.Rules {
		target, refs := parseRule(rule)
		if target != name {
			continue
		}
		for _, ref := range refs {
			switch ref.kind {
			case "DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "DOMAIN-REGEX":
				d := strings.TrimPrefix(ref.value, "+.")
				if !seenLit[d] {
					seenLit[d] = true
					literals = append(literals, d)
				}
			case "RULE-SET":
				if seenSet[ref.value] {
					continue
				}
				seenSet[ref.value] = true
				p, ok := doc.RuleProviders[ref.value]
				if !ok {
					continue
				}
				cat := ruleSetCategory(ref.value)
				kind := "geosite"
				if p.Behavior == "ipcidr" {
					kind = "geoip"
				}
				gs := GroupSource{Kind: kind, Name: cat, Sample: []string{}}
				if gl, err := cachedGeoList(ctx, kind, cat); err == nil && gl != nil {
					gs.Count = gl.Count
					if gl.Sample != nil {
						gs.Sample = gl.Sample
					}
				}
				out = append(out, gs)
			}
		}
	}
	if len(literals) > 0 {
		out = append([]GroupSource{{Kind: "domain", Count: len(literals), Sample: literals}}, out...)
	}
	if out == nil {
		out = []GroupSource{}
	}
	return out, nil
}

// applyBoardChoices - после подключения выставить сохранённый выбор групп.
func (a *App) applyBoardChoices() {
	live := a.liveGroups()
	if live == nil {
		return
	}
	for group, option := range a.boardSt.Choices() {
		l, ok := live[group]
		if !ok || l.Now == option || !slices.Contains(l.All, option) {
			continue
		}
		resp, err := a.coreAPI("PUT", "/proxies/"+url.PathEscape(group), map[string]string{"name": option})
		if err == nil {
			resp.Body.Close()
		}
		if err != nil || resp.StatusCode >= 300 {
			a.logs.Add("app", "warning", fmt.Sprintf("Не удалось выбрать %s в группе %s", option, group))
		}
	}
}
