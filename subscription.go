package main

// Импорт подписки: ссылка вида https://host/sub/xxxx, за которой обычно
// стоит список ссылок vless:// в base64 (реже - открытым текстом или сразу
// блоком proxies: как в конфиге mihomo/clash). Сначала показываем, что нашли,
// пользователь отмечает нужные серверы, потом импортируем выбранное - тем же
// путём, что и вставку ссылок на вкладке «Серверы».
//
// Ссылка запоминается (Subscription): серверы за ней у провайдера меняются,
// а ссылка нет, поэтому при каждом запуске программы подписка скачивается
// заново и её серверы заменяются свежими (см. refreshSubs).

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

// fetchSubscription скачивает тело подписки по http(s)-ссылке. Второе значение -
// название из заголовка profile-title (Remnawave, 3x-ui, Marzban), если есть.
func fetchSubscription(ctx context.Context, raw string) (string, string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", "", errors.New("нужна ссылка http:// или https://")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	req.Header.Set("User-Agent", appName+"/"+appVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("не удалось скачать: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("подписка ответила %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, subMaxBody+1))
	if err != nil {
		return "", "", fmt.Errorf("обрыв при скачивании: %w", err)
	}
	if len(b) > subMaxBody {
		return "", "", errors.New("подписка слишком большая")
	}
	if len(b) == 0 {
		return "", "", errors.New("подписка пустая")
	}
	return string(b), profileTitle(resp.Header.Get("Profile-Title")), nil
}

// profileTitle разбирает заголовок profile-title: открытый текст или "base64:...".
func profileTitle(v string) string {
	v = strings.TrimSpace(v)
	if rest, ok := strings.CutPrefix(v, "base64:"); ok {
		v = ""
		for _, enc := range subBase64Encodings {
			if dec, err := enc.DecodeString(strings.TrimSpace(rest)); err == nil && utf8.Valid(dec) {
				v = strings.TrimSpace(string(dec))
				break
			}
		}
	}
	if r := []rune(v); len(r) > 60 {
		v = string(r[:60])
	}
	return v
}

// subDefaultName - название подписки, если провайдер его не прислал: хост ссылки.
func subDefaultName(raw string) string {
	if u, err := url.Parse(strings.TrimSpace(raw)); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return "Подписка"
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
	body, title, err := fetchSubscription(r.Context(), in.URL)
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
	if title == "" {
		title = subDefaultName(in.URL)
	}
	known := "" // эта ссылка уже добавлена: импорт заменит её серверы
	for _, sb := range a.servers.Snapshot().Subs {
		if sb.URL == strings.TrimSpace(in.URL) {
			known = sb.Name
		}
	}
	writeJSON(w, 200, map[string]any{"servers": toSubItems(servers), "errors": errs, "name": title, "known": known})
}

func (a *App) hSubImport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL   string   `json:"url"`
		Name  string   `json:"name"`
		Skip  []string `json:"skip"` // имена, с которых сняли галочку: при обновлении не возвращать
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
	if strings.TrimSpace(in.URL) == "" {
		a.addServers(w, parsed, errs) // без ссылки: просто серверы, как вставка
		return
	}
	if errs == nil {
		errs = []string{}
	}
	if len(parsed) == 0 {
		writeJSON(w, 200, map[string]any{"added": []string{}, "errors": errs})
		return
	}
	sub := Subscription{URL: strings.TrimSpace(in.URL), Name: strings.TrimSpace(in.Name), Skip: append([]string{}, in.Skip...)}
	for _, sb := range a.servers.Snapshot().Subs {
		if sb.URL == sub.URL {
			sub.ID = sb.ID // та же ссылка: заменяем серверы этой подписки, а не дублируем
			if sub.Name == "" {
				sub.Name = sb.Name
			}
		}
	}
	if sub.ID == "" {
		sub.ID = randomHex(6)
	}
	if sub.Name == "" {
		sub.Name = subDefaultName(sub.URL)
	}
	sub.Updated = time.Now()
	names, _, err := a.saveSub(sub, parsed)
	if err != nil {
		writeJSON(w, 200, map[string]any{"added": []string{}, "errors": append(errs, "ядро не приняло серверы: "+err.Error())})
		return
	}
	a.logs.Add("app", "info", fmt.Sprintf("Подписка «%s»: %d серверов", sub.Name, len(names)))
	writeJSON(w, 200, map[string]any{"added": names, "errors": errs, "restarted": a.restartIfRunning()})
}

// ---------- сохранённые подписки ----------

// Subscription - ссылка на подписку, которую программа помнит и перекачивает
// при каждом запуске.
type Subscription struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	URL     string    `json:"url"`
	Skip    []string  `json:"skip,omitempty"` // имена из подписки, которые пользователь убрал
	Updated time.Time `json:"updated,omitempty"`
	Error   string    `json:"error,omitempty"` // последняя неудачная попытка обновить
}

// mergeSub заменяет серверы подписки subID свежим списком fresh. Сервер с тем
// же именем в подписке (Orig) сохраняет ID и имя, данное пользователем; серверы
// подписки встают на место прежних, а не в конец. names - их итоговые имена.
func mergeSub(all []Server, subID string, fresh []Server, skip []string, taken map[string]bool) (next []Server, names []string) {
	old := map[string]Server{}
	pos := -1
	for _, s := range all {
		if s.Sub == subID {
			if _, dup := old[s.Orig]; !dup {
				old[s.Orig] = s
			}
			if pos < 0 {
				pos = len(next)
			}
			continue
		}
		next = append(next, s)
		taken[s.Name] = true
	}
	if pos < 0 {
		pos = len(next)
	}
	skipped := map[string]bool{}
	for _, n := range skip {
		skipped[n] = true
	}
	var mine []Server
	for _, s := range fresh {
		orig := s.Name
		if skipped[orig] {
			continue
		}
		s.Sub, s.Orig = subID, orig
		if o, ok := old[orig]; ok {
			s.ID, s.Name = o.ID, o.Name
			delete(old, orig) // повтор имени в подписке получит новый ID
		} else {
			s.ID = randomHex(6)
		}
		if taken[s.Name] {
			s.Name = uniqueName(s.Name, taken)
		}
		taken[s.Name] = true
		mine = append(mine, s)
		names = append(names, s.Name)
	}
	out := append([]Server(nil), next[:pos]...)
	out = append(out, mine...)
	out = append(out, next[pos:]...)
	return out, names
}

func sameServers(a, b []Server) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// saveSub кладёт подписку и её серверы в хранилище после проверки ядром.
// changed - список серверов действительно поменялся (нужен перезапуск ядра).
func (a *App) saveSub(sub Subscription, fresh []Server) (names []string, changed bool, err error) {
	snap := a.servers.Snapshot()
	taken := map[string]bool{}
	for _, cs := range configServers(a.userConfig()) {
		taken[cs.Name] = true
	}
	next, names := mergeSub(snap.Servers, sub.ID, fresh, sub.Skip, taken)
	changed = !sameServers(next, snap.Servers)
	if changed {
		if err := a.validateWithServers(next); err != nil {
			return nil, false, err
		}
	}
	err = a.servers.Update(func(f *serversFile) error {
		f.Servers = next
		found := false
		for i := range f.Subs {
			if f.Subs[i].ID == sub.ID {
				f.Subs[i], found = sub, true
			}
		}
		if !found {
			f.Subs = append(f.Subs, sub)
		}
		if !hasServer(next, f.Active) && len(names) > 0 {
			f.Active = names[0] // выбранный сервер пропал из подписки
		}
		return nil
	})
	return names, changed, err
}

// refreshSub перекачивает одну подписку. При ошибке прежние серверы остаются.
func (a *App) refreshSub(ctx context.Context, id string) (changed bool, err error) {
	var sub Subscription
	for _, sb := range a.servers.Snapshot().Subs {
		if sb.ID == id {
			sub = sb
		}
	}
	if sub.ID == "" {
		return false, errors.New("подписка не найдена")
	}
	fail := func(err error) (bool, error) {
		_ = a.servers.Update(func(f *serversFile) error {
			for i := range f.Subs {
				if f.Subs[i].ID == id {
					f.Subs[i].Error = err.Error()
				}
			}
			return nil
		})
		a.logs.Add("app", "warning", fmt.Sprintf("Подписка «%s» не обновилась, остались прежние серверы: %v", sub.Name, err))
		return false, err
	}
	body, _, err := fetchSubscription(ctx, sub.URL)
	if err != nil {
		return fail(err)
	}
	fresh, errs := parseSubscription(body)
	if len(fresh) == 0 {
		msg := "не нашёл серверов в подписке"
		if len(errs) > 0 {
			msg = strings.Join(errs, "; ")
		}
		return fail(errors.New(msg))
	}
	sub.Updated, sub.Error = time.Now(), ""
	names, changed, err := a.saveSub(sub, fresh)
	if err != nil {
		return fail(fmt.Errorf("ядро не приняло серверы: %w", err))
	}
	if changed {
		a.logs.Add("app", "info", fmt.Sprintf("Подписка «%s» обновлена: %d серверов", sub.Name, len(names)))
	} else {
		a.logs.Add("app", "info", fmt.Sprintf("Подписка «%s» без изменений", sub.Name))
	}
	return changed, nil
}

// refreshSubs обновляет все подписки (onlyFailed - только упавшие в прошлый раз).
func (a *App) refreshSubs(ctx context.Context, onlyFailed bool) (changed bool, failed int) {
	for _, sb := range a.servers.Snapshot().Subs {
		if onlyFailed && sb.Error == "" {
			continue
		}
		ch, err := a.refreshSub(ctx, sb.ID)
		changed = changed || ch
		if err != nil {
			failed++
		}
	}
	return changed, failed
}

func (a *App) hSubRefresh(w http.ResponseWriter, r *http.Request) {
	changed, err := a.refreshSub(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"changed": changed, "restarted": changed && a.restartIfRunning()})
}

func (a *App) hSubDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var name string
	err := a.servers.Update(func(f *serversFile) error {
		idx := -1
		for i, sb := range f.Subs {
			if sb.ID == id {
				idx, name = i, sb.Name
			}
		}
		if idx < 0 {
			return errors.New("подписка не найдена")
		}
		f.Subs = append(f.Subs[:idx], f.Subs[idx+1:]...)
		var keep []Server
		for _, s := range f.Servers {
			if s.Sub != id {
				keep = append(keep, s)
			}
		}
		f.Servers = keep
		if !hasServer(keep, f.Active) {
			f.Active = ""
			if len(keep) > 0 {
				f.Active = keep[0].Name
			}
		}
		return nil
	})
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	a.logs.Add("app", "info", "Удалена подписка «"+name+"» вместе с её серверами")
	writeJSON(w, 200, map[string]any{"ok": true, "restarted": a.restartIfRunning()})
}
