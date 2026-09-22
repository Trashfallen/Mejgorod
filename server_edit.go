package main

// Глубокая настройка сервера с вкладки «Серверы»: его параметры в YAML
// (sni, fingerprint, flow, транспорт и т.д.). После правки сервер живёт
// как YAML, ссылка vless:// у него больше не хранится: она бы устарела.

import (
	"errors"
	"net/http"
	"strings"

	"gopkg.in/yaml.v3"
)

// blockYAML разворачивает «- {name: x, type: vless, ...}» в многострочный YAML:
// так параметры удобно править. Порядок ключей сохраняется.
func blockYAML(text string) string {
	var doc yaml.Node
	if yaml.Unmarshal([]byte(text), &doc) != nil {
		return text
	}
	var unflow func(n *yaml.Node)
	unflow = func(n *yaml.Node) {
		n.Style &^= yaml.FlowStyle
		if n.Kind == yaml.ScalarNode {
			// кавычки вернёт сам кодировщик, где без них значение поменяет тип
			n.Style &^= yaml.DoubleQuotedStyle | yaml.SingleQuotedStyle
		}
		for _, c := range n.Content {
			unflow(c)
		}
	}
	unflow(&doc)
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if enc.Encode(&doc) != nil {
		return text
	}
	enc.Close()
	return strings.TrimRight(b.String(), "\n")
}

func (a *App) hServerYAML(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	for _, s := range a.servers.Snapshot().Servers {
		if s.ID != id {
			continue
		}
		lines, err := s.entryLines()
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"name": s.Name, "yaml": blockYAML(strings.Join(lines, "\n")), "fromLink": s.Link != ""})
		return
	}
	writeErr(w, 404, "сервер не найден")
}

func (a *App) hServerSetYAML(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		YAML string `json:"yaml"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	items, err := splitYAMLProxies(in.YAML)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if len(items) != 1 {
		writeErr(w, 400, "нужен ровно один сервер")
		return
	}
	snap := a.servers.Snapshot()
	next := append([]Server(nil), snap.Servers...)
	idx := -1
	for i, s := range next {
		if s.ID == id {
			idx = i
		}
	}
	if idx < 0 {
		writeErr(w, 404, "сервер не найден")
		return
	}
	// имя меняется переименованием, здесь - только параметры
	next[idx].YAML, next[idx].Link = items[0], ""
	if err := a.validateWithServers(next); err != nil {
		writeErr(w, 400, "ядро не приняло сервер: "+err.Error())
		return
	}
	err = a.servers.Update(func(f *serversFile) error {
		for i := range f.Servers {
			if f.Servers[i].ID == id {
				f.Servers[i] = next[idx]
				return nil
			}
		}
		return errors.New("сервер не найден")
	})
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	a.logs.Add("app", "info", "Параметры сервера изменены: "+next[idx].Name)
	writeJSON(w, 200, map[string]any{"ok": true, "restarted": a.restartIfRunning()})
}
