package main

// Ядро пишет каждый ping без ответа как error: «receive ICMP echo reply: ...
// i/o timeout». Какая-нибудь программа (проверка серверов, игровой лаунчер)
// пингует сотни адресов подряд, большинство не отвечает на ICMP, и журнал
// забивается ошибками, на вкладке «Логи» горит красная точка, а настоящие
// ошибки уезжают за край буфера. Оставляем первые строки как debug, остальные
// сворачиваем в одну сводку.

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	icmpShown  = 3
	icmpWindow = 30 * time.Second
)

func isICMPTimeout(msg string) bool {
	return strings.HasPrefix(msg, "receive ICMP echo reply") && strings.HasSuffix(msg, "i/o timeout")
}

type icmpDigest struct {
	mu     sync.Mutex
	shown  int
	hidden int
	timer  *time.Timer
	report func(hidden int)
}

// take: true - строку показать (как debug), false - она ушла в счётчик.
func (d *icmpDigest) take() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer == nil {
		d.timer = time.AfterFunc(icmpWindow, d.flush)
	}
	if d.shown < icmpShown {
		d.shown++
		return true
	}
	d.hidden++
	return false
}

func (d *icmpDigest) flush() {
	d.mu.Lock()
	hidden := d.hidden
	d.shown, d.hidden, d.timer = 0, 0, nil
	d.mu.Unlock()
	if hidden > 0 && d.report != nil {
		d.report(hidden)
	}
}

func icmpSummary(hidden int) string {
	return fmt.Sprintf("Пинги без ответа: ещё %d за %d с (адреса не отвечают на ICMP, это не сбой VPN)", hidden, int(icmpWindow.Seconds()))
}
