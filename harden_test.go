package main

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestHardenSDDL(t *testing.T) {
	for _, s := range []string{sddlLocked, sddlData} {
		sd, err := windows.SecurityDescriptorFromString(s)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if _, _, err := sd.DACL(); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

func TestDedicatedDir(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"Mejgorod.exe", "Mejgorod.exe.old", "Mejgorod.lnk", "README.md", "Как пользоваться.txt"} {
		os.WriteFile(filepath.Join(dir, n), nil, 0o644)
	}
	os.Mkdir(filepath.Join(dir, "data"), 0o755)
	if err := dedicatedDir(dir, "data"); err != nil {
		t.Fatalf("своя папка: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o644)
	if dedicatedDir(dir, "data") == nil {
		t.Fatal("папка с посторонним файлом считается своей")
	}
}
