package main

import "testing"

// Только печатает, что видно на этом ПК: состояние сети у всех разное.
func TestNetEnv(t *testing.T) {
	t.Logf("hasGlobalIPv6=%v", hasGlobalIPv6())
	if v := detectCorpVPN(); v != nil {
		t.Logf("corp VPN: %+v", *v)
	} else {
		t.Log("corp VPN: нет")
	}
}

func TestCorpProductOf(t *testing.T) {
	t.Cleanup(func() { setCorpExtra("") })
	setCorpExtra("")
	cases := []struct{ friendly, desc, want string }{
		{"Citrix Virtual Adapter", "Citrix Virtual Adapter", "Citrix Secure Access"},
		{"Подключение по локальной сети", "CP-TAP-Windows Adapter V9", "КриптоПро NGate"}, // NGate 1.0.20, как есть
		{"Ethernet 3", "CryptoPro NGate Virtual Adapter", "КриптоПро NGate"},
		{"NGate", "TAP-Windows Adapter V9", "КриптоПро NGate"},
		{"Подключение по локальной сети", "Адаптер КриптоПро NGate", "КриптоПро NGate"},
		{"Radmin VPN", "Famatech Radmin VPN Ethernet Adapter", ""},
		{"Беспроводная сеть", "RZ616 Wi-Fi 6E 160MHz", ""},
		{"Подключение по локальной сети* 6", "WAN Miniport (IP)", ""},
	}
	for _, c := range cases {
		if got := corpProductOf(c.friendly, c.desc); got != c.want {
			t.Errorf("corpProductOf(%q, %q) = %q, want %q", c.friendly, c.desc, got, c.want)
		}
	}
}

func TestCorpExtraFromSettings(t *testing.T) {
	t.Cleanup(func() { setCorpExtra("") })
	setCorpExtra(" Acme VPN ; tap-corp,\n")
	if got := corpProductOf("Ethernet 5", "ACME VPN Adapter"); got != corpCustomName {
		t.Fatalf("свой адаптер не узнан: %q", got)
	}
	if got := corpProductOf("TAP-Corp", "TAP-Windows Adapter V9"); got != corpCustomName {
		t.Fatalf("второй адаптер не узнан: %q", got)
	}
	setCorpExtra("")
	if got := corpProductOf("Ethernet 5", "ACME VPN Adapter"); got != "" {
		t.Fatalf("после очистки адаптер всё ещё узнаётся: %q", got)
	}
}

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"https://remote.example.ru":       "remote.example.ru",
		"https://Remote.Example.ru:8443/": "remote.example.ru",
		"remote.example.ru":               "remote.example.ru",
		"remote.example.ru:443":           "remote.example.ru",
		"  https://10.1.2.3/path ":        "10.1.2.3",
		"":                                "",
	}
	for in, want := range cases {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// Только печатает: что нашлось в настройках NGate на этом ПК.
func TestNgateGateways(t *testing.T) {
	t.Logf("ngateGateways=%v", ngateGateways())
}
