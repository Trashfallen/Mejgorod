// MihomoDesk - VPN-клиент для Windows поверх ядра mihomo.
// Принимает тот же конфиг, что XKeen на роутере, сам адаптирует его под ПК (TUN)
// и управляется одной кнопкой: окно с интерфейсом + значок в трее.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime/debug"

	"golang.org/x/sys/windows"
)

const (
	appName    = "MihomoDesk"
	appVersion = "1.0.0"
)

func main() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic: %v %s", r, debug.Stack())
			fatalBox(fmt.Sprintf("Внутренняя ошибка: %v", r))
		}
	}()
	autostart := flag.Bool("autostart", false, "запуск из автозагрузки: без окна")
	noElevate := flag.Bool("no-elevate", false, "не запрашивать права администратора (отладка)")
	flag.Parse()
	// отладка интерфейса без прав администратора: ядро стартует без TUN
	devNoTun = os.Getenv("MIHOMODESK_DEV_NOTUN") == "1"

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

	app, err := newApp(paths)
	if err != nil {
		fatalBox(err.Error())
		return
	}
	if err := app.startServer(); err != nil {
		fatalBox("Не удалось запустить интерфейс:\n" + err.Error())
		return
	}
	log.Printf("%s %s started, ui port %d, elevated=%v", appName, appVersion, app.port, isElevated())

	if !*autostart {
		go app.openWindow()
	}
	if app.settings.Get().ConnectOnLaunch {
		app.core.Connect()
	}

	runTray(app) // блокирует до выхода
	app.shutdown()
}
