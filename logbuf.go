package main

import (
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type LogLine struct {
	Seq   int64  `json:"seq"`
	Time  string `json:"time"`
	Level string `json:"level"` // debug | info | warning | error
	Src   string `json:"src"`   // core | app
	Msg   string `json:"msg"`
}

// LogBuf - кольцевой буфер логов ядра и программы для вкладки «Логи».
type LogBuf struct {
	mu    sync.Mutex
	lines []LogLine
	seq   int64
	max   int
}

func newLogBuf(max int) *LogBuf { return &LogBuf{max: max} }

func (b *LogBuf) Add(src, level, msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	b.lines = append(b.lines, LogLine{b.seq, time.Now().Format("15:04:05"), level, src, msg})
	if len(b.lines) > b.max {
		b.lines = append([]LogLine(nil), b.lines[len(b.lines)-b.max:]...)
	}
	if src == "app" {
		log.Printf("[%s] %s", level, msg)
	}
}

func (b *LogBuf) After(seq int64) ([]LogLine, int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []LogLine{}
	for _, l := range b.lines {
		if l.Seq > seq {
			out = append(out, l)
		}
	}
	return out, b.seq
}

func (b *LogBuf) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = nil
}

// CoreErrorsSince - последние ошибки ядра после seq, для текста ошибки подключения.
func (b *LogBuf) CoreErrorsSince(seq int64, n int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var msgs []string
	for _, l := range b.lines {
		if l.Seq > seq && l.Src == "core" && (l.Level == "error" || l.Level == "fatal" || l.Level == "warning") {
			msgs = append(msgs, l.Msg)
		}
	}
	if len(msgs) > n {
		msgs = msgs[len(msgs)-n:]
	}
	return strings.Join(msgs, "; ")
}

func (b *LogBuf) Seq() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq
}

// Формат логов mihomo: time="..." level=info msg="..."
var coreLogRe = regexp.MustCompile(`^time="[^"]*" level=(\w+) msg=("(?:[^"\\]|\\.)*"|\S*)`)

func parseCoreLine(s string) (level, msg string) {
	m := coreLogRe.FindStringSubmatch(s)
	if m == nil {
		// не строка логгера: паника, вывод -t и т.п.
		return "raw", s
	}
	level, msg = m[1], m[2]
	if strings.HasPrefix(msg, `"`) {
		if u, err := strconv.Unquote(msg); err == nil {
			msg = u
		}
	}
	if level == "warn" {
		level = "warning"
	}
	return level, msg
}
