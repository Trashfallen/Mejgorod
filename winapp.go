package main

// Окно программы на панели задач.
//
// Окно - это Edge в режиме --app, и Windows по умолчанию прячет его под значок
// Edge. Назначаем окну свой AppUserModelID и команду перезапуска: у него своя
// кнопка с иконкой MihomoDesk, а закреплённый значок запускает MihomoDesk.exe.
// Заодно следим, чтобы окно было одно: повторное открытие показывает уже
// открытое, окна от прошлого запуска (с устаревшим токеном) закрываются.

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const appUserModelID = "Trashfallen.MihomoDesk"

var (
	modShell32                      = windows.NewLazySystemDLL("shell32.dll")
	procSHGetPropertyStoreForWindow = modShell32.NewProc("SHGetPropertyStoreForWindow")
	modUser32                       = windows.NewLazySystemDLL("user32.dll")
	procGetWindowTextW              = modUser32.NewProc("GetWindowTextW")
	procShowWindow                  = modUser32.NewProc("ShowWindow")
	procSetForegroundWindow         = modUser32.NewProc("SetForegroundWindow")
	procIsIconic                    = modUser32.NewProc("IsIconic")
	procIsWindow                    = modUser32.NewProc("IsWindow")
	procPostMessageW                = modUser32.NewProc("PostMessageW")
	procAllowSetForegroundWindow    = modUser32.NewProc("AllowSetForegroundWindow")
)

type propertyKey struct {
	fmtid windows.GUID
	pid   uint32
}

// PROPVARIANT: 8 байт заголовка + 16 байт значения на x64.
type propVariant struct {
	vt  uint16
	_   [3]uint16
	val uintptr
	_   uintptr
}

type iPropertyStoreVtbl struct {
	QueryInterface, AddRef, Release, GetCount, GetAt, GetValue, SetValue, Commit uintptr
}

type iPropertyStore struct{ vtbl *iPropertyStoreVtbl }

var (
	iidPropertyStore = windows.GUID{Data1: 0x886D8EEB, Data2: 0x8CF2, Data3: 0x4446, Data4: [8]byte{0x8D, 0x02, 0xCD, 0xBA, 0x1D, 0xBD, 0xCF, 0x99}}
	fmtAppUserModel  = windows.GUID{Data1: 0x9F4C2855, Data2: 0x9F79, Data3: 0x4B39, Data4: [8]byte{0xA8, 0xD0, 0xE1, 0xD4, 0x2D, 0xE1, 0xD5, 0xF3}}
)

const (
	pkRelaunchCommand = 2
	pkRelaunchIcon    = 3
	pkRelaunchName    = 4
	pkAppID           = 5
	vtLPWSTR          = 31
)

// tagWindow даёт окну свой значок на панели задач и команду для закрепления.
func tagWindow(hwnd windows.HWND, exe string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err == nil {
		defer windows.CoUninitialize()
	}
	var ps *iPropertyStore
	if r, _, _ := procSHGetPropertyStoreForWindow.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&iidPropertyStore)), uintptr(unsafe.Pointer(&ps))); r != 0 || ps == nil {
		return fmt.Errorf("SHGetPropertyStoreForWindow: 0x%x", r)
	}
	defer syscall.SyscallN(ps.vtbl.Release, uintptr(unsafe.Pointer(ps)))
	set := func(pid uint32, value string) error {
		s, _ := windows.UTF16PtrFromString(value)
		pv := propVariant{vt: vtLPWSTR, val: uintptr(unsafe.Pointer(s))}
		key := propertyKey{fmtAppUserModel, pid}
		r, _, _ := syscall.SyscallN(ps.vtbl.SetValue, uintptr(unsafe.Pointer(ps)), uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&pv)))
		runtime.KeepAlive(s)
		if r != 0 {
			return fmt.Errorf("SetValue %d: 0x%x", pid, r)
		}
		return nil
	}
	// команду перезапуска задают до ID: иначе закреплённый значок запустит Edge
	for _, p := range []struct {
		pid uint32
		val string
	}{
		{pkRelaunchCommand, `"` + exe + `"`},
		{pkRelaunchName, appName},
		{pkRelaunchIcon, exe + ",0"},
		{pkAppID, appUserModelID},
	} {
		if err := set(p.pid, p.val); err != nil {
			return err
		}
	}
	syscall.SyscallN(ps.vtbl.Commit, uintptr(unsafe.Pointer(ps)))
	return nil
}

// Поиск окон: EnumWindows с одним callback на процесс (их число ограничено).
var (
	enumMu   sync.Mutex
	enumOut  []windows.HWND
	enumProc = syscall.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
		if !windows.IsWindowVisible(hwnd) {
			return 1
		}
		var cls [64]uint16
		if n, _ := windows.GetClassName(hwnd, &cls[0], int32(len(cls))); windows.UTF16ToString(cls[:n]) != "Chrome_WidgetWin_1" {
			return 1
		}
		var title [128]uint16
		n, _, _ := procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&title[0])), uintptr(len(title)))
		if windows.UTF16ToString(title[:n]) != appName {
			return 1
		}
		var pid uint32
		if _, err := windows.GetWindowThreadProcessId(hwnd, &pid); err != nil {
			return 1
		}
		if strings.EqualFold(filepath.Base(processPath(pid)), "msedge.exe") {
			enumOut = append(enumOut, hwnd)
		}
		return 1
	})
)

// appWindows - открытые окна программы (Edge с заголовком MihomoDesk).
func appWindows() []windows.HWND {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumOut = nil
	_ = windows.EnumWindows(enumProc, nil)
	return append([]windows.HWND(nil), enumOut...)
}

func focusWindow(hwnd windows.HWND) {
	if r, _, _ := procIsIconic.Call(uintptr(hwnd)); r != 0 {
		procShowWindow.Call(uintptr(hwnd), 9) // SW_RESTORE
	}
	procSetForegroundWindow.Call(uintptr(hwnd))
}

func closeWindow(hwnd windows.HWND) {
	procPostMessageW.Call(uintptr(hwnd), 0x0010, 0, 0) // WM_CLOSE
}

func isWindow(hwnd windows.HWND) bool {
	r, _, _ := procIsWindow.Call(uintptr(hwnd))
	return r != 0
}

// allowForeground - второй запуск программы разрешает первой вывести окно вперёд.
func allowForeground() {
	procAllowSetForegroundWindow.Call(0xFFFFFFFF) // ASFW_ANY
}

// tagNewWindows ждёт окно, открытое только что, и даёт ему свой значок.
// Edge может переписать свойства, пока окно создаётся, поэтому метим
// несколько раз в первые секунды.
func (a *App) tagNewWindows() {
	exe := a.paths.Exe
	tagged := map[windows.HWND]int{}
	for i := 0; i < 60; i++ {
		for _, w := range appWindows() {
			if tagged[w] < 3 && (tagged[w] == 0 || i%5 == 0) {
				if err := tagWindow(w, exe); err == nil {
					tagged[w]++
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
}
