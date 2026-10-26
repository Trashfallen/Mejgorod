package main

// Программа работает с правами администратора и запускает Mejgorod.exe
// (автозапуск через Планировщик) и data\core\mihomo.exe. Если их может
// подменить обычный процесс пользователя, подмена получит права администратора
// без UAC. Поэтому при запуске с правами администратора закрываем папку
// программы от записи всем, кроме администраторов и SYSTEM.
//
// Закрываем только отдельную папку, где нет ничего, кроме файлов программы:
// рабочий стол или Загрузки трогать нельзя. data остается доступной на запись
// (конфиги правят и вручную), data\core снова закрыта - там исполняемое ядро.
// OW ограничивает владельца папки: иначе он смог бы вернуть себе права.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

const (
	// SYSTEM и администраторы - полный доступ, пользователи и владелец - чтение и запуск
	sddlLocked = "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1200a9;;;BU)(A;OICI;0x1200a9;;;OW)"
	// то же, но пользователи и владелец могут менять файлы (без удаления вложенных папок)
	sddlData = "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1301bf;;;BU)(A;OICI;0x1301bf;;;OW)"
)

// installHardened - exe и ядро нельзя подменить без прав администратора.
var installHardened bool

func hardenInstall(p Paths) error {
	if !isElevated() {
		return fmt.Errorf("нет прав администратора")
	}
	dir := filepath.Dir(p.Exe)
	// отладочная копия в dist\ рядом с исходниками: ее пересобирает обычный пользователь
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "go.mod")); err == nil {
		return fmt.Errorf("копия из папки исходников")
	}
	if err := dedicatedDir(dir, filepath.Base(p.Data)); err != nil {
		return err
	}
	for _, s := range []struct{ path, sddl string }{
		{dir, sddlLocked}, {p.Data, sddlData}, {p.CoreDir, sddlLocked},
	} {
		if err := setDACL(s.path, s.sddl); err != nil {
			return fmt.Errorf("%s: %w", s.path, err)
		}
	}
	installHardened = true
	return nil
}

// dedicatedDir: в папке только Mejgorod* и data (и описания из архива релиза, см. package\).
func dedicatedDir(dir, data string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		if n == strings.ToLower(data) || strings.HasPrefix(n, "mejgorod") ||
			strings.HasPrefix(n, "readme") || n == "license" || n == "как пользоваться.txt" {
			continue
		}
		return fmt.Errorf("в папке %s есть посторонний %s: права на нее не меняю", dir, e.Name())
	}
	return nil
}

func setDACL(path, sddl string) error {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
