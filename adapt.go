package main

// Адаптация конфига роутера (XKeen) под ПК. Правим текст, а не пересобираем YAML:
// так комментарии, якоря и порядок остаются ровно как у пользователя.
// Меняются только ключи верхнего уровня, в конце дописывается блок для ПК.

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// devNoTun - режим отладки: TUN выключен, права администратора не нужны.
var devNoTun bool

// detectIPv6 - есть ли у ПК настоящий IPv6; в тестах подменяется.
var detectIPv6 = hasGlobalIPv6

var topKeyRe = regexp.MustCompile(`^([A-Za-z0-9_][A-Za-z0-9_.\-]*)[ \t]*:(?:[ \t]|$)`)

type yblock struct {
	key        string
	start, end int // строки [start, end)
}

type Adapted struct {
	Text      string   `json:"text"`
	Changes   []string `json:"changes"`
	Port      int      `json:"port"`
	Secret    string   `json:"-"`
	NoServers bool     `json:"noServers"` // ни proxies, ни провайдеров, ни серверов программы
}

// Только для роутера: на ПК их нет смысла держать или они мешают.
var routerOnlyKeys = []string{"redir-port", "tproxy-port", "routing-mark", "interface-name"}

func splitLines(s string) []string {
	s = strings.TrimPrefix(s, string(rune(0xFEFF)))
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(s, "\n")
}

func isBlank(l string) bool { return strings.TrimSpace(l) == "" }

// continuesBlock - строка относится к блоку ключа выше: отступ или элемент
// списка с нулевым отступом ("key:\n- a").
func continuesBlock(l string) bool {
	if l == "" {
		return false
	}
	switch l[0] {
	case ' ', '\t':
		return true
	case '-':
		return !strings.HasPrefix(l, "---")
	}
	return false
}

func topBlocks(lines []string) []yblock {
	var out []yblock
	for i := 0; i < len(lines); i++ {
		m := topKeyRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		j := i + 1
	scan:
		for ; j < len(lines); j++ {
			l := lines[j]
			switch {
			case isBlank(l), continuesBlock(l):
				continue
			case l[0] == '#':
				// комментарий с нулевым отступом внутри блока: смотрим, что после него
				k := j + 1
				for k < len(lines) && (isBlank(lines[k]) || strings.HasPrefix(lines[k], "#")) {
					k++
				}
				if k < len(lines) && continuesBlock(lines[k]) {
					j = k
					continue
				}
				break scan
			default:
				break scan
			}
		}
		end := j
		for end > i+1 && isBlank(lines[end-1]) {
			end--
		}
		out = append(out, yblock{m[1], i, end})
		i = end - 1
	}
	return out
}

// scalarAfterColon достаёт значение "key: value # comment" -> value (без кавычек).
func scalarAfterColon(line string) string {
	i := strings.Index(line, ":")
	if i < 0 {
		return ""
	}
	return unquoteScalar(strings.TrimSpace(line[i+1:]))
}

func unquoteScalar(v string) string {
	if v == "" {
		return ""
	}
	switch v[0] {
	case '"':
		for i := 1; i < len(v); i++ {
			if v[i] == '\\' {
				i++
				continue
			}
			if v[i] == '"' {
				if s, err := strconv.Unquote(v[:i+1]); err == nil {
					return s
				}
				return v[1:i]
			}
		}
		return strings.Trim(v, `"`)
	case '\'':
		if end := strings.Index(v[1:], "'"); end >= 0 {
			return v[1 : end+1]
		}
		return strings.Trim(v, "'")
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

func indentOf(l string) int { return len(l) - len(strings.TrimLeft(l, " \t")) }

// adaptConfig - сборка без знания о сети ПК (тесты, проверка шаблонов).
func adaptConfig(src string, s Settings, servers []Server) (*Adapted, error) {
	return adaptConfigEnv(src, s, servers, Env{IPv6: detectIPv6()})
}

func adaptConfigEnv(src string, s Settings, servers []Server, env Env) (*Adapted, error) {
	lines := splitLines(src)
	blocks := topBlocks(lines)
	if len(blocks) == 0 {
		return nil, errors.New("конфиг пустой или это не YAML")
	}
	idx := map[string]yblock{}
	for _, b := range blocks {
		if _, dup := idx[b.key]; dup {
			return nil, fmt.Errorf("ключ %q встречается в конфиге дважды (строка %d)", b.key, b.start+1)
		}
		idx[b.key] = b
	}
	_, hasProxies := idx["proxies"]
	_, hasProviders := idx["proxy-providers"]
	res := &Adapted{Port: s.ControllerPort, NoServers: !hasProxies && !hasProviders && len(servers) == 0}
	var changes []string
	drop := make([]bool, len(lines))
	repl := map[int]string{} // строки, заменённые на месте (номера строк не сдвигаются)
	dropBlock := func(k string) {
		b := idx[k]
		for i := b.start; i < b.end; i++ {
			drop[i] = true
		}
	}
	var pc []string                       // блок для ПК, дописывается в конец
	insertAt, insert := -1, []string(nil) // вставка внутрь существующего блока

	var removed []string
	for _, k := range routerOnlyKeys {
		if _, ok := idx[k]; ok {
			dropBlock(k)
			removed = append(removed, k)
		}
	}
	if len(removed) > 0 {
		changes = append(changes, "Убраны настройки роутера: "+strings.Join(removed, ", "))
	}

	if b, ok := idx["allow-lan"]; ok {
		dropBlock("allow-lan")
		if v := scalarAfterColon(lines[b.start]); v != "false" {
			changes = append(changes, "allow-lan: "+v+" -> false: прокси и DNS не видны другим устройствам")
		}
	}
	pc = append(pc, "allow-lan: false")

	ctrl := fmt.Sprintf("127.0.0.1:%d", s.ControllerPort)
	if b, ok := idx["external-controller"]; ok {
		dropBlock("external-controller")
		if v := scalarAfterColon(lines[b.start]); v != ctrl {
			changes = append(changes, "external-controller: "+v+" -> "+ctrl+": панель доступна только с этого ПК")
		}
	} else {
		changes = append(changes, "Добавлен external-controller: "+ctrl)
	}
	pc = append(pc, "external-controller: "+ctrl)

	if b, ok := idx["secret"]; ok {
		res.Secret = scalarAfterColon(lines[b.start])
		if res.Secret == "" {
			dropBlock("secret")
		}
	}
	if res.Secret == "" {
		res.Secret = s.Secret
		pc = append(pc, "secret: "+s.Secret)
		changes = append(changes, "Добавлен secret для панели управления")
	}

	if b, ok := idx["find-process-mode"]; ok {
		if v := scalarAfterColon(lines[b.start]); v == "off" {
			dropBlock("find-process-mode")
			pc = append(pc, "find-process-mode: strict")
			changes = append(changes, "find-process-mode: off -> strict: работают правила PROCESS-NAME")
		}
	}

	if _, ok := idx["external-ui"]; !ok {
		pc = append(pc, "external-ui: zashboard")
		if _, ok := idx["external-ui-url"]; !ok {
			pc = append(pc, "external-ui-url: https://github.com/Zephyruso/zashboard/releases/latest/download/dist.zip")
		}
		changes = append(changes, "Добавлена панель zashboard (external-ui)")
	}

	// Без IPv6 у ПК ядро не должно раздавать адреса IPv6: соединения на них
	// падают с bind6 и приложения ждут перехода на IPv4.
	ipv6 := env.IPv6
	if !ipv6 {
		if _, ok := idx["ipv6"]; ok {
			dropBlock("ipv6")
		}
		pc = append(pc, "ipv6: false")
		changes = append(changes, "ipv6: false - у этого ПК нет IPv6")
	}

	if b, ok := idx["dns"]; ok && env.Split {
		// блок пересобирается целиком в конце: так номера строк не сдвигаются
		dropBlock("dns")
		dl, notes := splitDNSBlock(lines[b.start+1:b.end], env)
		pc = append(pc, dl...)
		changes = append(changes, notes...)
	} else if ok {
		child := -1
		for i := b.start + 1; i < b.end; i++ {
			l := lines[i]
			if isBlank(l) || strings.HasPrefix(strings.TrimSpace(l), "#") {
				continue
			}
			if child < 0 {
				child = indentOf(l)
			}
			if indentOf(l) != child {
				continue
			}
			t := strings.TrimSpace(l)
			switch {
			case strings.HasPrefix(t, "listen:"):
				drop[i] = true
				changes = append(changes, "В dns убран listen: "+scalarAfterColon(l)+" - DNS перехватывается через TUN")
			case strings.HasPrefix(t, "ipv6:") && !ipv6 && scalarAfterColon(l) == "true":
				repl[i] = l[:child] + "ipv6: false # [Mejgorod] было true: у ПК нет IPv6"
			}
		}
	} else {
		mode := "redir-host"
		if env.Split {
			mode = "fake-ip"
		}
		pc = append(pc,
			"dns:",
			"  enable: true",
			"  ipv6: "+strconv.FormatBool(ipv6),
			"  enhanced-mode: "+mode,
			"  default-nameserver: [77.88.8.8, 1.1.1.1]",
			"  nameserver: [https://1.1.1.1/dns-query, https://8.8.8.8/dns-query]",
		)
		if env.Split {
			pc = append(pc, "  fake-ip-filter:")
			pc = append(pc, yamlItems(env.fakeIPFilter(), "    ")...)
			if pol := env.nameserverPolicy(); len(pol) > 0 {
				pc = append(pc, "  nameserver-policy:")
				pc = append(pc, policyLines(pol, "    ")...)
			}
		}
		changes = append(changes, "Добавлен блок dns: в конфиге его не было (на роутере его пишет XKeen)")
	}

	if _, ok := idx["tun"]; ok {
		dropBlock("tun")
		changes = append(changes, "Блок tun из конфига заменён настройками программы")
	} else {
		if env.Split {
			changes = append(changes, "Добавлен блок tun")
		} else {
			changes = append(changes, "Добавлен блок tun: весь трафик ПК идёт через mihomo")
		}
	}
	stack := s.TunStack
	if env.CorpVPN != nil && stack != "gvisor" {
		// system и mixed отдают TCP обратно в стек Windows, а Citrix такие пакеты сбрасывает
		stack = "gvisor"
		changes = append(changes, "Стек TUN "+s.TunStack+" -> gvisor: "+env.CorpVPN.Name+" сбрасывает соединения других стеков")
	}
	pc = append(pc,
		"tun:",
		"  enable: "+strconv.FormatBool(!devNoTun),
		"  device: "+tunDevice,
		"  stack: "+stack,
		"  auto-route: true",
		"  auto-detect-interface: true",
		"  strict-route: "+strconv.FormatBool(s.StrictRoute),
		"  dns-hijack:",
		"    - any:53",
		"    - tcp://any:53",
	)
	if env.Split && len(env.RouteAddress) > 0 {
		pc = append(pc, "  route-address:")
		pc = append(pc, yamlItems(env.RouteAddress, "    ")...)
		who := "корпоративным VPN"
		if env.CorpVPN != nil {
			who = env.CorpVPN.Name
		}
		sets := ""
		if len(env.RouteSets) > 0 {
			sets = " (" + strings.Join(env.RouteSets, ", ") + ")"
		}
		changes = append(changes, fmt.Sprintf("Совместимость с %s: в TUN идут только адреса fake-ip и %d подсетей из правил%s, остальное мимо TUN",
			who, len(env.RouteAddress)-1, sets))
	}
	if len(env.RouteExclude) > 0 {
		pc = append(pc, "  route-exclude-address:")
		pc = append(pc, yamlItems(env.RouteExclude, "    ")...)
		changes = append(changes, fmt.Sprintf("Мимо TUN: адреса серверов и шлюза VPN (%d)", len(env.RouteExclude)))
	}

	if len(servers) > 0 {
		// имена не должны совпадать с прокси конфига: иначе выбор в группе неоднозначен
		taken := map[string]bool{}
		for _, cs := range configServers(src) {
			taken[cs.Name] = true
		}
		fixed := make([]Server, len(servers))
		for i, sv := range servers {
			sv.Name = uniqueName(sv.Name, taken)
			taken[sv.Name] = true
			fixed[i] = sv
		}
		if b, ok := idx["proxy-providers"]; ok {
			if v := strings.TrimSpace(scalarAfterColon(lines[b.start])); v != "" && v != "{}" {
				return nil, errors.New("proxy-providers записан в одну строку: перепишите его блоком, чтобы программа могла добавить свои серверы")
			}
			child := "  "
			for i := b.start + 1; i < b.end; i++ {
				if l := lines[i]; !isBlank(l) && !strings.HasPrefix(strings.TrimSpace(l), "#") {
					child = l[:indentOf(l)]
					break
				}
			}
			pl, err := providerLines(fixed, child)
			if err != nil {
				return nil, err
			}
			if v := strings.TrimSpace(scalarAfterColon(lines[b.start])); v == "{}" {
				dropBlock("proxy-providers")
				pc = append(pc, "proxy-providers:")
				pl, _ = providerLines(fixed, "  ")
				pc = append(pc, pl...)
			} else {
				insertAt, insert = b.end, pl
			}
		} else {
			pl, err := providerLines(fixed, "  ")
			if err != nil {
				return nil, err
			}
			pc = append(pc, "proxy-providers:")
			pc = append(pc, pl...)
		}
		changes = append(changes, fmt.Sprintf("Добавлены серверы из вкладки «Серверы»: %d", len(servers)))
	}

	// Убранные строки не удаляем, а комментируем: номера строк в ошибках
	// ядра совпадают с редактором.
	var out []string
	for i, l := range lines {
		if i == insertAt {
			out = append(out, insert...)
		}
		if r, ok := repl[i]; ok {
			l = r
		}
		if drop[i] {
			l = "# [Mejgorod] " + l
		}
		out = append(out, l)
	}
	if insertAt >= len(lines) {
		out = append(out, insert...)
	}
	for len(out) > 0 && isBlank(out[len(out)-1]) {
		out = out[:len(out)-1]
	}
	out = append(out, "", "# ===== Mejgorod: настройки для ПК (добавлены автоматически) =====")
	out = append(out, pc...)
	res.Text = strings.Join(out, "\n") + "\n"
	res.Changes = changes
	return res, nil
}

// configStats - сколько серверов и групп в конфиге, для главной страницы.
func configStats(src string) (proxies, groups int) {
	lines := splitLines(src)
	for _, b := range topBlocks(lines) {
		if b.key != "proxies" && b.key != "proxy-groups" {
			continue
		}
		n, child := 0, -1
		for i := b.start + 1; i < b.end; i++ {
			l := lines[i]
			t := strings.TrimSpace(l)
			if !strings.HasPrefix(t, "-") {
				continue
			}
			if child < 0 {
				child = indentOf(l)
			}
			if indentOf(l) == child {
				n++
			}
		}
		if b.key == "proxies" {
			proxies = n
		} else {
			groups = n
		}
	}
	return
}

func yamlItems(items []string, indent string) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, indent+"- "+strconv.Quote(it))
	}
	return out
}

func policyLines(pol [][2]string, indent string) []string {
	out := make([]string, 0, len(pol))
	for _, p := range pol {
		out = append(out, indent+strconv.Quote(p[0])+": "+strconv.Quote(p[1]))
	}
	return out
}

// splitDNSBlock пересобирает блок dns пользователя для режима «только нужное»:
// enhanced-mode: fake-ip, свой fake-ip-filter и DNS корпоративного VPN
// для его доменов. Остальные строки остаются как были.
func splitDNSBlock(body []string, env Env) (out []string, notes []string) {
	child := -1
	for _, l := range body {
		if !isBlank(l) && !strings.HasPrefix(strings.TrimSpace(l), "#") {
			child = indentOf(l)
			break
		}
	}
	if child < 0 {
		child = 2
	}
	pad := strings.Repeat(" ", child)
	keyOf := func(l string) string {
		if isBlank(l) || indentOf(l) != child || strings.HasPrefix(strings.TrimSpace(l), "#") {
			return ""
		}
		t := strings.TrimSpace(l)
		if i := strings.Index(t, ":"); i > 0 {
			return t[:i]
		}
		return ""
	}
	// подблок ключа: строка ключа и всё, что глубже
	sub := func(i int) int {
		j := i + 1
		for j < len(body) && (isBlank(body[j]) || indentOf(body[j]) > child || (strings.HasPrefix(strings.TrimSpace(body[j]), "#") && keyOf(body[j]) == "")) {
			j++
		}
		return j
	}
	parse := func(lines []string) map[string]any {
		var m map[string]any
		txt := make([]string, len(lines))
		for k, l := range lines {
			if len(l) >= child {
				txt[k] = l[child:]
			}
		}
		_ = yaml.Unmarshal([]byte(strings.Join(txt, "\n")), &m)
		return m
	}

	filter := env.fakeIPFilter()
	policy := env.nameserverPolicy()
	var haveMode, haveFilter, havePolicy, whitelist bool
	out = append(out, "dns:")
	for i := 0; i < len(body); {
		l := body[i]
		switch keyOf(l) {
		case "listen":
			notes = append(notes, "В dns убран listen: "+scalarAfterColon(l)+" - DNS перехватывается через TUN")
			i++
			continue
		case "ipv6":
			if !env.IPv6 && scalarAfterColon(l) == "true" {
				l = pad + "ipv6: false # [Mejgorod] было true: у ПК нет IPv6"
			}
		case "enhanced-mode":
			haveMode = true
			if v := scalarAfterColon(l); v != "fake-ip" {
				l = pad + "enhanced-mode: fake-ip # [Mejgorod] было " + v
				notes = append(notes, "dns: enhanced-mode "+v+" -> fake-ip: так в TUN попадает только нужное")
			}
		case "fake-ip-filter-mode":
			whitelist = scalarAfterColon(l) == "whitelist"
		case "fake-ip-filter":
			haveFilter = true
			j := sub(i)
			if m := parse(body[i:j]); m != nil {
				if items, ok := m["fake-ip-filter"].([]any); ok {
					var have []string
					for _, it := range items {
						have = append(have, fmt.Sprint(it))
					}
					filter = mergeUnique(have, filter)
				}
			}
			if whitelist {
				out = append(out, body[i:j]...)
			} else {
				out = append(out, pad+"fake-ip-filter:")
				out = append(out, yamlItems(filter, pad+"  ")...)
			}
			i = j
			continue
		case "nameserver-policy":
			havePolicy = true
			j := sub(i)
			m := parse(body[i:j])
			have := map[string]bool{}
			if mm, ok := m["nameserver-policy"].(map[string]any); ok {
				for k := range mm {
					have[k] = true
				}
			}
			var add [][2]string
			for _, p := range policy {
				if !have[p[0]] {
					add = append(add, p)
				}
			}
			if v := strings.TrimSpace(scalarAfterColon(l)); v != "" && strings.HasPrefix(v, "{") {
				// в одну строку: дописать в неё нельзя, оставляем как есть
				out = append(out, body[i:j]...)
				if len(add) > 0 {
					notes = append(notes, "dns: nameserver-policy записан в одну строку, домены VPN в него не добавлены")
				}
			} else {
				inner := pad + "  "
				for _, x := range body[i+1 : j] {
					if !isBlank(x) && !strings.HasPrefix(strings.TrimSpace(x), "#") {
						inner = x[:indentOf(x)]
						break
					}
				}
				out = append(out, l)
				out = append(out, policyLines(add, inner)...)
				out = append(out, body[i+1:j]...)
			}
			i = j
			continue
		}
		out = append(out, l)
		i++
	}
	if !haveMode {
		out = append(out, pad+"enhanced-mode: fake-ip")
		notes = append(notes, "dns: enhanced-mode fake-ip - так в TUN попадает только нужное")
	}
	if !haveFilter && !whitelist {
		out = append(out, pad+"fake-ip-filter:")
		out = append(out, yamlItems(filter, pad+"  ")...)
	}
	if !havePolicy && len(policy) > 0 {
		out = append(out, pad+"nameserver-policy:")
		out = append(out, policyLines(policy, pad+"  ")...)
	}
	if len(policy) > 0 {
		notes = append(notes, fmt.Sprintf("dns: %d внутренних доменов VPN резолвит его DNS %s", len(policy), policy[0][1]))
	}
	for len(out) > 1 && isBlank(out[len(out)-1]) {
		out = out[:len(out)-1]
	}
	return out, notes
}

func mergeUnique(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range append(append([]string(nil), a...), b...) {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
