package main

import (
	"time"

	"fyne.io/systray"

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

// runTray крутит цикл сообщений значка в трее (главный поток) до выхода.
func runTray(a *App) {
	a.quitFn = systray.Quit
	exited := make(chan struct{})
	systray.Run(func() {
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
