package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestThemeSetting(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"":                  "auto",
		`{"theme":"dark"}`:  "dark",
		`{"theme":"light"}`: "light",
		`{"theme":"auto"}`:  "auto",
		`{"theme":"pink"}`:  "auto",
		`{}`:                "auto",
	}
	for content, want := range cases {
		p := filepath.Join(dir, "settings.json")
		_ = os.Remove(p)
		if content != "" {
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if got := loadSettings(p).Get().Theme; got != want {
			t.Errorf("settings %q: theme = %q, want %q", content, got, want)
		}
	}
}
