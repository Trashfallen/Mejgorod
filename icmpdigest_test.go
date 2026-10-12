package main

import "testing"

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

func TestICMPDigest(t *testing.T) {
	var reported []int
	d := &icmpDigest{report: func(n int) { reported = append(reported, n) }}
	shown := 0
	for i := 0; i < 50; i++ {
		if d.take() {
			shown++
		}
	}
	if shown != icmpShown {
		t.Fatalf("показано %d, ждали %d", shown, icmpShown)
	}
	d.flush()
	if len(reported) != 1 || reported[0] != 50-icmpShown {
		t.Fatalf("сводка: %v", reported)
	}
	// после сводки счёт начинается заново
	if !d.take() {
		t.Fatal("после сводки первая строка должна показываться")
	}
	d.flush()
	if len(reported) != 1 {
		t.Fatalf("пустая сводка не нужна: %v", reported)
	}
}
