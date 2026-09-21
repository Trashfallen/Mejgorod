package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Встроенные шаблоны: разбираются, без серверов помечаются NoServers,
// с серверами (и ядром из MIHOMODESK_CORE) проходят "mihomo -t".
func TestBuiltinTemplates(t *testing.T) {
	core := os.Getenv("MIHOMODESK_CORE")
	for _, tpl := range builtinTemplates {
		b, err := builtinFS.ReadFile(tpl.file)
		if err != nil {
			t.Fatal(err)
		}
		ad, err := adaptConfig(string(b), defaultSettings(), nil)
		if err != nil {
			t.Fatalf("%s: %v", tpl.ID, err)
		}
		if !ad.NoServers {
			t.Errorf("%s: в шаблоне нет серверов, ждали NoServers", tpl.ID)
		}
		if g := selectGroups(string(b)); len(g) == 0 {
			t.Errorf("%s: нет select-групп", tpl.ID)
		}
		for _, servers := range [][]Server{nil, testServers} {
			ad, err := adaptConfig(string(b), defaultSettings(), servers)
			if err != nil {
				t.Fatalf("%s: %v", tpl.ID, err)
			}
			if core == "" {
				continue
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			os.WriteFile(path, []byte(ad.Text), 0o644)
			if out, err := testConfig(context.Background(), core, dir, path); err != nil {
				t.Errorf("%s (серверов %d): mihomo -t: %s", tpl.ID, len(servers), out)
			}
		}
	}
}
