package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

const subVlessLink = "vless://11111111-2222-3333-4444-555555555555@203.0.113.10:443?security=reality&sni=example.com&pbk=AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHHIIIIJJJJKKK&sid=0123&type=tcp&flow=xtls-rprx-vision#Sub%20One"

func TestParseSubscriptionLinkList(t *testing.T) {
	plain := subVlessLink + "\nvless://11111111-2222-3333-4444-555555555555@203.0.113.11:443?type=tcp#Sub%20Two\n"
	for _, body := range []string{
		plain, // открытым текстом
		base64.StdEncoding.EncodeToString([]byte(plain)),    // как чаще всего отдают подписки
		base64.RawURLEncoding.EncodeToString([]byte(plain)), // urlsafe без паддинга
	} {
		servers, errs := parseSubscription(body)
		if len(errs) != 0 {
			t.Errorf("body %q: unexpected errs %v", short(body), errs)
		}
		if len(servers) != 2 || servers[0].Name != "Sub One" || servers[1].Name != "Sub Two" {
			t.Fatalf("body %q: got %+v", short(body), servers)
		}
		if servers[0].Link != subVlessLink {
			t.Errorf("link not preserved: %q", servers[0].Link)
		}
	}
}

func TestParseSubscriptionYAML(t *testing.T) {
	// подписка может отдать целый конфиг mihomo, а не только proxies:
	full := `port: 7890
proxies:
  - {name: Alpha, type: vless, server: 203.0.113.20, port: 443, uuid: 11111111-2222-3333-4444-555555555555}
  - {name: Beta, type: trojan, server: 203.0.113.21, port: 443, password: x}
proxy-groups:
  - {name: PROXY, type: select, proxies: [Alpha, Beta]}
rules:
  - MATCH,PROXY
`
	for _, body := range []string{full, base64.StdEncoding.EncodeToString([]byte(full))} {
		servers, errs := parseSubscription(body)
		if len(errs) != 0 {
			t.Errorf("unexpected errs %v", errs)
		}
		if len(servers) != 2 || servers[0].Name != "Alpha" || servers[1].Name != "Beta" {
			t.Fatalf("got %+v", servers)
		}
	}
}

func TestParseSubscriptionEmpty(t *testing.T) {
	if _, errs := parseSubscription(""); len(errs) == 0 {
		t.Error("expected error on empty body")
	}
	if _, errs := parseSubscription(base64.StdEncoding.EncodeToString([]byte("просто текст без ссылок"))); len(errs) == 0 {
		t.Error("expected error on garbage")
	}
}

func TestSubItemsRoundTrip(t *testing.T) {
	link := Server{Name: "Sub One", Link: subVlessLink}
	yaml := Server{Name: "Alpha", YAML: "- {name: Alpha, type: vless, server: 203.0.113.20, port: 443, uuid: 11111111-2222-3333-4444-555555555555}"}
	items := toSubItems([]Server{link, yaml})
	if items[0].Kind != "link" || items[0].Val != subVlessLink {
		t.Errorf("link item = %+v", items[0])
	}
	if items[0].Type != "vless" || items[0].Host != "203.0.113.10" {
		t.Errorf("link meta = %+v", items[0])
	}
	if items[1].Kind != "yaml" || !strings.Contains(items[1].Val, "Alpha") {
		t.Errorf("yaml item = %+v", items[1])
	}
	if items[1].Type != "vless" || items[1].Host != "203.0.113.20" {
		t.Errorf("yaml meta = %+v", items[1])
	}
}

func TestFindProxiesBlock(t *testing.T) {
	src := "a: 1\nproxies:\n  - {name: X, type: vless}\nrules:\n  - MATCH,DIRECT\n"
	block, ok := findProxiesBlock(src)
	if !ok || !strings.HasPrefix(block, "proxies:") || strings.Contains(block, "rules:") {
		t.Fatalf("block=%q ok=%v", block, ok)
	}
	if _, ok := findProxiesBlock("a: 1\nrules:\n  - MATCH,DIRECT\n"); ok {
		t.Error("found proxies where there is none")
	}
}
