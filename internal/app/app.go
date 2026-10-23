package app

import (
	"context"
	"encoding/json"
	"errors"
	"highloader/internal/api"
	"highloader/internal/domain"
	"highloader/internal/engine"
	"highloader/internal/history"
	"highloader/internal/transport"
	"io"
	"log"
	"sync"
)

type Options struct {
	Store               *history.Store
	Logger              *log.Logger
	Version             string
	PickFolder          func() (string, bool, error)
	CPU                 engine.CPU
	CloseCPU            func() error
	NewTransport        func(int) (engine.Transport, func())
	Sink                engine.CheckpointSink
	RequestExit         func()
	ReportJS, ReportCSS []byte
}
type ownedRun struct {
	id, key  string
	runner   *engine.Runner
	released chan struct{}
}
type Application struct {
	mu                  sync.Mutex
	draft               domain.Draft
	reserved, closing   bool
	discardAcknowledged bool
	active              *ownedRun
	ops                 sync.WaitGroup
	options             Options
	ctx                 context.Context
	cancel              context.CancelFunc
	closeOnce           sync.Once
	exitOnce            sync.Once
	closeErr            error
}

func New(o Options) (*Application, error) {
	if o.Store == nil || o.PickFolder == nil {
		return nil, errors.New("application requires history and folder adapter")
	}
	if o.Logger == nil {
		o.Logger = log.New(io.Discard, "", 0)
	}
	if o.NewTransport == nil {
		o.NewTransport = func(n int) (engine.Transport, func()) { c := transport.New(n); return c, c.Close }
	}
	if o.Sink == nil {
		o.Sink = o.Store
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Application{draft: domain.NewDraft(), options: o, ctx: ctx, cancel: cancel}, nil
}

func (a *Application) reserve(revision *uint64) (domain.Draft, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.available(); err != nil {
		return domain.Draft{}, err
	}
	if revision != nil && *revision != a.draft.Revision {
		return domain.Draft{}, conflict("stale_revision", "Настройки изменились. Обновите страницу.")
	}
	a.reserved = true
	a.ops.Add(1)
	return copyValue(a.draft), nil
}
func (a *Application) release() { a.mu.Lock(); a.reserved = false; a.mu.Unlock(); a.ops.Done() }
func (a *Application) available() error {
	if a.closing {
		return conflict("closing", "Приложение завершает работу.")
	}
	if a.reserved {
		return conflict("reserved", "Другая операция ещё выполняется.")
	}
	if a.active != nil {
		select {
		case <-a.active.released:
		default:
			return conflict("active", "Дождитесь завершения теста и сохранения результатов.")
		}
		v := a.active.runner.View()
		if !v.WorkersReleased || !v.Durable || v.PersistenceError != "" {
			return conflict("unsaved", "Результаты не сохранены; новый запуск заблокирован.")
		}
	}
	return nil
}
func conflict(code, message string) error {
	return &api.Error{Code: code, Message: message, Status: 409}
}
func copyValue[T any](v T) T {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	var out T
	if e = json.Unmarshal(b, &out); e != nil {
		panic(e)
	}
	return out
}
