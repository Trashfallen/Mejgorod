package main

// Маршруты TUN. Обычно TUN забирает весь трафик (route 0.0.0.0/0). Рядом
// с корпоративным VPN так нельзя (см. CorpVPN), и программа включает режим
// «только нужное»: DNS отдаёт приложениям подменные адреса (fake-ip), в TUN
// заворачиваются только они и подсети из правил, которые ведут в VPN.
// Соединения самого ядра (DIRECT, серверы, DNS) идут на настоящие адреса,
// мимо TUN, поэтому петли не бывает, даже если кто-то перекинет пакет.

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

const defaultFakeIPRange = "198.18.0.1/16"

// Env - что программа знает про ПК и сеть при сборке конфига.
type Env struct {
	IPv6         bool
	CorpVPN      *CorpVPN
	Split        bool     // TUN только для fake-ip и нужных подсетей
	RouteAddress []string // split: что заворачивать в TUN
	RouteExclude []string // никогда не заворачивать: серверы, шлюз VPN
	RouteSets    []string // списки правил, попавшие в RouteAddress
	MissingSets  []string // списки, которых ещё нет на диске
}

// Windows и локальная сеть: им нужны настоящие адреса.
var baseFakeIPFilter = []string{
	"+.lan", "+.local", "+.localdomain", "+.home.arpa", "+.internal",
	"+.msftconnecttest.com", "+.msftncsi.com", "time.windows.com", "+.pool.ntp.org",
	"stun.*.*", "stun.*.*.*",
}

type cfgRuleProvider struct {
	Type     string   `yaml:"type"`
	Behavior string   `yaml:"behavior"`
	Format   string   `yaml:"format"`
	URL      string   `yaml:"url"`
	Path     string   `yaml:"path"`
	Payload  []string `yaml:"payload"`
}

type cfgDoc struct {
	ProxyGroups []struct {
		Name    string   `yaml:"name"`
		Proxies []string `yaml:"proxies"`
	} `yaml:"proxy-groups"`
	RuleProviders map[string]cfgRuleProvider `yaml:"rule-providers"`
	Rules         []string                   `yaml:"rules"`
	Proxies       []struct {
		Server string `yaml:"server"`
	} `yaml:"proxies"`
	DNS struct {
		FakeIPRange string `yaml:"fake-ip-range"`
	} `yaml:"dns"`
}

// Что делает правило без VPN.
var notProxied = map[string]bool{"DIRECT": true, "REJECT": true, "REJECT-DROP": true, "PASS": true, "COMPATIBLE": true}

// proxiedTarget - уйдёт ли трафик правила в VPN при выборе групп по умолчанию
// (первый вариант группы).
func proxiedTarget(target string, groups map[string][]string) bool {
	for depth := 0; depth < 8; depth++ {
		if notProxied[target] {
			return false
		}
		opts, isGroup := groups[target]
		if !isGroup || len(opts) == 0 {
			return true // сервер или группа из провайдеров
		}
		target = opts[0]
	}
	return true
}

var (
	logicRuleRe  = regexp.MustCompile(`^(AND|OR|NOT|SUB-RULE),`)
	nestedTermRe = regexp.MustCompile(`\(([A-Z0-9-]+),([^,()]+)((?:,[^,()]+)*)\)`)
)

type ruleRef struct {
	kind      string // RULE-SET, IP-CIDR, IP-CIDR6
	value     string
	noResolve bool
}

// parseRule разбирает правило на цель и ссылки на списки/подсети внутри.
func parseRule(rule string) (target string, refs []ruleRef) {
	rule = strings.TrimSpace(rule)
	if logicRuleRe.MatchString(rule) {
		i := strings.LastIndex(rule, "))")
		if i < 0 {
			return "", nil
		}
		tail := strings.Split(strings.TrimPrefix(rule[i+2:], ","), ",")
		target = strings.TrimSpace(tail[0])
		for _, m := range nestedTermRe.FindAllStringSubmatch(rule[:i+2], -1) {
			refs = append(refs, ruleRef{m[1], strings.TrimSpace(m[2]), strings.Contains(m[3], "no-resolve")})
		}
		return target, refs
	}
	f := strings.Split(rule, ",")
	for i := range f {
		f[i] = strings.TrimSpace(f[i])
	}
	if f[0] == "MATCH" {
		if len(f) > 1 {
			return f[1], nil
		}
		return "", nil
	}
	if len(f) < 3 {
		return "", nil
	}
	nr := false
	for _, x := range f[3:] {
		nr = nr || x == "no-resolve"
	}
	return f[2], []ruleRef{{f[0], f[1], nr}}
}

// splitPlan - что из правил должно попасть в TUN в режиме «только нужное».
type splitPlan struct {
	CIDRs     []string // подсети прямо из правил и inline-списков
	IPSets    []string // списки ipcidr, использованные с no-resolve
	FakeRange string
}

func planSplit(src string) (*splitPlan, *cfgDoc, error) {
	var doc cfgDoc
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		return nil, nil, err
	}
	groups := map[string][]string{}
	for _, g := range doc.ProxyGroups {
		groups[g.Name] = g.Proxies
	}
	plan := &splitPlan{FakeRange: defaultFakeIPRange}
	if doc.DNS.FakeIPRange != "" {
		plan.FakeRange = doc.DNS.FakeIPRange
	}
	seenCIDR, seenSet := map[string]bool{}, map[string]bool{}
	addCIDR := func(c string) {
		if p, err := netip.ParsePrefix(c); err == nil && !seenCIDR[p.Masked().String()] {
			seenCIDR[p.Masked().String()] = true
			plan.CIDRs = append(plan.CIDRs, p.Masked().String())
		}
	}
	for _, rule := range doc.Rules {
		target, refs := parseRule(rule)
		if target == "" || !proxiedTarget(target, groups) {
			continue
		}
		for _, r := range refs {
			switch r.kind {
			case "IP-CIDR", "IP-CIDR6":
				addCIDR(r.value)
			case "RULE-SET":
				p, ok := doc.RuleProviders[r.value]
				if !ok {
					continue
				}
				switch {
				case p.Behavior == "classical" && p.Type == "inline":
					for _, e := range p.Payload {
						if f := strings.Split(e, ","); len(f) >= 2 && (f[0] == "IP-CIDR" || f[0] == "IP-CIDR6") {
							addCIDR(strings.TrimSpace(f[1]))
						}
					}
				case p.Behavior == "ipcidr" && p.Type == "inline":
					for _, e := range p.Payload {
						addCIDR(strings.TrimSpace(e))
					}
				case p.Behavior == "ipcidr" && r.noResolve && !seenSet[r.value]:
					// без no-resolve список проверяется по адресу домена: fake-ip его и так покроет
					seenSet[r.value] = true
					plan.IPSets = append(plan.IPSets, r.value)
				}
			}
		}
	}
	sort.Strings(plan.IPSets)
	return plan, &doc, nil
}

// providerFile - где ядро хранит скачанный список (как в mihomo: rules/md5(url)).
func providerFile(home string, p cfgRuleProvider) string {
	if p.Path != "" {
		if filepath.IsAbs(p.Path) {
			return p.Path
		}
		return filepath.Join(home, p.Path)
	}
	sum := md5.Sum([]byte(p.URL))
	return filepath.Join(home, "rules", hex.EncodeToString(sum[:]))
}

// readIPSet читает подсети из скачанного списка ipcidr (mrs - через ядро).
func readIPSet(coreExe, home string, p cfgRuleProvider) ([]string, error) {
	file := providerFile(home, p)
	if _, err := os.Stat(file); err != nil {
		return nil, err
	}
	var text []byte
	switch p.Format {
	case "mrs":
		tmp, err := os.CreateTemp("", "mihomodesk-*.txt")
		if err != nil {
			return nil, err
		}
		tmp.Close()
		defer os.Remove(tmp.Name())
		cmd := exec.Command(coreExe, "convert-ruleset", "ipcidr", "mrs", file, tmp.Name())
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		}
		if text, err = os.ReadFile(tmp.Name()); err != nil {
			return nil, err
		}
	case "yaml":
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		var y struct {
			Payload []string `yaml:"payload"`
		}
		if err := yaml.Unmarshal(b, &y); err != nil {
			return nil, err
		}
		text = []byte(strings.Join(y.Payload, "\n"))
	default: // text
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		text = b
	}
	var out []string
	for _, l := range strings.Split(string(text), "\n") {
		l = strings.TrimSpace(strings.SplitN(l, "#", 2)[0])
		if p, err := netip.ParsePrefix(l); err == nil {
			out = append(out, p.Masked().String())
		}
	}
	return out, nil
}

var serverRe = regexp.MustCompile(`(?:^|[{,\s])server:\s*['"]?([^'",}\s]+)`)

// serverIPs - адреса серверов из конфига и вкладки «Серверы»: к ним ядро
// подключается само, в TUN их пускать нельзя.
func serverIPs(doc *cfgDoc, servers []Server) []string {
	var hosts []string
	if doc != nil {
		for _, p := range doc.Proxies {
			hosts = append(hosts, p.Server)
		}
	}
	for _, s := range servers {
		if m := serverRe.FindStringSubmatch(s.YAML); m != nil {
			hosts = append(hosts, m[1])
		}
	}
	return resolveAll(hosts)
}

func resolveAll(hosts []string) []string {
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out = map[string]bool{}
	)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	for _, h := range hosts {
		h = strings.Trim(strings.TrimSpace(h), "[]")
		if h == "" {
			continue
		}
		if ip, err := netip.ParseAddr(h); err == nil {
			out[netip.PrefixFrom(ip, ip.BitLen()).String()] = true
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ips, _ := net.DefaultResolver.LookupNetIP(ctx, "ip", h)
			mu.Lock()
			for _, ip := range ips {
				ip = ip.Unmap()
				out[netip.PrefixFrom(ip, ip.BitLen()).String()] = true
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	list := make([]string, 0, len(out))
	for k := range out {
		list = append(list, k)
	}
	sort.Strings(list)
	return list
}

// buildEnv собирает Env для конфига: сеть ПК, режим маршрутов, подсети.
func (a *App) buildEnv(src string, servers []Server) Env {
	env := Env{IPv6: detectIPv6()}
	set := a.settings.Get()
	env.CorpVPN = detectCorpVPN()
	switch set.TunRoute {
	case "split":
		env.Split = true
	case "full":
	default:
		env.Split = env.CorpVPN != nil
	}
	plan, doc, err := planSplit(src)
	if err != nil {
		doc = nil
	}
	env.RouteExclude = serverIPs(doc, servers)
	if env.CorpVPN != nil {
		for _, ip := range env.CorpVPN.GatewayIPs {
			env.RouteExclude = append(env.RouteExclude, ip+"/32")
		}
	}
	if !env.Split || plan == nil {
		return env
	}
	if p, err := netip.ParsePrefix(plan.FakeRange); err == nil {
		env.RouteAddress = append(env.RouteAddress, p.Masked().String())
	}
	env.RouteAddress = append(env.RouteAddress, plan.CIDRs...)
	for _, name := range plan.IPSets {
		cidrs, err := readIPSet(a.paths.CoreExe, a.paths.Home, doc.RuleProviders[name])
		if err != nil {
			env.MissingSets = append(env.MissingSets, name)
			continue
		}
		env.RouteSets = append(env.RouteSets, name)
		env.RouteAddress = append(env.RouteAddress, cidrs...)
	}
	if !env.IPv6 {
		v4 := env.RouteAddress[:0]
		for _, c := range env.RouteAddress {
			if !strings.Contains(c, ":") {
				v4 = append(v4, c)
			}
		}
		env.RouteAddress = v4
	}
	return env
}

// fakeIPFilter - домены, которым нужен настоящий адрес.
func (e Env) fakeIPFilter() []string {
	out := append([]string(nil), baseFakeIPFilter...)
	if e.CorpVPN != nil {
		for _, s := range e.CorpVPN.Suffixes {
			out = append(out, "+."+s)
		}
		if g := e.CorpVPN.Gateway; g != "" {
			out = append(out, g)
		}
	}
	return out
}

// nameserverPolicy - внутренние домены VPN резолвит его DNS.
func (e Env) nameserverPolicy() [][2]string {
	if e.CorpVPN == nil || len(e.CorpVPN.DNS) == 0 {
		return nil
	}
	var out [][2]string
	for _, s := range e.CorpVPN.Suffixes {
		out = append(out, [2]string{"+." + s, e.CorpVPN.DNS[0]})
	}
	return out
}
