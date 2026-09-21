package main

// Серверы, добавленные в программе поверх конфига (ссылки vless:// или блоки YAML).
// В ядро они попадают inline proxy-provider'ом, поэтому группы с include-all
// видят их без правки конфига. Переключение - выбор сервера в основной группе.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const providerName = "mihomodesk"

type Server struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Link string `json:"link,omitempty"` // исходная ссылка vless://
	YAML string `json:"yaml,omitempty"` // или блок YAML (один элемент списка, с "- ")
}

// ServerInfo - то, что показывает вкладка «Серверы».
type ServerInfo struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Host   string `json:"host"`
	Port   string `json:"port"`
	Net    string `json:"net"`
	Sec    string `json:"sec"`
	Source string `json:"source"` // desk | config
	Link   string `json:"link,omitempty"`
}

type serversFile struct {
	Servers []Server `json:"servers"`
	Active  string   `json:"active"` // имя выбранного сервера; "" - как в конфиге
	Group   string   `json:"group"`  // группа для переключения; "" - первая select-группа
}

type ServerStore struct {
	mu   sync.Mutex
	path string
	f    serversFile
}

func loadServers(path string) *ServerStore {
	st := &ServerStore{path: path}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &st.f)
	}
	return st
}

func (st *ServerStore) Snapshot() serversFile {
	st.mu.Lock()
	defer st.mu.Unlock()
	f := st.f
	f.Servers = append([]Server(nil), st.f.Servers...)
	return f
}

func (st *ServerStore) Update(fn func(*serversFile) error) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	next := st.f
	next.Servers = append([]Server(nil), st.f.Servers...)
	if err := fn(&next); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(next, "", "  ")
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, st.path); err != nil {
		return err
	}
	st.f = next
	return nil
}

// ---------- разбор ввода ----------

// parseServersInput разбирает то, что вставил пользователь: ссылки vless://
// (по одной в строке) или YAML с одним или несколькими прокси.
func parseServersInput(text string) ([]Server, []string) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	var out []Server
	var errs []string
	if text == "" {
		return nil, []string{"пусто"}
	}
	if strings.Contains(text, "://") && !strings.Contains(text, "\n  ") && !strings.Contains(text, "type:") {
		for _, l := range strings.Split(text, "\n") {
			l = strings.TrimSpace(l)
			if l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			s, err := parseLink(l)
			if err != nil {
				errs = append(errs, short(l)+": "+err.Error())
				continue
			}
			out = append(out, s)
		}
		return out, errs
	}
	items, err := splitYAMLProxies(text)
	if err != nil {
		return nil, []string{err.Error()}
	}
	for _, it := range items {
		name := yamlItemName(it)
		if name == "" {
			errs = append(errs, "в блоке YAML нет name")
			continue
		}
		if !regexp.MustCompile(`(?m)^\s*-?\s*(?:type|server):`).MatchString(it) && !strings.Contains(it, "type:") {
			errs = append(errs, name+": нет type/server")
			continue
		}
		out = append(out, Server{Name: name, YAML: it})
	}
	return out, errs
}

func short(s string) string {
	if len(s) > 48 {
		return s[:45] + "..."
	}
	return s
}

func parseLink(link string) (Server, error) {
	if !strings.HasPrefix(strings.ToLower(link), "vless://") {
		return Server{}, errors.New("поддерживаются ссылки vless://")
	}
	if _, err := vlessEntry(link, ""); err != nil {
		return Server{}, err
	}
	u, _ := url.Parse(link)
	name := strings.TrimSpace(u.Fragment)
	if name == "" {
		name = u.Host
	}
	return Server{Name: name, Link: link}, nil
}

// vlessEntry превращает ссылку vless:// в запись прокси mihomo (flow-YAML в одну строку).
func vlessEntry(link, name string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return "", fmt.Errorf("кривая ссылка: %v", err)
	}
	if u.User == nil || u.User.Username() == "" {
		return "", errors.New("нет uuid")
	}
	host, portStr := u.Hostname(), u.Port()
	port, err := strconv.Atoi(portStr)
	if host == "" || err != nil || port <= 0 || port > 65535 {
		return "", errors.New("нет адреса или порта сервера")
	}
	q := u.Query()
	if name == "" {
		name = strings.TrimSpace(u.Fragment)
		if name == "" {
			name = u.Host
		}
	}
	netw := strings.ToLower(q.Get("type"))
	if netw == "" || netw == "raw" {
		netw = "tcp"
	}
	sec := strings.ToLower(q.Get("security"))
	if sec == "" {
		sec = "none"
	}

	m := &ymap{}
	m.add("name", name)
	m.add("type", "vless")
	m.add("server", host)
	m.add("port", port)
	m.add("uuid", u.User.Username())
	m.add("udp", true)
	if flow := q.Get("flow"); flow != "" {
		m.add("flow", flow)
	}
	if enc := q.Get("encryption"); enc != "" && enc != "none" {
		m.add("encryption", enc)
	}
	pe := q.Get("packetEncoding")
	if pe == "" {
		pe = "xudp"
	}
	m.add("packet-encoding", pe)

	switch sec {
	case "none":
	case "tls", "reality":
		m.add("tls", true)
		if sni := q.Get("sni"); sni != "" {
			m.add("servername", sni)
		}
		fp := q.Get("fp")
		if fp == "" && sec == "reality" {
			fp = "chrome"
		}
		if fp != "" {
			m.add("client-fingerprint", fp)
		}
		if alpn := q.Get("alpn"); alpn != "" {
			m.add("alpn", strings.Split(alpn, ","))
		}
		if v := q.Get("allowInsecure"); v == "1" || v == "true" {
			m.add("skip-cert-verify", true)
		}
		if sec == "reality" {
			pbk := q.Get("pbk")
			if pbk == "" {
				return "", errors.New("reality без pbk (public key)")
			}
			ro := &ymap{}
			ro.add("public-key", pbk)
			if sid := q.Get("sid"); sid != "" {
				ro.add("short-id", sid)
			}
			m.add("reality-opts", ro)
		}
	default:
		return "", fmt.Errorf("security=%s не поддерживается", sec)
	}

	path, hostHdr := q.Get("path"), q.Get("host")
	switch netw {
	case "tcp":
		m.add("network", "tcp")
	case "ws", "httpupgrade":
		m.add("network", "ws")
		o := &ymap{}
		if path != "" {
			o.add("path", path)
		}
		if hostHdr != "" {
			h := &ymap{}
			h.add("Host", hostHdr)
			o.add("headers", h)
		}
		if netw == "httpupgrade" {
			o.add("v2ray-http-upgrade", true)
		}
		m.add("ws-opts", o)
	case "grpc":
		m.add("network", "grpc")
		o := &ymap{}
		o.add("grpc-service-name", q.Get("serviceName"))
		m.add("grpc-opts", o)
	case "h2", "http":
		m.add("network", "h2")
		o := &ymap{}
		if hostHdr != "" {
			o.add("host", strings.Split(hostHdr, ","))
		}
		if path != "" {
			o.add("path", path)
		}
		m.add("h2-opts", o)
	case "xhttp", "splithttp":
		m.add("network", "xhttp")
		o := &ymap{}
		if path != "" {
			o.add("path", path)
		}
		if hostHdr != "" {
			o.add("host", hostHdr)
		}
		if mode := q.Get("mode"); mode != "" {
			o.add("mode", mode)
		}
		m.add("xhttp-opts", o)
	default:
		return "", fmt.Errorf("type=%s не поддерживается", netw)
	}
	return m.flow(), nil
}

// ymap - упорядоченный словарь для вывода в flow-YAML.
type ymap struct {
	keys []string
	vals []any
}

func (m *ymap) add(k string, v any) { m.keys, m.vals = append(m.keys, k), append(m.vals, v) }

func (m *ymap) flow() string {
	parts := make([]string, len(m.keys))
	for i, k := range m.keys {
		parts[i] = k + ": " + yval(m.vals[i])
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func yval(v any) string {
	switch x := v.(type) {
	case string:
		return strconv.Quote(x)
	case int:
		return strconv.Itoa(x)
	case bool:
		return strconv.FormatBool(x)
	case []string:
		q := make([]string, len(x))
		for i, s := range x {
			q[i] = strconv.Quote(strings.TrimSpace(s))
		}
		return "[" + strings.Join(q, ", ") + "]"
	case *ymap:
		return x.flow()
	}
	return `""`
}

// ---------- YAML-блоки ----------

// splitYAMLProxies делит вставленный YAML на элементы списка прокси
// и приводит каждый к виду "- name: ..." с отступом 0.
func splitYAMLProxies(text string) ([]string, error) {
	lines := splitLines(text)
	var body []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "proxies:" {
			continue
		}
		body = append(body, strings.TrimRight(l, " \t"))
	}
	minInd := -1
	for _, l := range body {
		if isBlank(l) || strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		if ind := indentOf(l); minInd < 0 || ind < minInd {
			minInd = ind
		}
	}
	if minInd < 0 {
		return nil, errors.New("пусто")
	}
	for i, l := range body {
		if len(l) >= minInd {
			body[i] = l[minInd:]
		}
	}
	first := ""
	for _, l := range body {
		if !isBlank(l) && !strings.HasPrefix(l, "#") {
			first = l
			break
		}
	}
	if !strings.HasPrefix(first, "-") {
		// один прокси без "- ": делаем из него элемент списка
		for i, l := range body {
			if isBlank(l) {
				continue
			}
			if l == first {
				body[i] = "- " + l
			} else {
				body[i] = "  " + l
			}
		}
	}
	var items []string
	var cur []string
	flush := func() {
		for len(cur) > 0 && isBlank(cur[len(cur)-1]) {
			cur = cur[:len(cur)-1]
		}
		if len(cur) > 0 {
			items = append(items, strings.Join(cur, "\n"))
		}
		cur = nil
	}
	for _, l := range body {
		if strings.HasPrefix(l, "-") {
			flush()
		}
		if strings.HasPrefix(l, "#") && len(cur) == 0 {
			continue
		}
		cur = append(cur, l)
	}
	flush()
	if len(items) == 0 {
		return nil, errors.New("не нашёл ни одного прокси")
	}
	return items, nil
}

var (
	itemNameRe = regexp.MustCompile(`(?m)^(-?\s*)name:\s*(.+?)\s*$`)
	flowNameRe = regexp.MustCompile(`name:\s*("(?:[^"\\]|\\.)*"|'[^']*'|[^,}]+)`)
)

func yamlItemName(item string) string {
	first := strings.SplitN(strings.TrimSpace(item), "\n", 2)[0]
	if strings.Contains(first, "{") {
		if m := flowNameRe.FindStringSubmatch(first); m != nil {
			return unquoteScalar(strings.TrimSpace(m[1]))
		}
	}
	if m := itemNameRe.FindStringSubmatch(item); m != nil {
		return unquoteScalar(m[2])
	}
	return ""
}

// renameYAMLItem меняет name в блоке YAML (для уникальных имён).
func renameYAMLItem(item, name string) string {
	first := strings.SplitN(item, "\n", 2)[0]
	if strings.Contains(first, "{") {
		loc := flowNameRe.FindStringSubmatchIndex(item)
		if loc != nil {
			return item[:loc[2]] + strconv.Quote(name) + item[loc[3]:]
		}
		return item
	}
	done := false
	return itemNameRe.ReplaceAllStringFunc(item, func(s string) string {
		if done {
			return s
		}
		done = true
		m := itemNameRe.FindStringSubmatch(s)
		return m[1] + "name: " + strconv.Quote(name)
	})
}

// entryFor - запись для payload провайдера.
func (s Server) entryLines() ([]string, error) {
	if s.Link != "" {
		e, err := vlessEntry(s.Link, s.Name)
		if err != nil {
			return nil, err
		}
		return []string{"- " + e}, nil
	}
	return splitLines(renameYAMLItem(s.YAML, s.Name)), nil
}

// providerLines - блок inline-провайдера с серверами программы.
// indent - отступ ключа провайдера (у ключа proxy-providers он 0, у провайдера 2).
func providerLines(servers []Server, indent string) ([]string, error) {
	out := []string{
		indent + providerName + ":",
		indent + "  type: inline",
		indent + "  payload:",
	}
	for _, s := range servers {
		lines, err := s.entryLines()
		if err != nil {
			return nil, fmt.Errorf("сервер %q: %w", s.Name, err)
		}
		for _, l := range lines {
			out = append(out, indent+"    "+l)
		}
	}
	return out, nil
}

// ---------- что есть в конфиге ----------

type cfgItem struct {
	fields map[string]string // ключи первого уровня элемента
}

// listItems разбирает элементы списка в блоке верхнего уровня (proxies, proxy-groups).
func listItems(src, key string) []cfgItem {
	lines := splitLines(src)
	var items []cfgItem
	for _, b := range topBlocks(lines) {
		if b.key != key {
			continue
		}
		child := -1
		var cur *cfgItem
		fieldInd := -1
		for i := b.start + 1; i < b.end; i++ {
			l := lines[i]
			t := strings.TrimSpace(l)
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			ind := indentOf(l)
			if strings.HasPrefix(t, "-") && (child < 0 || ind == child) {
				child = ind
				items = append(items, cfgItem{fields: map[string]string{}})
				cur = &items[len(items)-1]
				rest := strings.TrimSpace(strings.TrimPrefix(t, "-"))
				if strings.HasPrefix(rest, "{") {
					for _, m := range regexp.MustCompile(`([\w-]+):\s*("(?:[^"\\]|\\.)*"|'[^']*'|\{[^}]*\}|\[[^\]]*\]|[^,}]+)`).FindAllStringSubmatch(rest, -1) {
						if _, ok := cur.fields[m[1]]; !ok {
							cur.fields[m[1]] = unquoteScalar(strings.TrimSpace(m[2]))
						}
					}
					fieldInd = -2
					continue
				}
				fieldInd = ind + (len(t) - len(rest))
				if k, v, ok := strings.Cut(rest, ":"); ok {
					cur.fields[strings.TrimSpace(k)] = unquoteScalar(strings.TrimSpace(v))
				}
				continue
			}
			if cur != nil && ind == fieldInd {
				if k, v, ok := strings.Cut(t, ":"); ok {
					cur.fields[strings.TrimSpace(k)] = unquoteScalar(strings.TrimSpace(v))
				}
			}
		}
	}
	return items
}

func configServers(src string) []ServerInfo {
	var out []ServerInfo
	for _, it := range listItems(src, "proxies") {
		f := it.fields
		if f["name"] == "" {
			continue
		}
		out = append(out, ServerInfo{
			Name: f["name"], Type: f["type"], Host: f["server"], Port: f["port"],
			Net: orDefault(f["network"], "tcp"), Sec: secOf(f), Source: "config",
		})
	}
	return out
}

func secOf(f map[string]string) string {
	if _, ok := f["reality-opts"]; ok {
		return "reality"
	}
	if f["tls"] == "true" {
		return "tls"
	}
	return "none"
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// selectGroups - имена select-групп конфига по порядку.
func selectGroups(src string) []string {
	var out []string
	for _, it := range listItems(src, "proxy-groups") {
		if it.fields["type"] == "select" && it.fields["name"] != "" {
			out = append(out, it.fields["name"])
		}
	}
	return out
}

func (s Server) info() ServerInfo {
	si := ServerInfo{ID: s.ID, Name: s.Name, Source: "desk", Link: s.Link}
	if s.Link != "" {
		u, err := url.Parse(s.Link)
		if err == nil {
			q := u.Query()
			si.Type, si.Host, si.Port = "vless", u.Hostname(), u.Port()
			si.Net = orDefault(strings.ToLower(q.Get("type")), "tcp")
			si.Sec = orDefault(strings.ToLower(q.Get("security")), "none")
		}
		return si
	}
	items := listItems("proxies:\n"+indentBlock(s.YAML, "  "), "proxies")
	if len(items) > 0 {
		f := items[0].fields
		si.Type, si.Host, si.Port = f["type"], f["server"], f["port"]
		si.Net, si.Sec = orDefault(f["network"], "tcp"), secOf(f)
	}
	return si
}

func indentBlock(s, ind string) string {
	lines := splitLines(s)
	for i, l := range lines {
		if l != "" {
			lines[i] = ind + l
		}
	}
	return strings.Join(lines, "\n")
}

// uniqueName подбирает имя, которого ещё нет среди taken.
func uniqueName(name string, taken map[string]bool) string {
	if !taken[name] {
		return name
	}
	for i := 2; ; i++ {
		n := fmt.Sprintf("%s %d", name, i)
		if !taken[n] {
			return n
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func hasServer(list []Server, name string) bool {
	for _, s := range list {
		if s.Name == name {
			return true
		}
	}
	return false
}
