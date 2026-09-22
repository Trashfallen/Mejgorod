// genicon пишет winres/icon.png для go-winres (значок exe).
package main

import (
	"os"
	"path/filepath"

	"mihomodesk/internal/icon"
)

func main() {
	out := "winres"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(out, "icon.png"), icon.PNG(256, icon.Off), 0o644); err != nil {
		panic(err)
	}
}
