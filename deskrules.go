package main

// Свои сайты и программы с вкладки «Группы» живут прямо в конфиге, в разделах
// между метками программы: так их видно в редакторе и можно перенести на роутер.
//
//	rules:
//	  # MihomoDesk: свои сайты и программы (вкладка «Группы»)
//	  - OR,((DOMAIN-SUFFIX,instagram.com),(RULE-SET,instagram@domain)),Заблок. сервисы # desk: instagram.com
//	  - PROCESS-NAME,Telegram.exe,DIRECT # desk: Telegram.exe
//	  # MihomoDesk: конец
//
// Раздел стоит первым в rules: явный выбор пользователя важнее остальных правил.
// Списки geosite/geoip для сайтов - в таком же разделе в конце rule-providers.

import (
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	deskRulesBegin = "# MihomoDesk: свои сайты и программы (вкладка «Группы»)"
	deskProvBegin  = "# MihomoDesk: списки для своих сайтов"
	deskEnd        = "# MihomoDesk: конец"
	deskTag        = " # desk: "
)

type deskRule struct {
	Label  string
	Body   string // правило без цели: DOMAIN-SUFFIX,x | OR,((...)) | PROCESS-NAME,x
	Target string
}

type deskProvider struct {
	Name     string
	Behavior string // domain | ipcidr
	URL      string
}

func (r deskRule) kind() string {
	if strings.HasPrefix(r.Body, "PROCESS-") {
		return "app"
	}
	return "site"
}

func deskID(label string) string {
	h := fnv.New64a()
	h.Write([]byte(strings.ToLower(label)))
	return strconv.FormatUint(h.Sum64(), 36)
}

func (r deskRule) line() string { return r.Body + "," + r.Target + deskTag + r.Label }

// deskSection - строки раздела [begin, end] внутри блока key, -1 если нет.
func deskSection(lines []string, b yblock, begin string) (int, int) {
	for i := b.start + 1; i < b.end; i++ {
		if strings.TrimSpace(lines[i]) != begin {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == deskEnd {
				return i, j
			}
		}
		return i, b.end - 1
	}
	return -1, -1
}

func findBlock(lines []string, key string) (yblock, bool) {
	for _, b := range topBlocks(lines) {
		if b.key == key {
			return b, true
		}
	}
	return yblock{}, false
}

// readDesk достаёт из конфига свои правила и списки.
func readDesk(src string) (rules []deskRule, provs []deskProvider) {
	lines := splitLines(src)
	if b, ok := findBlock(lines, "rules"); ok {
		if s, e := deskSection(lines, b, deskRulesBegin); s >= 0 {
			for _, l := range lines[s+1 : e] {
				t := strings.TrimSpace(l)
				if !strings.HasPrefix(t, "- ") {
					continue
				}
				t = strings.TrimSpace(t[2:])
				label := ""
				if i := strings.Index(t, deskTag); i >= 0 {
					t, label = strings.TrimSpace(t[:i]), strings.TrimSpace(t[i+len(deskTag):])
				}
				target, _ := parseRule(t)
				if target == "" {
					continue
				}
				body := strings.TrimSuffix(t, ","+target)
				if label == "" {
					label = body
				}
				rules = append(rules, deskRule{Label: label, Body: body, Target: target})
			}
		}
	}
	if b, ok := findBlock(lines, "rule-providers"); ok {
		if s, e := deskSection(lines, b, deskProvBegin); s >= 0 {
			for _, l := range lines[s+1 : e] {
				var m map[string]cfgRuleProvider
				if yaml.Unmarshal([]byte(strings.TrimSpace(l)), &m) != nil {
					continue
				}
				for name, p := range m {
					provs = append(provs, deskProvider{Name: name, Behavior: p.Behavior, URL: p.URL})
				}
			}
		}
	}
	return rules, provs
}

func deskItemsView(src string) []BoardItem {
	rules, _ := readDesk(src)
	items := []BoardItem{}
	for _, r := range rules {
		route := "vpn"
		if rt := routeOf(r.Target); rt != "vpn" {
			route = "direct"
		}
		detail := "программа, весь трафик"
		if r.kind() == "site" {
			n := strings.Count(r.Body, "DOMAIN-SUFFIX,")
			parts := []string{}
			if n > 0 {
				parts = append(parts, fmt.Sprintf("доменов: %d", n))
			}
			for _, t := range strings.Split(r.Body, "(RULE-SET,")[1:] {
				name := strings.SplitN(strings.SplitN(t, ")", 2)[0], ",", 2)[0]
				parts = append(parts, "список "+name)
			}
			detail = "сайт · " + strings.Join(parts, " · ")
		}
		items = append(items, BoardItem{ID: deskID(r.Label), Kind: r.kind(), Label: r.Label, Route: route, Detail: detail})
	}
	return items
}

// writeDesk заменяет разделы программы в конфиге (или создаёт их).
func writeDesk(src string, rules []deskRule, provs []deskProvider) (string, error) {
	trailingNL := strings.HasSuffix(src, "\n")
	lines := splitLines(src)
	if trailingNL && len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	// старые разделы убираем
	for _, key := range []string{"rules", "rule-providers"} {
		begin := deskRulesBegin
		if key == "rule-providers" {
			begin = deskProvBegin
		}
		if b, ok := findBlock(lines, key); ok {
			if s, e := deskSection(lines, b, begin); s >= 0 {
				lines = append(lines[:s:s], lines[e+1:]...)
			}
		}
	}
	// списки - в конец rule-providers (он обычно выше rules)
	if len(provs) > 0 {
		sec := []string{deskProvBegin}
		for _, p := range provs {
			sec = append(sec, fmt.Sprintf("%s: { type: http, format: mrs, behavior: %s, interval: 86400, url: %s }",
				p.Name, p.Behavior, strconv.Quote(p.URL)))
		}
		sec = append(sec, deskEnd)
		var err error
		if lines, err = insertIntoBlock(lines, "rule-providers", sec, false); err != nil {
			return "", err
		}
	}
	// правила - первыми в rules
	if len(rules) > 0 {
		sec := []string{deskRulesBegin}
		for _, r := range rules {
			sec = append(sec, "- "+r.line())
		}
		sec = append(sec, deskEnd)
		var err error
		if lines, err = insertIntoBlock(lines, "rules", sec, true); err != nil {
			return "", err
		}
	}
	out := strings.Join(lines, "\n")
	if trailingNL {
		out += "\n"
	}
	var check map[string]any
	if err := yaml.Unmarshal([]byte(out), &check); err != nil {
		return "", fmt.Errorf("после правки конфиг перестал разбираться: %w", err)
	}
	return out, nil
}

// insertIntoBlock вставляет строки в блок верхнего уровня с отступом его
// элементов: в начало (first) или в конец. Нет блока - дописывает его в конец.
func insertIntoBlock(lines []string, key string, sec []string, first bool) ([]string, error) {
	b, ok := findBlock(lines, key)
	if !ok {
		for len(lines) > 0 && isBlank(lines[len(lines)-1]) {
			lines = lines[:len(lines)-1]
		}
		lines = append(lines, "", key+":")
		for _, l := range sec {
			lines = append(lines, "  "+l)
		}
		return lines, nil
	}
	if v := strings.TrimSpace(scalarAfterColon(lines[b.start])); v != "" {
		return nil, fmt.Errorf("%s записан в одну строку: перепишите его блоком, чтобы программа могла добавить правила", key)
	}
	indent := "  "
	for i := b.start + 1; i < b.end; i++ {
		if l := lines[i]; !isBlank(l) && !strings.HasPrefix(strings.TrimSpace(l), "#") {
			indent = l[:indentOf(l)]
			break
		}
	}
	at := b.end
	if first {
		at = b.start + 1
	}
	ins := make([]string, len(sec))
	for i, l := range sec {
		ins[i] = indent + l
	}
	out := append(append(append([]string{}, lines[:at]...), ins...), lines[at:]...)
	return out, nil
}

// siteRuleBody - условие правила для сайта: его домены и найденные списки.
func siteRuleBody(domains []string, geosite, geoip string) (string, error) {
	var terms []string
	for _, d := range domains {
		terms = append(terms, "DOMAIN-SUFFIX,"+d)
	}
	if geosite != "" {
		terms = append(terms, "RULE-SET,"+geosite)
	}
	if geoip != "" {
		terms = append(terms, "RULE-SET,"+geoip+",no-resolve")
	}
	switch len(terms) {
	case 0:
		return "", errors.New("нечего добавлять: нет ни доменов, ни списков")
	case 1:
		if geoip == "" {
			return terms[0], nil
		}
	}
	return "OR,((" + strings.Join(terms, "),(") + "))", nil
}

// providerFor - имя списка в конфиге для url: существующий с тем же url
// или новый свободный. added = true, если его нужно дописать.
func providerFor(src string, desk []deskProvider, cat, behavior, url string) (name string, added bool) {
	var doc struct {
		RuleProviders map[string]cfgRuleProvider `yaml:"rule-providers"`
	}
	_ = yaml.Unmarshal([]byte(src), &doc)
	for n, p := range doc.RuleProviders {
		if p.URL == url {
			isDesk := false
			for _, d := range desk {
				isDesk = isDesk || d.Name == n
			}
			return n, isDesk
		}
	}
	name = cat + "@" + behavior
	for i := 2; ; i++ {
		if _, taken := doc.RuleProviders[name]; !taken {
			return name, true
		}
		name = fmt.Sprintf("%s-%d@%s", cat, i, behavior)
	}
}
