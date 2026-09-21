package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseRule(t *testing.T) {
	cases := []struct {
		rule, target string
		refs         []ruleRef
	}{
		{"RULE-SET,ru@ipcidr,RU сайты,no-resolve", "RU сайты", []ruleRef{{"RULE-SET", "ru@ipcidr", true}}},
		{"IP-CIDR,1.2.3.0/24,WoW", "WoW", []ruleRef{{"IP-CIDR", "1.2.3.0/24", false}}},
		{"MATCH,DIRECT", "DIRECT", nil},
		{"OR,((RULE-SET,meta@domain),(RULE-SET,meta@ipcidr,no-resolve)),Meta", "Meta",
			[]ruleRef{{"RULE-SET", "meta@domain", false}, {"RULE-SET", "meta@ipcidr", true}}},
		{"AND,((NETWORK,TCP),(NOT,((DST-PORT,80/443/8080/8443))),(RULE-SET,ovh@ipcidr,no-resolve)),WoW", "WoW",
			[]ruleRef{{"NETWORK", "TCP", false}, {"DST-PORT", "80/443/8080/8443", false}, {"RULE-SET", "ovh@ipcidr", true}}},
	}
	for _, c := range cases {
		target, refs := parseRule(c.rule)
		if target != c.target || !slices.Equal(refs, c.refs) {
			t.Errorf("%s:\n got %q %v\nwant %q %v", c.rule, target, refs, c.target, c.refs)
		}
	}
}

const splitSample = `anchors:
  a2: &ipcidr { type: http, format: mrs, behavior: ipcidr, interval: 86400 }
  a4: &inline { type: inline, behavior: classical }
proxy-groups:
  - {name: VPN, type: select, proxies: [srv]}
  - {name: RU, type: select, proxies: [DIRECT, VPN]}
  - {name: CDN, type: select, proxies: [PASS, VPN]}
rule-providers:
  game@inline:
    <<: *inline
    payload:
      - DOMAIN-SUFFIX,game.example
      - IP-CIDR,5.6.7.8/32,no-resolve
  tg@ipcidr: { <<: *ipcidr, url: https://example.com/tg.mrs }
  ru@ipcidr: { <<: *ipcidr, url: https://example.com/ru.mrs }
  cf@ipcidr: { <<: *ipcidr, url: https://example.com/cf.mrs }
rules:
  - RULE-SET,game@inline,VPN
  - RULE-SET,tg@ipcidr,VPN,no-resolve
  - RULE-SET,ru@ipcidr,RU,no-resolve
  - RULE-SET,cf@ipcidr,VPN
  - IP-CIDR,9.9.9.0/24,CDN
  - IP-CIDR,8.8.0.0/16,VPN,no-resolve
  - MATCH,DIRECT
proxies:
  - {name: srv, type: vless, server: 203.0.113.5, port: 443, uuid: 11111111-2222-3333-4444-555555555555}
dns:
  enable: true
  ipv6: true
  enhanced-mode: redir-host
  nameserver: [https://1.1.1.1/dns-query]
`

func TestPlanSplit(t *testing.T) {
	plan, doc, err := planSplit(splitSample)
	if err != nil {
		t.Fatal(err)
	}
	// ru - группа RU по умолчанию DIRECT; cf - без no-resolve; 9.9.9.0/24 - CDN начинается с PASS
	if !slices.Equal(plan.IPSets, []string{"tg@ipcidr"}) {
		t.Errorf("sets = %v", plan.IPSets)
	}
	if !slices.Equal(plan.CIDRs, []string{"5.6.7.8/32", "8.8.0.0/16"}) {
		t.Errorf("cidrs = %v", plan.CIDRs)
	}
	if got := serverIPs(doc, nil); !slices.Equal(got, []string{"203.0.113.5/32"}) {
		t.Errorf("servers = %v", got)
	}
}

func TestAdaptSplit(t *testing.T) {
	env := Env{
		Split:        true,
		CorpVPN:      &CorpVPN{Name: "Citrix Secure Access", DNS: []string{"10.9.9.9"}, Suffixes: []string{"corp.example"}, Gateway: "gw.corp.example"},
		RouteAddress: []string{"198.18.0.0/16", "5.6.7.8/32"},
		RouteExclude: []string{"203.0.113.5/32"},
	}
	ad, err := adaptConfigEnv(splitSample, defaultSettings(), nil, env)
	if err != nil {
		t.Fatal(err)
	}
	in, out := splitLines(splitSample), splitLines(ad.Text)
	for i, l := range in { // номера строк не сдвинулись
		if strings.TrimSpace(l) != "" && out[i] != l && out[i] != "# [MihomoDesk] "+l {
			t.Fatalf("line %d: %q -> %q", i+1, l, out[i])
		}
	}
	for _, want := range []string{
		"# [MihomoDesk] dns:",
		"  enhanced-mode: fake-ip # [MihomoDesk] было redir-host",
		`    - "+.corp.example"`,
		`    "+.corp.example": "10.9.9.9"`,
		"  route-address:\n    - \"198.18.0.0/16\"",
		"  route-exclude-address:\n    - \"203.0.113.5/32\"",
	} {
		if !strings.Contains(ad.Text, want) {
			t.Errorf("no %q in:\n%s", want, ad.Text)
		}
	}
	if core := os.Getenv("MIHOMODESK_CORE"); core != "" {
		dir := t.TempDir()
		f := filepath.Join(dir, "config.yaml")
		os.WriteFile(f, []byte(ad.Text), 0o644)
		if msg, err := testConfig(context.Background(), core, dir, f); err != nil {
			t.Errorf("mihomo -t: %v\n%s", err, msg)
		}
	}
}

// MIHOMODESK_CONFIG + MIHOMODESK_CORE + MIHOMODESK_HOME (папка ядра со списками):
// режим «только нужное» на реальном конфиге.
func TestAdaptSplitRealConfig(t *testing.T) {
	src, core, home := os.Getenv("MIHOMODESK_CONFIG"), os.Getenv("MIHOMODESK_CORE"), os.Getenv("MIHOMODESK_HOME")
	if src == "" || core == "" || home == "" {
		t.Skip("MIHOMODESK_CONFIG/CORE/HOME not set")
	}
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	plan, doc, err := planSplit(string(b))
	if err != nil {
		t.Fatal(err)
	}
	env := Env{Split: true, CorpVPN: detectCorpVPN(), RouteAddress: []string{"198.18.0.0/16"}}
	env.RouteAddress = append(env.RouteAddress, plan.CIDRs...)
	for _, name := range plan.IPSets {
		cidrs, err := readIPSet(core, home, doc.RuleProviders[name])
		t.Logf("set %s: %d prefixes, err=%v", name, len(cidrs), err)
		env.RouteAddress = append(env.RouteAddress, cidrs...)
	}
	env.RouteExclude = serverIPs(doc, testServers)
	t.Logf("plan: cidrs=%v sets=%v exclude=%v corp=%+v", plan.CIDRs, plan.IPSets, env.RouteExclude, env.CorpVPN)
	ad, err := adaptConfigEnv(string(b), defaultSettings(), testServers, env)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range ad.Changes {
		t.Log("change:", c)
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "config.yaml")
	os.WriteFile(f, []byte(ad.Text), 0o644)
	if msg, err := testConfig(context.Background(), core, dir, f); err != nil {
		t.Errorf("mihomo -t: %v\n%s", err, msg)
	}
	if dst := os.Getenv("MIHOMODESK_DUMP"); dst != "" {
		os.WriteFile(dst, []byte(ad.Text), 0o644)
	}
}
