package main

// Управление процессом mihomo: запуск, остановка, логи, трафик.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

const (
	stStopped  = "stopped"
	stStarting = "starting"
	stRunning  = "running"
	stStopping = "stopping"
	stError    = "error"
)

type Traffic struct {
	Up        int64 `json:"up"`
	Down      int64 `json:"down"`
	UpTotal   int64 `json:"upTotal"`
	DownTotal int64 `json:"downTotal"`
	Conns     int   `json:"conns"`
}

type CoreState struct {
	Status    string  `json:"status"`
	Stage     string  `json:"stage"`
	Error     string  `json:"error"`
	StartedAt int64   `json:"startedAt"`
	Port      int     `json:"port"`
	Secret    string  `json:"secret"`
	Traffic   Traffic `json:"traffic"`
}

type Core struct {
	app *App
	job windows.Handle

	mu        sync.Mutex
	status    string
	stage     string
	errMsg    string
	cmd       *exec.Cmd
	done      chan struct{}
	cancel    context.CancelFunc
	stopping  bool
	startedAt time.Time
	port      int
	secret    string
	traffic   Traffic
	onChange  []func(status string)
	// abortReason - почему программа сама остановила ядро (петля и т.п.)
	abortReason string
}

func newCore(app *App) *Core {
	c := &Core{app: app, status: stStopped}
	if job, err := newKillOnCloseJob(); err == nil {
		c.job = job
	}
	return c
}

func (c *Core) State() CoreState {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := CoreState{Status: c.status, Stage: c.stage, Error: c.errMsg, Port: c.port, Secret: c.secret, Traffic: c.traffic}
	if c.status == stRunning {
		st.StartedAt = c.startedAt.UnixMilli()
	}
	return st
}

func (c *Core) Status() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *Core) OnChange(fn func(string)) {
	c.mu.Lock()
	c.onChange = append(c.onChange, fn)
	c.mu.Unlock()
}

// setLocked меняет статус и уведомляет подписчиков (трей). Вызывать под c.mu.
func (c *Core) setLocked(status string) {
	c.status = status
	for _, fn := range c.onChange {
		go fn(status)
	}
}

func (c *Core) setStage(s string) {
	c.mu.Lock()
	c.stage = s
	c.mu.Unlock()
	c.app.logs.Add("app", "info", s)
}

// Connect запускает подключение в фоне; ход виден через State().
func (c *Core) Connect() {
	c.mu.Lock()
	switch c.status {
	case stStarting, stRunning, stStopping:
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel, c.stopping, c.errMsg, c.stage, c.abortReason = cancel, false, "", "Подготовка", ""
	c.setLocked(stStarting)
	c.mu.Unlock()
	go c.run(ctx)
}

func (c *Core) run(ctx context.Context) {
	err := c.launch(ctx)
	if err == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stage = ""
	if ctx.Err() != nil || c.stopping {
		c.setLocked(stStopped)
		return
	}
	c.errMsg = err.Error()
	c.setLocked(stError)
	c.app.logs.Add("app", "error", "Не удалось подключиться: "+err.Error())
}

func (c *Core) launch(ctx context.Context) error {
	a := c.app
	if !isElevated() && !devNoTun {
		return errors.New("нет прав администратора, без них TUN не создать. Перезапустите программу и подтвердите запрос Windows")
	}
	if !fileExists(a.paths.CoreExe) {
		c.setStage("Скачиваю ядро mihomo")
		if err := a.installCore(ctx); err != nil {
			return err
		}
	}
	src, err := os.ReadFile(a.configPath())
	if errors.Is(err, os.ErrNotExist) || (err == nil && strings.TrimSpace(string(src)) == "") {
		return errors.New("нет конфига: выберите шаблон или вставьте свой на вкладке «Конфиг»")
	}
	if err != nil {
		return err
	}
	set := a.settings.Get()
	servers := a.servers.Snapshot().Servers
	c.setStage("Смотрю сеть")
	env := a.buildEnv(string(src), servers)
	ad, err := adaptConfigEnv(string(src), set, servers, env)
	if err != nil {
		return fmt.Errorf("конфиг: %w", err)
	}
	if ad.NoServers {
		return errors.New("нет ни одного сервера: добавьте ссылку vless:// на вкладке «Серверы»")
	}
	if !portFree(ad.Port) {
		return fmt.Errorf("порт %d занят другой программой. Смените порт панели в настройках", ad.Port)
	}
	if err := os.WriteFile(a.paths.RunConfig, []byte(ad.Text), 0o644); err != nil {
		return err
	}

	switch {
	case env.Split && env.CorpVPN != nil:
		a.logs.Add("app", "info", fmt.Sprintf("Подключён %s: включаю совместимость, в TUN идёт только нужное (%d подсетей)",
			env.CorpVPN.Name, len(env.RouteAddress)))
	case env.Split:
		a.logs.Add("app", "info", fmt.Sprintf("В TUN идёт только нужное (%d подсетей)", len(env.RouteAddress)))
	case env.CorpVPN != nil:
		a.logs.Add("app", "warning", "Подключён "+env.CorpVPN.Name+", а маршруты TUN - «весь трафик». "+
			"Так он и VPN мешают друг другу: верните в настройках режим «Авто»")
	}
	if len(env.MissingSets) > 0 {
		a.logs.Add("app", "warning", "Списки ещё не скачаны, в TUN пока не попадут: "+strings.Join(env.MissingSets, ", ")+
			". После скачивания программа переподключится")
	}

	c.setStage("Проверяю конфиг")
	if out, err := testConfig(ctx, a.paths.CoreExe, a.paths.Home, a.paths.RunConfig); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ошибка в конфиге: %s", out)
	}

	c.setStage("Запускаю ядро")
	logSeq := a.logs.Seq()
	cmd := exec.Command(a.paths.CoreExe, "-d", a.paths.Home, "-f", a.paths.RunConfig)
	cmd.Dir = a.paths.Home
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow | createNewProcessGroup}
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ядро не запустилось: %w", err)
	}
	if c.job != 0 {
		_ = assignToJob(c.job, cmd.Process.Pid)
	}
	go c.pumpLogs(pr)
	done := make(chan struct{})
	c.mu.Lock()
	c.cmd, c.done, c.port, c.secret = cmd, done, ad.Port, ad.Secret
	stopping := c.stopping
	c.mu.Unlock()
	go func() {
		err := cmd.Wait()
		pw.Close()
		close(done)
		c.exited(cmd, err)
	}()
	if stopping {
		c.terminate(cmd, done)
	}

	c.setStage("Жду ответа ядра (первый запуск качает списки правил)")
	deadline := time.Now().Add(2 * time.Minute)
	for !c.apiReady(ad.Port, ad.Secret) {
		select {
		case <-done:
			c.mu.Lock()
			reason := c.abortReason
			c.mu.Unlock()
			if reason != "" {
				return errors.New(reason)
			}
			msg := a.logs.CoreErrorsSince(logSeq, 3)
			if msg == "" {
				msg = "подробности на вкладке «Логи»"
			}
			return fmt.Errorf("ядро остановилось при запуске: %s", msg)
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			c.terminate(cmd, done)
			return errors.New("ядро не ответило за 2 минуты")
		}
	}

	c.mu.Lock()
	if c.stopping || c.cmd != cmd {
		c.mu.Unlock()
		return context.Canceled
	}
	c.stage, c.startedAt = "", time.Now()
	c.setLocked(stRunning)
	c.mu.Unlock()
	a.logs.Add("app", "info", "Подключено")
	go c.watchTraffic(ad.Port, ad.Secret, done)
	if !devNoTun && set.TunRoute == "auto" {
		go c.watchCorpVPN(env.CorpVPN != nil, done)
	}
	go func() {
		a.applyActiveServer()
		a.applyBoardChoices()
	}()
	go func() {
		c.refreshProviders(ad.Port, ad.Secret, done)
		// нужные для маршрутов списки скачались только сейчас - применяем
		if len(env.MissingSets) > 0 && c.Status() == stRunning {
			if e := a.buildEnv(string(src), servers); len(e.MissingSets) < len(env.MissingSets) {
				a.logs.Add("app", "info", "Списки для маршрутов скачаны, переподключаюсь")
				c.Restart()
			}
		}
	}()
	return nil
}

func (c *Core) exited(cmd *exec.Cmd, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd != cmd {
		return
	}
	c.cmd, c.traffic = nil, Traffic{}
	if c.stopping {
		c.stage = ""
		c.setLocked(stStopped)
		c.app.logs.Add("app", "info", "Отключено")
		return
	}
	if c.status == stRunning {
		c.errMsg = "ядро неожиданно остановилось"
		if err != nil {
			c.errMsg += ": " + err.Error()
		}
		if c.abortReason != "" {
			c.errMsg = c.abortReason // уже в логе
		} else {
			c.app.logs.Add("app", "error", c.errMsg)
		}
		c.setLocked(stError)
	}
	// при запуске ошибку оформит run()
}

// Disconnect останавливает ядро и ждёт его завершения.
func (c *Core) Disconnect() {
	c.mu.Lock()
	if c.status == stStopped || c.status == stError {
		c.errMsg = ""
		c.setLocked(stStopped)
		c.mu.Unlock()
		return
	}
	c.stopping = true
	c.setLocked(stStopping)
	if c.cancel != nil {
		c.cancel()
	}
	cmd, done := c.cmd, c.done
	c.mu.Unlock()
	if cmd != nil {
		c.terminate(cmd, done)
	}
	c.waitIdle(15 * time.Second)
}

func (c *Core) Restart() {
	c.Disconnect()
	c.Connect()
}

// waitIdle ждёт, пока статус станет stopped или error.
func (c *Core) waitIdle(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s := c.Status(); s == stStopped || s == stError {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (c *Core) terminate(cmd *exec.Cmd, done chan struct{}) {
	if cmd.Process == nil {
		return
	}
	// сначала штатно, чтобы ядро само сняло TUN и маршруты
	if err := sendCtrlBreak(cmd.Process.Pid); err == nil {
		select {
		case <-done:
			return
		case <-time.After(5 * time.Second):
		}
	}
	_ = cmd.Process.Kill()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

const (
	logLinesPerSec = 100 // больше строк ядра в секунду в лог не пишем: окно начинает тормозить
	loopLimit      = 300 // столько «reject loopback» за loopWindow - петля, отключаемся
	loopWindow     = 5 * time.Second
)

func (c *Core) pumpLogs(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var (
		sec       = time.Now().Truncate(time.Second)
		inSec     int
		dropped   int
		loopStart time.Time
		loops     int
	)
	for sc.Scan() {
		line := sc.Text()
		now := time.Now()

		// Петля: ядро ловит собственные соединения. Сотни в секунду вешают ПК.
		if strings.Contains(line, "reject loopback") {
			if now.Sub(loopStart) > loopWindow {
				loopStart, loops = now, 0
			}
			if loops++; loops == loopLimit {
				reason := "Петля в сети: ядро ловит собственные соединения через TUN. " +
					"VPN отключён, чтобы не вешать компьютер. Подробности на вкладке «Логи»"
				if detectCorpVPN() != nil {
					reason = "VPN отключён: мешает Citrix Secure Access (рабочий VPN). " +
						"Он перехватывает соединения ядра и отправляет их обратно в TUN. " +
						"Отключите Citrix на время работы Mejgorod"
				}
				go c.abort(reason)
			}
		}

		if s := now.Truncate(time.Second); !s.Equal(sec) {
			if dropped > 0 {
				c.app.logs.Add("app", "warning", fmt.Sprintf("Пропущено строк лога ядра: %d (больше %d в секунду)", dropped, logLinesPerSec))
			}
			sec, inSec, dropped = s, 0, 0
		}
		if inSec++; inSec > logLinesPerSec {
			dropped++
			continue
		}
		level, msg := parseCoreLine(line)
		if level == "raw" {
			level = "info"
		}
		c.app.logs.Add("core", level, msg)
	}
	_, _ = io.Copy(io.Discard, r)
}

// abort останавливает ядро с ошибкой, которую увидит пользователь.
func (c *Core) abort(reason string) {
	c.mu.Lock()
	cmd, done := c.cmd, c.done
	if cmd == nil || c.stopping || c.abortReason != "" {
		c.mu.Unlock()
		return
	}
	c.abortReason = reason
	c.mu.Unlock()
	c.app.logs.Add("app", "error", reason)
	c.terminate(cmd, done)
}

// watchCorpVPN переподключает VPN, когда Citrix подключили или отключили:
// режим маршрутов «Авто» зависит от него.
func (c *Core) watchCorpVPN(was bool, done chan struct{}) {
	for {
		select {
		case <-done:
			return
		case <-time.After(5 * time.Second):
		}
		if now := corpVPNUp(); now != was {
			if c.Status() != stRunning {
				return
			}
			msg := "Citrix Secure Access отключён: возвращаю весь трафик в TUN, переподключаюсь"
			if now {
				msg = "Подключён Citrix Secure Access: переподключаюсь в режиме совместимости"
			}
			c.app.logs.Add("app", "info", msg)
			go c.Restart()
			return
		}
	}
}

// refreshProviders перекачивает пустые списки правил: при первом запуске
// они могли не скачаться, пока сеть поднималась.
func (c *Core) refreshProviders(port int, secret string, done chan struct{}) {
	select {
	case <-done:
		return
	case <-time.After(3 * time.Second):
	}
	base := fmt.Sprintf("http://127.0.0.1:%d/providers/rules", port)
	call := func(method, u string, timeout time.Duration) (*http.Response, error) {
		req, _ := http.NewRequest(method, u, nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		cl := http.Client{Timeout: timeout}
		return cl.Do(req)
	}
	resp, err := call("GET", base, 5*time.Second)
	if err != nil {
		return
	}
	var data struct {
		Providers map[string]struct {
			VehicleType string `json:"vehicleType"`
			RuleCount   int    `json:"ruleCount"`
		} `json:"providers"`
	}
	err = json.NewDecoder(resp.Body).Decode(&data)
	resp.Body.Close()
	if err != nil {
		return
	}
	var empty []string
	for name, p := range data.Providers {
		if p.RuleCount == 0 && strings.EqualFold(p.VehicleType, "HTTP") {
			empty = append(empty, name)
		}
	}
	if len(empty) == 0 {
		return
	}
	sort.Strings(empty)
	c.app.logs.Add("app", "info", "Докачиваю списки правил: "+strings.Join(empty, ", "))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, name := range empty {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if r, err := call("PUT", base+"/"+url.PathEscape(name), time.Minute); err == nil {
				r.Body.Close()
			}
		}()
	}
	wg.Wait()
}

func (c *Core) apiReady(port int, secret string) bool {
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/version", port), nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	cl := http.Client{Timeout: time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// watchTraffic читает поток /traffic (скорость) и раз в 3 секунды /connections.
func (c *Core) watchTraffic(port int, secret string, done chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-done; cancel() }()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	get := func(path string) (*http.Response, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", base+path, nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		return http.DefaultClient.Do(req)
	}

	go func() {
		for ctx.Err() == nil {
			if !c.app.uiActive() {
				// окна нет: счётчик соединений никто не видит, ядро не дёргаем
				select {
				case <-ctx.Done():
				case <-time.After(3 * time.Second):
				}
				continue
			}
			if resp, err := get("/connections"); err == nil {
				var v struct {
					Up    int64             `json:"uploadTotal"`
					Down  int64             `json:"downloadTotal"`
					Conns []json.RawMessage `json:"connections"`
				}
				if json.NewDecoder(resp.Body).Decode(&v) == nil {
					c.mu.Lock()
					c.traffic.Conns, c.traffic.UpTotal, c.traffic.DownTotal = len(v.Conns), v.Up, v.Down
					c.mu.Unlock()
				}
				resp.Body.Close()
			}
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
			}
		}
	}()

	for ctx.Err() == nil {
		resp, err := get("/traffic")
		if err == nil {
			dec := json.NewDecoder(resp.Body)
			for {
				var t struct {
					Up   int64 `json:"up"`
					Down int64 `json:"down"`
				}
				if dec.Decode(&t) != nil {
					break
				}
				c.mu.Lock()
				c.traffic.Up, c.traffic.Down = t.Up, t.Down
				c.mu.Unlock()
			}
			resp.Body.Close()
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

func portFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// testConfig - "mihomo -t": проверка конфига без запуска.
func testConfig(ctx context.Context, core, home, file string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, core, "-t", "-d", home, "-f", file)
	cmd.SysProcAttr = hiddenProc()
	out, err := cmd.CombinedOutput()
	var msgs []string
	ok := false
	for _, l := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if strings.Contains(l, "test is successful") {
			ok = true
			continue
		}
		if strings.Contains(l, "test failed") {
			continue // "configuration file <временный путь> test failed" - шум
		}
		// информационные строки ядра ("Start initial configuration...") не нужны
		level, msg := parseCoreLine(l)
		switch level {
		case "info", "debug":
			continue
		}
		msgs = append(msgs, msg)
	}
	text := strings.Join(msgs, "\n")
	if err == nil && ok {
		return text, nil
	}
	if text == "" && err != nil {
		text = err.Error()
	}
	return text, errors.New("config test failed")
}
