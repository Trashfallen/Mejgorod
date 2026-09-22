package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDeskRoundTrip(t *testing.T) {
	gsURL := geoBase + "geosite/instagram.mrs"
	name, added := providerFor(splitSample, nil, "instagram", "domain", gsURL)
	if name != "instagram@domain" || !added {
		t.Fatalf("provider = %q %v", name, added)
	}
	body, err := siteRuleBody([]string{"instagram.com"}, name, "")
	if err != nil {
		t.Fatal(err)
	}
	rules := []deskRule{
		{Label: "instagram.com", Body: body, Target: "VPN"},
		{Label: "Telegram.exe", Body: "PROCESS-NAME,Telegram.exe", Target: "DIRECT"},
	}
	provs := []deskProvider{{Name: name, Behavior: "domain", URL: gsURL}}
	text, err := writeDesk(splitSample, rules, provs)
	if err != nil {
		t.Fatal(err)
	}
	gotRules, gotProvs := readDesk(text)
	if !slices.Equal(gotRules, rules) || !slices.Equal(gotProvs, provs) {
		t.Fatalf("round trip:\n%v\n%v\n%s", gotRules, gotProvs, text)
	}
	// правила программы идут первыми
	if i, j := strings.Index(text, "# desk: instagram.com"), strings.Index(text, "RULE-SET,game@inline,VPN"); i < 0 || i > j {
		t.Errorf("desk rules are not first:\n%s", text)
	}
	items := deskItemsView(text)
	if len(items) != 2 || items[0].Route != "vpn" || items[1].Route != "direct" || items[1].Kind != "app" {
		t.Errorf("items = %+v", items)
	}
	// повторная запись не плодит разделы, пустая - убирает их без следа
	again, _ := writeDesk(text, rules, provs)
	if again != text {
		t.Errorf("second write changed text")
	}
	clean, _ := writeDesk(text, nil, nil)
	if clean != splitSample {
		t.Errorf("empty write left traces:\n%s", clean)
	}
	// тот же url в конфиге уже есть - используем его список
	if n, add := providerFor(text, provs, "instagram", "domain", gsURL); n != name || !add {
		t.Errorf("reuse desk provider = %q %v", n, add)
	}
	if core := os.Getenv("MEJGOROD_CORE"); core != "" {
		ad, err := adaptConfig(text, defaultSettings(), nil)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		f := filepath.Join(dir, "config.yaml")
		os.WriteFile(f, []byte(ad.Text), 0o644)
		if msg, err := testConfig(context.Background(), core, dir, f); err != nil {
			t.Errorf("mihomo -t: %v\n%s", err, msg)
		}
	}
}

func TestSiteRuleBody(t *testing.T) {
	cases := []struct {
		domains      []string
		gs, gi, want string
	}{
		{[]string{"a.com"}, "", "", "DOMAIN-SUFFIX,a.com"},
		{[]string{"a.com"}, "a@domain", "", "OR,((DOMAIN-SUFFIX,a.com),(RULE-SET,a@domain))"},
		{[]string{"a.com", "b.net"}, "", "a@ipcidr", "OR,((DOMAIN-SUFFIX,a.com),(DOMAIN-SUFFIX,b.net),(RULE-SET,a@ipcidr,no-resolve))"},
	}
	for _, c := range cases {
		if got, _ := siteRuleBody(c.domains, c.gs, c.gi); got != c.want {
			t.Errorf("got %q want %q", got, c.want)
		}
		// цель правила разбирается обратно
		if target, _ := parseRule(c.want + ",VPN"); target != "VPN" {
			t.Errorf("%s: target %q", c.want, target)
		}
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"instagram.com":                   "instagram.com",
		"https://www.Instagram.com/p/xyz": "instagram.com",
		"*.cdninstagram.com":              "cdninstagram.com",
		"госуслуги.рф":                    "госуслуги.рф",
	} {
		if got, err := normalizeDomain(in); got != want || err != nil {
			t.Errorf("normalizeDomain(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := normalizeDomain("просто текст"); err == nil {
		t.Error("garbage accepted")
	}
	if got := geoCandidates("x.com"); got[0] != "twitter" {
		t.Errorf("x.com -> %v", got)
	}
	for in, want := range map[string]string{
		`C:\Program Files\Telegram\Telegram.exe`: "Telegram.exe",
		"discord":                                "discord.exe",
		"Yandex Messenger.exe":                   "Yandex Messenger.exe",
	} {
		if got, err := normalizeProcess(in); got != want || err != nil {
			t.Errorf("normalizeProcess(%q) = %q, %v", in, got, err)
		}
	}
}

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		tag, cur string
		want     bool
	}{
		{"v1.1.0", "1.0.0", true},
		{"v1.0.0", "1.0.0", false},
		{"1.0.10", "1.0.9", true},
		{"v1.2", "1.1.9", true},
		{"v1.0.0-beta", "1.0.0", false},
		{"latest", "1.0.0", false},
	} {
		if got := newerVersion(c.tag, c.cur); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v", c.tag, c.cur, got)
		}
	}
}
