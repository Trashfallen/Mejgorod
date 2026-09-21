package main

import (
	"strings"
	"testing"
)

// Серверы для TestAdaptRealConfig: ссылки разных видов + YAML-блок.
var testServers = []Server{
	{ID: "a", Name: "NL Reality", Link: "vless://11111111-2222-3333-4444-555555555555@nl.example.com:443?type=tcp&security=reality&pbk=AbCdEf0123456789AbCdEf0123456789AbCdEf01234&sid=6ba85179e30d4fc2&sni=www.microsoft.com&fp=chrome&flow=xtls-rprx-vision#NL%20Reality"},
	{ID: "b", Name: "DE WS", Link: "vless://11111111-2222-3333-4444-555555555555@de.example.com:443?type=ws&security=tls&path=%2Fws&host=de.example.com&sni=de.example.com#DE%20WS"},
	{ID: "c", Name: "Test TLS", YAML: "- name: 'Test TLS'\n  type: vless\n  server: 1.2.3.4\n  port: 2084\n  uuid: 11111111-2222-3333-4444-555555555555\n  network: tcp\n  tls: true\n  servername: example.com"},
}

func TestParseLinks(t *testing.T) {
	in := `
vless://11111111-2222-3333-4444-555555555555@nl.example.com:443?type=tcp&security=reality&pbk=KEY&sid=ab&sni=www.microsoft.com&fp=chrome&flow=xtls-rprx-vision#%F0%9F%87%B3%F0%9F%87%B1%20NL
vless://11111111-2222-3333-4444-555555555555@[2001:db8::1]:8443?type=grpc&security=tls&serviceName=svc#v6
vless://nouser.example.com:443
trojan://x@y:1#t
`
	got, errs := parseServersInput(in)
	if len(got) != 2 || len(errs) != 2 {
		t.Fatalf("got %d servers, %d errors: %v", len(got), len(errs), errs)
	}
	if got[0].Name != "🇳🇱 NL" {
		t.Errorf("name = %q", got[0].Name)
	}
	e, err := vlessEntry(got[0].Link, got[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`name: "🇳🇱 NL"`, `server: "nl.example.com"`, `port: 443`, `flow: "xtls-rprx-vision"`, `reality-opts: {public-key: "KEY", short-id: "ab"}`, `client-fingerprint: "chrome"`, `network: "tcp"`} {
		if !strings.Contains(e, want) {
			t.Errorf("entry has no %s: %s", want, e)
		}
	}
	e2, _ := vlessEntry(got[1].Link, "")
	if !strings.Contains(e2, `server: "2001:db8::1"`) || !strings.Contains(e2, `grpc-opts: {grpc-service-name: "svc"}`) {
		t.Errorf("grpc/ipv6 entry: %s", e2)
	}
}

func TestParseYAMLBlock(t *testing.T) {
	in := `proxies:
  - name: 'Test TLS'
    type: vless
    server: vpn.example.com
    port: 1234
  - {name: Second, type: vless, server: 1.1.1.1, port: 443}
`
	got, errs := parseServersInput(in)
	if len(errs) != 0 || len(got) != 2 {
		t.Fatalf("got %d, errs %v", len(got), errs)
	}
	if got[0].Name != "Test TLS" || got[1].Name != "Second" {
		t.Errorf("names: %q %q", got[0].Name, got[1].Name)
	}
	r := renameYAMLItem(got[0].YAML, "Test TLS 2")
	if !strings.Contains(r, `name: "Test TLS 2"`) || !strings.Contains(r, "  port: 1234") {
		t.Errorf("rename block: %s", r)
	}
	r2 := renameYAMLItem(got[1].YAML, "X")
	if !strings.Contains(r2, `{name: "X", type: vless`) {
		t.Errorf("rename flow: %s", r2)
	}
	info := got[0].info()
	if info.Host != "vpn.example.com" || info.Port != "1234" {
		t.Errorf("info: %+v", info)
	}
}

func TestProviderInjection(t *testing.T) {
	ad, err := adaptConfig(routerSample, defaultSettings(), testServers)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ad.Text, "proxy-providers:\n  mihomodesk:\n    type: inline\n    payload:\n      - {name: \"NL Reality\"") {
		t.Errorf("provider not appended:\n%s", ad.Text)
	}
	// конфиг уже с proxy-providers: вставляем внутрь блока
	src := routerSample + "proxy-providers:\n  sub:\n    type: http\n    url: https://example.com/sub\n"
	ad, err = adaptConfig(src, defaultSettings(), testServers[:1])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(ad.Text, "\nproxy-providers:") != 1 || !strings.Contains(ad.Text, "    url: https://example.com/sub\n  mihomodesk:\n    type: inline") {
		t.Errorf("provider not inserted:\n%s", ad.Text)
	}
	cs := configServers(routerSample)
	if len(cs) != 1 || cs[0].Name != "x" {
		t.Errorf("config servers: %+v", cs)
	}
	if g := selectGroups(routerSample); len(g) != 2 || g[0] != "A" {
		t.Errorf("groups: %v", g)
	}
}
