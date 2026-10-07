package main

// Подключение при запуске (из автозагрузки или после обновления). При входе
// в Windows сеть поднимается позже программы, и первая попытка часто падает,
// поэтому ждём сеть и повторяем, пока пользователь сам ничего не нажал.

import (
	"fmt"
	"time"
)

type autoConnector struct {
	attempt   func() string              // одна попытка: Connect и ожидание итога, возвращает статус
	permanent func() bool                // последняя ошибка не лечится повтором
	pause     func(d time.Duration) bool // false - пользователь вмешался, повторять не надо
	logf      func(format string, args ...any)
	delays    []time.Duration
}

func (ac autoConnector) run() {
	for i := 0; ; i++ {
		if ac.attempt() != stError || ac.permanent() || i >= len(ac.delays) {
			return
		}
		ac.logf("Подключиться не удалось, повторю через %d с (попытка %d из %d)", int(ac.delays[i].Seconds()), i+1, len(ac.delays))
		if !ac.pause(ac.delays[i]) {
			return
		}
	}
}

func waitFor(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Second)
	}
	return true
}

// autoConnect подключает VPN в фоне: ждёт сеть и повторяет неудачные попытки.
func (a *App) autoConnect() {
	go func() {
		if !networkReady() {
			a.logs.Add("app", "info", "Жду сеть перед подключением")
			if !waitFor(networkReady, 2*time.Minute) {
				a.logs.Add("app", "warning", "Сети нет уже 2 минуты, подключаюсь всё равно")
			}
		}
		autoConnector{
			attempt: func() string {
				a.core.Connect()
				for {
					if st := a.core.Status(); st != stStarting && st != stStopping {
						return st
					}
					time.Sleep(500 * time.Millisecond)
				}
			},
			permanent: a.core.ErrPermanent,
			pause: func(d time.Duration) bool {
				for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(500 * time.Millisecond) {
					if a.core.Status() != stError {
						return false
					}
				}
				return true
			},
			logf: func(format string, args ...any) {
				a.logs.Add("app", "info", fmt.Sprintf(format, args...))
			},
			delays: []time.Duration{10 * time.Second, 20 * time.Second, 30 * time.Second, 60 * time.Second, 60 * time.Second, 60 * time.Second, 60 * time.Second, 60 * time.Second, 60 * time.Second},
		}.run()
	}()
}
