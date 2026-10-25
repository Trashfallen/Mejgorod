package main

// Ядро повторяет одно и то же десятками строк, и журнал забивается:
//   - каждый ping без ответа - error «receive ICMP echo reply: ... i/o timeout»
//     (игровой лаунчер или проверка серверов пингует сотни адресов подряд);
//   - при запуске по строке «[Provider] ... not updated for a long time» на
//     каждый список правил, если программа не запускалась дольше суток;
//   - браузер или фоновая программа раз в минуту стучится на один и тот же
//     недоступный адрес, и ядро каждый раз пишет «dial ... error».
// На вкладке «Логи» горит красная точка, а настоящие ошибки уезжают за край
// буфера. Первые строки оставляем, остальные сворачиваем в одну сводку.

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	icmpShown  = 3
	icmpWindow = 30 * time.Second

	providerWindow = 5 * time.Second

	dialShown  = 1
	dialWindow = 15 * time.Minute
)

func isICMPTimeout(msg string) bool {
	return strings.HasPrefix(msg, "receive ICMP echo reply") && strings.HasSuffix(msg, "i/o timeout")
}

func isProviderRefresh(msg string) bool {
	return strings.HasPrefix(msg, "[Provider] ") && strings.HasSuffix(msg, "not updated for a long time, force refresh")
}

// [TCP] dial RU сайты (match OR/(...)) 198.18.0.1:50943(browser.exe) --> host:443 error: ...
var dialErrRe = regexp.MustCompile(`^\[(TCP|UDP)\] dial (.+?) \(match .*\) \S+?(?:\(([^)]*)\))? --> (\S+) error: `)

// dialErrKey - «куда и кто» для ошибки соединения, "" - это не она.
func dialErrKey(msg string) string {
	m := dialErrRe.FindStringSubmatch(msg)
	if m == nil {
		return ""
	}
	key := m[4]
	if m[3] != "" {
		key += " (" + m[3] + ")"
	}
	return key + " через " + m[2]
}

// joinMultiline склеивает многострочное сообщение ядра в одну строку.
// Так ядро пишет соединение, где не ответил ни один адрес хоста: первая строка
// с правилом, дальше по строке «dial tcp адрес: i/o timeout» на каждый адрес.
func joinMultiline(msg string) string {
	parts := strings.Split(strings.TrimRight(msg, "\n"), "\n")
	if len(parts) == 1 {
		return msg
	}
	tail := func(s string) string { return s[strings.LastIndex(s, ": ")+1:] }
	same := true
	for _, p := range parts[1:] {
		if tail(p) != tail(parts[0]) {
			same = false
			break
		}
	}
	if same {
		return fmt.Sprintf("%s (еще адресов с той же ошибкой: %d)", parts[0], len(parts)-1)
	}
	return strings.Join(parts, "; ")
}

// logDigest пропускает первые shown строк с одним ключом, остальные считает
// и через window после первой отдает количество в report.
type logDigest struct {
	shown  int
	window time.Duration
	report func(key string, hidden int)

	mu sync.Mutex
	m  map[string]*digestEntry
}

type digestEntry struct{ shown, hidden int }

// take: true - строку показать, false - она ушла в счетчик.
func (d *logDigest) take(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.m == nil {
		d.m = map[string]*digestEntry{}
	}
	e := d.m[key]
	if e == nil {
		e = &digestEntry{}
		d.m[key] = e
		time.AfterFunc(d.window, func() { d.flushEntry(key, e) })
	}
	if e.shown < d.shown {
		e.shown++
		return true
	}
	e.hidden++
	return false
}

func (d *logDigest) flush(key string) {
	d.mu.Lock()
	e := d.m[key]
	d.mu.Unlock()
	d.flushEntry(key, e)
}

func (d *logDigest) flushEntry(key string, e *digestEntry) {
	d.mu.Lock()
	if e == nil || d.m[key] != e {
		d.mu.Unlock()
		return
	}
	delete(d.m, key)
	hidden := e.hidden
	d.mu.Unlock()
	if hidden > 0 && d.report != nil {
		d.report(key, hidden)
	}
}

func icmpSummary(hidden int) string {
	return fmt.Sprintf("Пинги без ответа: еще %d за %d с (адреса не отвечают на ICMP, это не сбой VPN)", hidden, int(icmpWindow.Seconds()))
}

func providerSummary(n int) string {
	return fmt.Sprintf("Списки правил давно не обновлялись, качаю заново: %d шт.", n)
}

func dialSummary(key string, hidden int) string {
	return fmt.Sprintf("Не открывается %s: еще %d раз за %d мин", key, hidden, int(dialWindow.Minutes()))
}
