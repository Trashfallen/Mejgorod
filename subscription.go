package main

// Импорт подписки: ссылка вида https://host/sub/xxxx, за которой обычно
// стоит список ссылок vless:// в base64 (реже - открытым текстом или сразу
// блоком proxies: как в конфиге mihomo/clash). Сначала показываем, что нашли,
// пользователь отмечает нужные серверы, потом импортируем выбранное - тем же
// путём, что и вставку ссылок на вкладке «Серверы».

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const subMaxBody = 4 << 20 // подписки обычно на порядки меньше, это только потолок

// fetchSubscription скачивает тело подписки по http(s)-ссылке.
func fetchSubscription(ctx context.Context, raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("нужна ссылка http:// или https://")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	req.Header.Set("User-Agent", appName+"/"+appVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("не удалось скачать: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("подписка ответила %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, subMaxBody+1))
	if err != nil {
		return "", fmt.Errorf("обрыв при скачивании: %w", err)
	}
	if len(b) > subMaxBody {
		return "", errors.New("подписка слишком большая")
	}
	if len(b) == 0 {
		return "", errors.New("подписка пустая")
	}
	return string(b), nil
}

var subBase64Encodings = []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding}

// findProxiesBlock ищет в тексте верхнеуровневый ключ proxies: (конфиг
// mihomo/clash целиком или просто список прокси-провайдера) и возвращает
// его как есть, вместе со строкой "proxies:" - дальше это разбирает
// parseYAMLProxyList, как и вставленный вручную блок.
func findProxiesBlock(text string) (string, bool) {
	lines := splitLines(text)
	for _, b := range topBlocks(lines) {
		if b.key == "proxies" {
			return strings.Join(lines[b.start:b.end], "\n"), true
		}
	}
	return "", false
}

// parseSubscription разбирает тело подписки: пробует как есть и как base64,
// сперва ищет блок proxies: (YAML), иначе - список ссылок vless://.
func parseSubscription(raw string) ([]Server, []string) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if raw == "" {
		return nil, []string{"подписка пустая"}
	}
	candidates := []string{raw}
	clean := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, raw)
	for _, enc := range subBase64Encodings {
		dec, err := enc.DecodeString(clean)
		if err != nil || !utf8.Valid(dec) {
			continue
		}
		if s := strings.TrimSpace(string(dec)); s != "" {
			candidates = append(candidates, s)
		}
	}
	for _, c := range candidates {
		if block, ok := findProxiesBlock(c); ok {
			return parseYAMLProxyList(block)
		}
	}
	for _, c := range candidates {
		if strings.Contains(c, "://") {
			return parseServersInput(c)
		}
	}
	return nil, []string{"не нашёл в подписке ни ссылок vless://, ни блока proxies:"}
}

// SubItem - сервер из подписки для показа в чек-листе. Kind/Value достаточно,
// чтобы при импорте пересобрать Server без повторного скачивания подписки.
type SubItem struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Host string `json:"host"`
	Port string `json:"port"`
	Net  string `json:"net"`
	Sec  string `json:"sec"`
	Kind string `json:"kind"` // link | yaml
	Val  string `json:"value"`
}

func toSubItems(servers []Server) []SubItem {
	out := make([]SubItem, 0, len(servers))
	for _, s := range servers {
		info := s.info()
		it := SubItem{Name: s.Name, Type: info.Type, Host: info.Host, Port: info.Port, Net: info.Net, Sec: info.Sec}
		if s.Link != "" {
			it.Kind, it.Val = "link", s.Link
		} else {
			it.Kind, it.Val = "yaml", s.YAML
		}
		out = append(out, it)
	}
	return out
}

func (a *App) hSubPreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL string `json:"url"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	body, err := fetchSubscription(r.Context(), in.URL)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	servers, errs := parseSubscription(body)
	if len(servers) == 0 {
		msg := "не нашёл серверов в подписке"
		if len(errs) > 0 {
			msg = strings.Join(errs, "; ")
		}
		writeErr(w, 400, msg)
		return
	}
	if errs == nil {
		errs = []string{}
	}
	writeJSON(w, 200, map[string]any{"servers": toSubItems(servers), "errors": errs})
}

func (a *App) hSubImport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Items []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
			Val  string `json:"value"`
		} `json:"items"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var parsed []Server
	var errs []string
	for _, it := range in.Items {
		switch it.Kind {
		case "link":
			s, err := parseLink(it.Val)
			if err != nil {
				errs = append(errs, short(it.Val)+": "+err.Error())
				continue
			}
			if it.Name != "" {
				s.Name = it.Name
			}
			parsed = append(parsed, s)
		case "yaml":
			name := it.Name
			if name == "" {
				name = yamlItemName(it.Val)
			}
			if name == "" {
				errs = append(errs, "в блоке YAML нет name")
				continue
			}
			parsed = append(parsed, Server{Name: name, YAML: it.Val})
		default:
			errs = append(errs, "неизвестный тип сервера")
		}
	}
	a.addServers(w, parsed, errs)
}
