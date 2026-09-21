package main

// Шаблоны конфига: два встроенных в exe и любые .yaml из папки templates рядом с exe.
// Свои конфиги с uuid кладутся в эту папку, а не встраиваются: exe можно отдать другим.

import (
	"embed"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed builtin-templates/*.yaml
var builtinFS embed.FS

type Template struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Desc    string `json:"desc"`
	Builtin bool   `json:"builtin"`
	file    string
}

var builtinTemplates = []Template{
	{
		ID:      "builtin:selective",
		Name:    "Выборочно через VPN",
		Desc:    "Группа «Заблок. сервисы» и отдельные группы для YouTube, Discord, Twitch, Reddit, Meta, Spotify, Telegram, AI и др. Через VPN идёт только заблокированное, остальное напрямую (MATCH,DIRECT).",
		Builtin: true,
		file:    "builtin-templates/selective.yaml",
	},
	{
		ID:      "builtin:proxy-all",
		Name:    "Всё через VPN, российское напрямую",
		Desc:    "Группа PROXY. Российские сайты и IP, а также сервисы проверки IP идут напрямую, всё остальное через VPN (MATCH,PROXY).",
		Builtin: true,
		file:    "builtin-templates/proxy-all.yaml",
	},
}

func (a *App) templatesDir() string {
	return filepath.Join(filepath.Dir(a.paths.Exe), "templates")
}

func (a *App) listTemplates() []Template {
	out := append([]Template(nil), builtinTemplates...)
	entries, err := os.ReadDir(a.templatesDir())
	if err != nil {
		return out
	}
	var user []Template
	for _, e := range entries {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if e.IsDir() || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		user = append(user, Template{
			ID:   "user:" + e.Name(),
			Name: strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())),
			Desc: "Файл из папки templates рядом с программой",
			file: e.Name(),
		})
	}
	sort.Slice(user, func(i, j int) bool { return user[i].Name < user[j].Name })
	return append(out, user...)
}

// templateText отдаёт текст шаблона. Файлы ищем только среди перечисленных,
// поэтому id вида "user:..\..\x" ничего не прочитает.
func (a *App) templateText(id string) (string, error) {
	for _, t := range a.listTemplates() {
		if t.ID != id {
			continue
		}
		var b []byte
		var err error
		if t.Builtin {
			b, err = builtinFS.ReadFile(t.file)
		} else {
			b, err = os.ReadFile(filepath.Join(a.templatesDir(), t.file))
		}
		if err != nil {
			return "", err
		}
		return strings.TrimPrefix(string(b), string(rune(0xFEFF))), nil
	}
	return "", errors.New("шаблон не найден")
}
