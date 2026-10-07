package main

import (
	"os"
	"sync"
	"time"
	"unsafe"

	"fyne.io/systray"
	"golang.org/x/sys/windows"

	"mihomodesk/internal/icon"
)

var (
	icoOn   = icon.ICO(icon.On, 16, 20, 24, 32, 48)
	icoOff  = icon.ICO(icon.Off, 16, 20, 24, 32, 48)
	icoErr  = icon.ICO(icon.Err, 16, 20, 24, 32, 48)
	trayTip = map[string]string{
		stStopped:  "отключено",
		stStarting: "подключение...",
		stRunning:  "подключено",
		stStopping: "отключение...",
		stError:    "ошибка",
	}
)

var (
	procFindWindowW               = modUser32.NewProc("FindWindowW")
	procFindWindowExW             = modUser32.NewProc("FindWindowExW")
	procRegisterWindowMessageW    = modUser32.NewProc("RegisterWindowMessageW")
	procChangeWindowMessageFilter = modUser32.NewProc("ChangeWindowMessageFilter")
)

// taskbarReadyFunc - подмена в тестах.
var taskbarReadyFunc = taskbarReady

// taskbarReady - есть ли панель задач с областью уведомлений. Без неё
// Shell_NotifyIcon не добавит значок, а systray после такой неудачи молча
// остаётся без значка и меню (onReady не вызывается). При запуске из
// автозагрузки проводник нередко стартует позже программы.
func taskbarReady() bool {
	cls, _ := windows.UTF16PtrFromString("Shell_TrayWnd")
	tray, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(cls)), 0)
	if tray == 0 {
		return false
	}
	notify, _ := windows.UTF16PtrFromString("TrayNotifyWnd")
	n, _, _ := procFindWindowExW.Call(tray, 0, uintptr(unsafe.Pointer(notify)), 0)
	return n != 0
}

// allowTaskbarCreated: процесс с правами администратора не получает от
// проводника (обычные права) сообщение TaskbarCreated, и после перезапуска
// проводника значок не вернулся бы. Разрешаем это сообщение.
func allowTaskbarCreated() {
	name, _ := windows.UTF16PtrFromString("TaskbarCreated")
	msg, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(name)))
	if msg != 0 {
		const msgfltAdd = 1
		procChangeWindowMessageFilter.Call(msg, msgfltAdd)
	}
}

// waitForTaskbar ждёт панель задач до timeout. false - во время ожидания
// попросили выйти. По таймауту возвращает true: пробуем всё равно.
func waitForTaskbar(a *App, cancel <-chan struct{}, timeout time.Duration) bool {
	if taskbarReadyFunc() {
		return true
	}
	a.logs.Add("app", "info", "Жду панель задач, чтобы поставить значок в трей")
	deadline := time.Now().Add(timeout)
	for !taskbarReadyFunc() {
		if time.Now().After(deadline) {
			a.logs.Add("app", "warning", "Панель задач не появилась за "+timeout.String()+", ставлю значок как есть")
			return true
		}
		select {
		case <-cancel:
			return false
		case <-time.After(500 * time.Millisecond):
		}
	}
	// сразу после появления панель ещё может не принимать значки
	select {
	case <-cancel:
		return false
	case <-time.After(time.Second):
	}
	return true
}

// runTray крутит цикл сообщений значка в трее (главный поток) до выхода.
func runTray(a *App) {
	var (
		mu                       sync.Mutex
		running, ready, quitting bool
		cancelOnce               sync.Once
		cancel                   = make(chan struct{})
		exited                   = make(chan struct{})
		readyCh                  = make(chan struct{})
	)
	a.quitFn = func() {
		mu.Lock()
		quitting = true
		r, run := ready, running
		mu.Unlock()
		cancelOnce.Do(func() { close(cancel) })
		switch {
		case r:
			systray.Quit()
		case run:
			// значок так и не создался: Run не выйдет сам, завершаем руками
			go func() {
				time.Sleep(3 * time.Second)
				mu.Lock()
				r := ready
				mu.Unlock()
				if !r {
					a.shutdown()
					os.Exit(0)
				}
			}()
		}
	}
	allowTaskbarCreated()
	if !waitForTaskbar(a, cancel, 3*time.Minute) {
		return
	}
	mu.Lock()
	running = true
	mu.Unlock()
	go func() {
		select {
		case <-readyCh:
		case <-exited:
		case <-time.After(30 * time.Second):
			a.logs.Add("app", "error", "Значок в трее не создался: панель задач его не приняла. Окно открывается повторным запуском программы")
		}
	}()
	systray.Run(func() {
		mu.Lock()
		ready = true
		q := quitting
		mu.Unlock()
		close(readyCh)
		if q {
			systray.Quit()
			return
		}
		a.logs.Add("app", "info", "Значок в трее создан")
		systray.SetTitle(appName)
		mOpen := systray.AddMenuItem("Открыть "+appName, "")
		mToggle := systray.AddMenuItem("Подключить", "")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Выход", "Отключить VPN и закрыть программу")
		systray.SetOnTapped(func() { go a.openWindow() })

		apply := func(status string) {
			switch status {
			case stRunning:
				systray.SetIcon(icoOn)
			case stError:
				systray.SetIcon(icoErr)
			default:
				systray.SetIcon(icoOff)
			}
			systray.SetTooltip(appName + ": " + trayTip[status])
			switch status {
			case stRunning, stStarting:
				mToggle.SetTitle("Отключить")
				mToggle.Enable()
			case stStopping:
				mToggle.SetTitle("Отключение...")
				mToggle.Disable()
			default:
				mToggle.SetTitle("Подключить")
				mToggle.Enable()
			}
		}
		last := ""
		refresh := func() {
			select {
			case <-exited: // значка уже нет
				return
			default:
			}
			if s := a.core.Status(); s != last {
				last = s
				apply(s)
			}
		}
		refresh()
		changes := make(chan string, 8)
		a.core.OnChange(func(s string) {
			select {
			case changes <- s:
			default:
			}
		})

		go func() {
			for {
				select {
				case <-exited:
					return
				case <-mOpen.ClickedCh:
					go a.openWindow()
				case <-mToggle.ClickedCh:
					go a.toggle()
				case <-mQuit.ClickedCh:
					a.quit()
					return
				case <-changes:
					// берём актуальный статус: события могли прийти пачкой
					refresh()
				case <-time.After(10 * time.Second):
					refresh()
				}
			}
		}()
	}, func() { close(exited) })
}

func (a *App) toggle() {
	switch a.core.Status() {
	case stRunning, stStarting:
		a.core.Disconnect()
	case stStopped, stError:
		a.core.Connect()
	}
}
