package main

import (
	"sync/atomic"
	"testing"
	"time"
)

func withTaskbar(t *testing.T, fn func() bool) {
	t.Helper()
	old := taskbarReadyFunc
	taskbarReadyFunc = fn
	t.Cleanup(func() { taskbarReadyFunc = old })
}

func TestWaitForTaskbarWaitsUntilReady(t *testing.T) {
	var calls atomic.Int32
	withTaskbar(t, func() bool { return calls.Add(1) >= 3 })
	a := &App{logs: newLogBuf(10)}
	if !waitForTaskbar(a, make(chan struct{}), 10*time.Second) {
		t.Fatal("ожидание прервано без причины")
	}
	if calls.Load() < 3 {
		t.Fatalf("не дождались панели: проверок %d", calls.Load())
	}
}

func TestWaitForTaskbarCancel(t *testing.T) {
	withTaskbar(t, func() bool { return false })
	a := &App{logs: newLogBuf(10)}
	cancel := make(chan struct{})
	go func() {
		time.Sleep(300 * time.Millisecond)
		close(cancel)
	}()
	if waitForTaskbar(a, cancel, 10*time.Second) {
		t.Fatal("ожидание должно прерываться выходом")
	}
}

func TestWaitForTaskbarTimeoutTriesAnyway(t *testing.T) {
	withTaskbar(t, func() bool { return false })
	a := &App{logs: newLogBuf(10)}
	if !waitForTaskbar(a, make(chan struct{}), 600*time.Millisecond) {
		t.Fatal("по таймауту нужно пробовать всё равно")
	}
}

// Только печатает: на ПК с работающим проводником оболочка принимает значки.
func TestShellAcceptsIcons(t *testing.T) {
	t.Logf("taskbarReady=%v shellAcceptsIcons=%v", taskbarReady(), shellAcceptsIcons())
}
