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
