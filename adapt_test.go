package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const routerSample = `log-level: warning
allow-lan: true
redir-port: 5000
tproxy-port: 5001
routing-mark: 255
find-process-mode: off
external-controller: 0.0.0.0:9090
secret: abc
external-ui: zashboard
proxy-groups:
  - name: A
    type: select
# комментарий внутри блока
  - name: B
    type: select
rules:
- MATCH,DIRECT
proxies:
  - name: 'x'
    type: vless
dns:
  enable: true
  listen: 0.0.0.0:53
  nameserver:
    - https://1.1.1.1/dns-query
`

func TestAdaptRouterConfig(t *testing.T) {
	ad, err := adaptConfig(routerSample, defaultSettings(), nil)
	if err != nil {
		t.Fatal(err)
	}
	in := splitLines(routerSample)
	out := splitLines(ad.Text)
	// номера исходных строк сохраняются
	for i, l := range in {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if out[i] != l && out[i] != "# [MihomoDesk] "+l {
			t.Fatalf("line %d changed: %q -> %q", i+1, l, out[i])
		}
	}
	for _, want := range []string{
		"# [MihomoDesk] redir-port: 5000",
		"# [MihomoDesk] tproxy-port: 5001",
		"# [MihomoDesk] routing-mark: 255",
		"# [MihomoDesk] allow-lan: true",
		"# [MihomoDesk] external-controller: 0.0.0.0:9090",
		"# [MihomoDesk]   listen: 0.0.0.0:53",
		"allow-lan: false",
		"external-controller: 127.0.0.1:9090",
		"find-process-mode: strict",
		"  stack: mixed",
		"  - name: B", // блок proxy-groups не порван комментарием
	} {
		if !strings.Contains(ad.Text, want) {
			t.Errorf("no %q in output", want)
		}
	}
	if ad.Secret != "abc" {
		t.Errorf("secret = %q", ad.Secret)
	}
	if strings.Contains(ad.Text, "\nsecret: "+defaultSettings().Secret) {
		t.Error("secret added twice")
	}
	p, g := configStats(routerSample)
	if p != 1 || g != 2 {
		t.Errorf("stats = %d proxies, %d groups", p, g)
	}
}

func TestAdaptIPv6(t *testing.T) {
	defer func(f func() bool) { detectIPv6 = f }(detectIPv6)
	src := "ipv6: true\nproxies:\n  - {name: x, type: direct}\ndns:\n  enable: true\n  ipv6: true\n  nameserver: [1.1.1.1]\n"

	detectIPv6 = func() bool { return false }
	ad, err := adaptConfig(src, defaultSettings(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := splitLines(ad.Text)
	if out[0] != "# [MihomoDesk] ipv6: true" || !strings.HasPrefix(out[5], "  ipv6: false # [MihomoDesk]") {
		t.Errorf("ipv6 lines not replaced in place:\n%s", ad.Text)
	}
	if !strings.Contains(ad.Text, "\nipv6: false\n") || !strings.Contains(ad.Text, "  device: "+tunDevice) {
		t.Errorf("no ipv6/device in PC block:\n%s", ad.Text)
	}

	detectIPv6 = func() bool { return true }
	ad, _ = adaptConfig(src, defaultSettings(), nil)
	if strings.Contains(ad.Text, "ipv6: false") {
		t.Errorf("ipv6 disabled on a PC with IPv6:\n%s", ad.Text)
	}
}

func TestAdaptRejectsGarbage(t *testing.T) {
	if _, err := adaptConfig("просто текст", defaultSettings(), nil); err == nil {
		t.Error("expected error")
	}
	if ad, err := adaptConfig("log-level: info\n", defaultSettings(), nil); err != nil || !ad.NoServers {
		t.Errorf("expected NoServers, got %v %v", ad, err)
	}
}

// MIHOMODESK_CONFIG=путь к реальному конфигу, MIHOMODESK_CORE=путь к mihomo.exe
func TestAdaptRealConfig(t *testing.T) {
	src := os.Getenv("MIHOMODESK_CONFIG")
	if src == "" {
		t.Skip("MIHOMODESK_CONFIG not set")
	}
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	ad, err := adaptConfig(string(b), defaultSettings(), testServers)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range ad.Changes {
		t.Log("change:", c)
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "config.yaml")
	os.WriteFile(out, []byte(ad.Text), 0o644)
	if core := os.Getenv("MIHOMODESK_CORE"); core != "" {
		msg, err := testConfig(context.Background(), core, dir, out)
		t.Logf("mihomo -t: err=%v\n%s", err, msg)
		if err != nil {
			t.Fail()
		}
	}
	if dst := os.Getenv("MIHOMODESK_DUMP"); dst != "" {
		os.WriteFile(dst, []byte(ad.Text), 0o644)
	}
}

// MIHOMODESK_FETCH=папка: проверка скачивания ядра с GitHub.
func TestFetchCore(t *testing.T) {
	dir := os.Getenv("MIHOMODESK_FETCH")
	if dir == "" {
		t.Skip("MIHOMODESK_FETCH not set")
	}
	f := &Fetcher{}
	dst := filepath.Join(dir, "mihomo.exe")
	tmp, err := f.Download(context.Background(), dst, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		t.Fatal(err)
	}
	v, err := coreVersion(dst)
	t.Logf("core %s err=%v", v, err)
}
