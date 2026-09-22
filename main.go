// MihomoDesk - VPN-клиент для Windows поверх ядра mihomo.
// Принимает тот же конфиг, что XKeen на роутере, сам адаптирует его под ПК (TUN)
// и управляется одной кнопкой: окно с интерфейсом + значок в трее.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime/debug"
	"time"

	"golang.org/x/sys/windows"
)

const appName = "MihomoDesk"

// appVersion - var, чтобы проверять обновление сборкой с другой версией (-X main.appVersion).
var appVersion = "1.2.0"

func main() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic: %v %s", r, debug.Stack())
			fatalBox(fmt.Sprintf("Внутренняя ошибка: %v", r))
		}
	}()
	autostart := flag.Bool("autostart", false, "запуск из автозагрузки: без окна")
	noElevate := flag.Bool("no-elevate", false, "не запрашивать права администратора (отладка)")
	afterUpdate := flag.Int("after-update", 0, "pid старой копии: дождаться её выхода после обновления")
	token := flag.String("token", "", "токен окна (перезапуск после обновления)")
	noWindow := flag.Bool("no-window", false, "не открывать окно (оно уже открыто)")
	connect := flag.Bool("connect", false, "подключить VPN после запуска")
	flag.Parse()
	if *afterUpdate > 0 {
		waitForExit(*afterUpdate, 30*time.Second)
	}
	// отладка интерфейса без прав администратора: ядро стартует без TUN
	devNoTun = os.Getenv("MIHOMODESK_DEV_NOTUN") == "1"
	noElevateRun = *noElevate
	if u := os.Getenv("MIHOMODESK_UPDATE_API"); u != "" {
		updateAPI = u // проверка обновления на своём сервере
	}

	paths, err := newPaths()
	if err != nil {
		fatalBox("Не удалось определить папку программы:\n" + err.Error())
		return
	}

	// Уже запущены - просим открыть окно. Проверяем до UAC,
	// чтобы повторный запуск не спрашивал права администратора.
	mutex, exists := acquireInstanceMutex()
	if exists {
		if !*autostart {
			if err := showExisting(paths); err != nil {
				fatalBox(appName + " уже запущен, но не отвечает:\n" + err.Error())
			}
		}
		return
	}

	if !*noElevate && !isElevated() {
		windows.CloseHandle(mutex)
		if err := relaunchElevated(os.Args[1:]); err != nil && !errors.Is(err, windows.ERROR_CANCELLED) {
			fatalBox("Не удалось запустить с правами администратора:\n" + err.Error())
		}
		return
	}
	defer windows.CloseHandle(mutex)

	if err := paths.ensure(); err != nil {
		fatalBox("Нет доступа к папке программы:\n" + err.Error())
		return
	}
	setupLog(paths)

	go cleanupOldExe()
	app, err := newApp(paths, *token)
	if err != nil {
		fatalBox(err.Error())
		return
	}
	app.adoptWindows = *token != ""
	if err := app.startServer(); err != nil {
		fatalBox("Не удалось запустить интерфейс:\n" + err.Error())
		return
	}
	log.Printf("%s %s started, ui port %d, elevated=%v", appName, appVersion, app.port, isElevated())

	if !*autostart && !*noWindow {
		go app.openWindow()
	}
	if *connect || app.settings.Get().ConnectOnLaunch {
		app.core.Connect()
	}
	if !app.settings.Get().NoUpdateCheck {
		go func() {
			time.Sleep(3 * time.Second)
			_ = app.updater.Check(context.Background())
			if u := app.updater.State(); u.Available {
				app.logs.Add("app", "info", "Доступна новая версия "+u.Version)
			}
		}()
	}

	runTray(app) // блокирует до выхода
	app.shutdown()
}
