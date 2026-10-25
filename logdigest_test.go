package main

import (
	"strings"
	"testing"
)

func TestIsICMPTimeout(t *testing.T) {
	yes := "receive ICMP echo reply: read ip4 192.0.2.10->198.51.100.7: i/o timeout"
	if !isICMPTimeout(yes) {
		t.Fatal("ping без ответа не узнан")
	}
	for _, no := range []string{
		"[TCP] dial DIRECT (match Match/) 198.18.0.1:5000(app.exe) --> example.com:443 error: dial tcp 192.0.2.1:443: i/o timeout",
		"receive ICMP echo reply: some other error",
		"",
	} {
		if isICMPTimeout(no) {
			t.Errorf("лишнее совпадение: %q", no)
		}
	}
}

func TestIsProviderRefresh(t *testing.T) {
	if !isProviderRefresh("[Provider] ru@domain not updated for a long time, force refresh") {
		t.Fatal("обновление списка не узнано")
	}
	if isProviderRefresh("[Provider] ru@domain pull error: timeout") {
		t.Fatal("ошибку обновления сворачивать нельзя")
	}
}

func TestDialErrKey(t *testing.T) {
	cases := map[string]string{
		"[TCP] dial RU сайты (match OR/((RuleSet,ru@inline) || (RuleSet,ru@domain))) 198.18.0.1:50943(browser.exe) --> quic.example.net:443 error: dial tcp 192.0.2.23:443: i/o timeout": "quic.example.net:443 (browser.exe) через RU сайты",
		"[TCP] dial DIRECT (match RuleSet/game@inline) 198.18.0.1:57062 --> gw.example.ru:443 error: dial tcp 192.0.2.186:443: i/o timeout":                                              "gw.example.ru:443 через DIRECT",
		"[TCP] dial Telegram (match OR/((RuleSet,telegram@domain))) 198.18.0.1:57099(Telegram.exe) --> 192.0.2.100:443 error: vps.example:1234 connect error: i/o timeout":               "192.0.2.100:443 (Telegram.exe) через Telegram",
		"receive ICMP echo reply: read ip4 192.0.2.10->198.51.100.7: i/o timeout":                                                                                                        "",
		"[TUN] default interface changed by monitor, => Wi-Fi":                                                                                                                           "",
	}
	for msg, want := range cases {
		if got := dialErrKey(msg); got != want {
			t.Errorf("%q:\n got %q\nwant %q", msg, got, want)
		}
	}
}

func TestJoinMultiline(t *testing.T) {
	head := "[TCP] dial DIRECT (match Match/) 198.18.0.1:1 --> gw.example.ru:443 error: dial tcp 192.0.2.1:443: i/o timeout"
	msg := head + "\ndial tcp 192.0.2.2:443: i/o timeout\ndial tcp 192.0.2.3:443: i/o timeout\n"
	if got, want := joinMultiline(msg), head+" (еще адресов с той же ошибкой: 2)"; got != want {
		t.Fatalf("got %q", got)
	}
	mixed := "a: i/o timeout\nb: connection refused"
	if got := joinMultiline(mixed); got != "a: i/o timeout; b: connection refused" {
		t.Fatalf("got %q", got)
	}
	if got := joinMultiline("one line"); got != "one line" {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(joinMultiline(msg), "\n") {
		t.Fatal("перенос строки остался")
	}
}

func TestLogDigest(t *testing.T) {
	var reported []int
	d := &logDigest{shown: icmpShown, window: icmpWindow, report: func(_ string, n int) { reported = append(reported, n) }}
	shown := 0
	for i := 0; i < 50; i++ {
		if d.take("icmp") {
			shown++
		}
	}
	if shown != icmpShown {
		t.Fatalf("показано %d, ждали %d", shown, icmpShown)
	}
	d.flush("icmp")
	if len(reported) != 1 || reported[0] != 50-icmpShown {
		t.Fatalf("сводка: %v", reported)
	}
	// после сводки счет начинается заново
	if !d.take("icmp") {
		t.Fatal("после сводки первая строка должна показываться")
	}
	d.flush("icmp")
	if len(reported) != 1 {
		t.Fatalf("пустая сводка не нужна: %v", reported)
	}
}

func TestLogDigestKeys(t *testing.T) {
	got := map[string]int{}
	d := &logDigest{shown: dialShown, window: dialWindow, report: func(k string, n int) { got[k] = n }}
	for i := 0; i < 5; i++ {
		d.take("a")
		d.take("b")
	}
	if d.take("c") != true {
		t.Fatal("новый ключ должен показываться")
	}
	d.flush("a")
	d.flush("b")
	d.flush("c")
	if got["a"] != 4 || got["b"] != 4 || len(got) != 2 {
		t.Fatalf("сводки: %v", got)
	}
}
