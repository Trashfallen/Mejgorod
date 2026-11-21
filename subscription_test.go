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

func TestMergeSubKeepsIDsAndOrder(t *testing.T) {
	all := []Server{
		{ID: "own1", Name: "Свой"},
		{ID: "a1", Name: "Мой NL", Orig: "NL", Sub: "s1", Link: "vless://old-nl"},
		{ID: "a2", Name: "DE", Orig: "DE", Sub: "s1", Link: "vless://de"},
		{ID: "own2", Name: "Ещё свой"},
	}
	fresh := []Server{{Name: "NL", Link: "vless://new-nl"}, {Name: "FI", Link: "vless://fi"}, {Name: "US", Link: "vless://us"}}
	next, names := mergeSub(all, "s1", fresh, []string{"US"}, map[string]bool{})
	if len(next) != 4 || next[0].ID != "own1" || next[3].ID != "own2" {
		t.Fatalf("порядок сломан: %+v", next)
	}
	if next[1].ID != "a1" || next[1].Name != "Мой NL" || next[1].Link != "vless://new-nl" {
		t.Fatalf("NL должен сохранить ID и имя, но взять новую ссылку: %+v", next[1])
	}
	if next[2].Name != "FI" || next[2].Sub != "s1" || next[2].Orig != "FI" || next[2].ID == "" {
		t.Fatalf("новый сервер FI: %+v", next[2])
	}
	if len(names) != 2 {
		t.Fatalf("US в skip, DE пропал из подписки: %v", names)
	}
	again, _ := mergeSub(next, "s1", fresh, []string{"US"}, map[string]bool{})
	if !sameServers(again, next) {
		t.Fatal("повтор с той же подпиской должен давать тот же список")
	}
}

func TestProfileTitle(t *testing.T) {
	if got := profileTitle("base64:0JzQvtC5IFZQTg=="); got != "Мой VPN" {
		t.Fatalf("base64: %q", got)
	}
	if got := profileTitle(" Plain "); got != "Plain" {
		t.Fatalf("plain: %q", got)
	}
}
