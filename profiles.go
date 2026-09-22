package main

// Профили: несколько конфигов, между которыми переключаются на главной
// («Мой конфиг», «Всё через VPN, российское напрямую» и т.д.). Каждый профиль -
// файл data/profiles/<имя>.yaml, активный указан в настройках. Правки в
// редакторе и свои сайты с вкладки «Группы» сохраняются в активный профиль.
// Шаблон становится профилем при первом выборе, дальше правится как свой.

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const defaultProfile = "Мой конфиг"

func (a *App) profilesDir() string { return filepath.Join(a.paths.Data, "profiles") }

func (a *App) profilePath(name string) string {
	return filepath.Join(a.profilesDir(), name+".yaml")
}

// configPath - файл активного конфига. Без профилей - старый data/config.yaml.
func (a *App) configPath() string {
	if p := a.settings.Get().ActiveProfile; p != "" {
		return a.profilePath(p)
	}
	return a.paths.UserConfig
}

// cleanProfileName: имя профиля годится в имя файла.
func cleanProfileName(s string) (string, error) {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\/:*?"<>|`, r) || r < 32 {
			return -1
		}
		return r
	}, s))
	s = strings.Trim(s, ". ")
	if s == "" {
		return "", errors.New("пустое имя профиля")
	}
	if utf8.RuneCountInString(s) > 60 {
		return "", errors.New("слишком длинное имя профиля")
	}
	return s, nil
}

func (a *App) profileNames() []string {
	entries, err := os.ReadDir(a.profilesDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".yaml") {
			out = append(out, strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// migrateProfiles: при первом запуске с профилями старый data/config.yaml
// становится профилем «Мой конфиг».
func (a *App) migrateProfiles() {
	if err := os.MkdirAll(a.profilesDir(), 0o755); err != nil {
		return
	}
	set := a.settings.Get()
	if set.ActiveProfile != "" && fileExists(a.profilePath(set.ActiveProfile)) {
		return
	}
	if names := a.profileNames(); len(names) > 0 {
		_, _ = a.settings.Update(func(s *Settings) { s.ActiveProfile = names[0] })
		return
	}
	b, err := os.ReadFile(a.paths.UserConfig)
	if err != nil || strings.TrimSpace(string(b)) == "" {
		_, _ = a.settings.Update(func(s *Settings) { s.ActiveProfile = "" })
		return
	}
	if err := os.WriteFile(a.profilePath(defaultProfile), b, 0o644); err != nil {
		return
	}
	_ = os.Rename(a.paths.UserConfig, a.paths.UserConfig+".moved")
	_, _ = a.settings.Update(func(s *Settings) { s.ActiveProfile = defaultProfile })
	a.logs.Add("app", "info", "Конфиг стал профилем «"+defaultProfile+"»")
}

// profileConfigText - текст произвольного профиля по имени (для активного -
// через кэш userConfig, для остальных - прямое чтение файла).
func (a *App) profileConfigText(name string) string {
	if name == a.settings.Get().ActiveProfile {
		return a.userConfig()
	}
	b, err := os.ReadFile(a.profilePath(name))
	if err != nil {
		return ""
	}
	return string(b)
}

// writeProfileConfig сохраняет текст в конкретный профиль: активный - как
// writeConfig, остальные - прямой записью в их файл.
func (a *App) writeProfileConfig(profile, text string) error {
	if profile == "" || profile == a.settings.Get().ActiveProfile {
		return a.writeConfig(text)
	}
	tmp := a.profilePath(profile) + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, a.profilePath(profile))
}

// writeConfig сохраняет текст в активный профиль (создаёт его, если профилей нет).
func (a *App) writeConfig(text string) error {
	path := a.configPath()
	if a.settings.Get().ActiveProfile == "" {
		if err := os.MkdirAll(a.profilesDir(), 0o755); err != nil {
			return err
		}
		path = a.profilePath(defaultProfile)
		if _, err := a.settings.Update(func(s *Settings) { s.ActiveProfile = defaultProfile }); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type profileInfo struct {
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

func (a *App) profilesResp() map[string]any {
	active := a.settings.Get().ActiveProfile
	profiles := []profileInfo{}
	have := map[string]bool{}
	for _, n := range a.profileNames() {
		profiles = append(profiles, profileInfo{Name: n, Active: n == active})
		have[strings.ToLower(n)] = true
	}
	// шаблоны, из которых ещё не сделан профиль
	templates := []Template{}
	for _, t := range a.listTemplates() {
		if !have[strings.ToLower(t.Name)] {
			templates = append(templates, t)
		}
	}
	return map[string]any{"active": active, "profiles": profiles, "templates": templates}
}

// activateProfile переключает конфиг и переподключает VPN, если он включён.
func (a *App) activateProfile(name string) error {
	if n, err := cleanProfileName(name); err != nil || n != name || !fileExists(a.profilePath(name)) {
		return fmt.Errorf("профиля «%s» нет", name)
	}
	if _, err := a.settings.Update(func(s *Settings) { s.ActiveProfile = name }); err != nil {
		return err
	}
	a.logs.Add("app", "info", "Профиль: "+name)
	a.restartIfRunning()
	return nil
}

func (a *App) hProfiles(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.profilesResp()) }

// POST /api/profiles/activate {name} или {template}: шаблон сначала становится профилем.
func (a *App) hProfileActivate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string `json:"name"`
		Template string `json:"template"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	name := in.Name
	if in.Template != "" {
		text, err := a.templateText(in.Template)
		if err != nil {
			writeErr(w, 404, err.Error())
			return
		}
		tname := ""
		for _, t := range a.listTemplates() {
			if t.ID == in.Template {
				tname = t.Name
			}
		}
		if name, err = cleanProfileName(tname); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		if !fileExists(a.profilePath(name)) {
			if err := os.MkdirAll(a.profilesDir(), 0o755); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			if err := os.WriteFile(a.profilePath(name), []byte(text), 0o644); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
		}
	}
	if err := a.activateProfile(name); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, a.profilesResp())
}

// POST /api/profiles {name, from}: новый профиль - копия активного или пустой.
func (a *App) hProfileCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		Copy bool   `json:"copy"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	name, err := cleanProfileName(in.Name)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if fileExists(a.profilePath(name)) {
		writeErr(w, 400, "профиль «"+name+"» уже есть")
		return
	}
	text := ""
	if in.Copy {
		text = a.userConfig()
	}
	if err := os.MkdirAll(a.profilesDir(), 0o755); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if err := os.WriteFile(a.profilePath(name), []byte(text), 0o644); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if err := a.activateProfile(name); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, a.profilesResp())
}

// profileFromPath - имя профиля из адреса запроса; "" - такого нет.
func (a *App) profileFromPath(r *http.Request) string {
	raw := r.PathValue("name")
	if n, err := cleanProfileName(raw); err != nil || n != raw || !fileExists(a.profilePath(n)) {
		return ""
	}
	return raw
}

func (a *App) hProfileRename(w http.ResponseWriter, r *http.Request) {
	old := a.profileFromPath(r)
	if old == "" {
		writeErr(w, 404, "нет такого профиля")
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	name, err := cleanProfileName(in.Name)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if !strings.EqualFold(name, old) && fileExists(a.profilePath(name)) {
		writeErr(w, 400, "профиль «"+name+"» уже есть")
		return
	}
	if err := os.Rename(a.profilePath(old), a.profilePath(name)); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if a.settings.Get().ActiveProfile == old {
		_, _ = a.settings.Update(func(s *Settings) { s.ActiveProfile = name })
	}
	writeJSON(w, 200, a.profilesResp())
}

func (a *App) hProfileDelete(w http.ResponseWriter, r *http.Request) {
	name := a.profileFromPath(r)
	if name == "" {
		writeErr(w, 404, "нет такого профиля")
		return
	}
	names := a.profileNames()
	active := a.settings.Get().ActiveProfile == name
	if active && len(names) < 2 {
		writeErr(w, 400, "это единственный профиль: сначала создайте другой")
		return
	}
	// в корзину не кладём, но и не теряем: остаётся .deleted рядом
	if err := os.Rename(a.profilePath(name), a.profilePath(name)+".deleted"); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if active {
		for _, n := range names {
			if n != name {
				_ = a.activateProfile(n)
				break
			}
		}
	}
	a.logs.Add("app", "info", "Профиль удалён: "+name)
	writeJSON(w, 200, a.profilesResp())
}
