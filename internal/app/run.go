package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"highloader/internal/api"
	"highloader/internal/engine"
	"highloader/internal/history"
	"highloader/internal/metrics"
	"highloader/internal/request"
	"time"
)

func (a *Application) Start(ctx context.Context, rev uint64, key string) (api.Active, error) {
	if key == "" || len(key) > 200 {
		return api.Active{}, &api.Error{Code: "invalid_key", Message: "Нужен ключ запуска длиной до 200 символов.", Status: 422}
	}
	old, err := a.options.Store.FindStart(ctx, key)
	if err == nil {
		v, e := a.View(ctx, old.RunID)
		return activeView(old.RunID, v), e
	}
	if !errors.Is(err, history.ErrNotFound) {
		return api.Active{}, err
	}
	d, err := a.reserve(&rev)
	if err != nil {
		return api.Active{}, err
	}
	defer a.release()
	idBytes := make([]byte, 16)
	if _, err = rand.Read(idBytes); err != nil {
		return api.Active{}, err
	}
	idBytes[6] = (idBytes[6] & 15) | 64
	idBytes[8] = (idBytes[8] & 63) | 128
	id := fmt.Sprintf("%x-%x-%x-%x-%x", idBytes[:4], idBytes[4:6], idBytes[6:8], idBytes[8:10], idBytes[10:])
	snap, err := request.Freeze(d, id, a.options.Version, time.Now().UTC())
	if err != nil {
		return api.Active{}, &api.Error{Code: "preflight", Message: "Настройки не прошли проверку. Откройте проверку перед запуском.", Status: 422}
	}
	tr, closeTransport := a.options.NewTransport(int(d.Capacity))
	runner, err := engine.New(snap, engine.Options{Transport: tr, Sink: a.options.Sink, CPU: a.options.CPU})
	if err != nil {
		closeTransport()
		return api.Active{}, err
	}
	// The reservation owns this bounded transaction even if the response connection disappears.
	startCtx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	result, err := a.options.Store.Start(startCtx, snap, key)
	if err != nil {
		closeTransport()
		return api.Active{}, err
	}
	if result.Existing {
		closeTransport()
		v, e := a.View(context.Background(), result.RunID)
		return activeView(result.RunID, v), e
	}
	run := &ownedRun{id: id, key: key, runner: runner, released: make(chan struct{})}
	runCtx, runCancel := context.WithCancel(a.ctx)
	a.mu.Lock()
	a.active = run
	closing := a.closing
	a.mu.Unlock()
	if closing {
		// Exit may acknowledge while the reserved start transaction is committing.
		// Persist a terminal record without admitting any traffic from that start.
		runCancel()
	}
	go func() {
		defer runCancel()
		_ = runner.Run(runCtx)
		closeTransport()
		close(run.released)
		a.options.Logger.Print("Runner and transport released; durability remains explicit")
	}()
	a.options.Logger.Print("Start snapshot committed before runner dispatch")
	return activeView(id, runner.View()), nil
}

func activeView(id string, v engine.View) api.Active {
	if v.Points == nil {
		v.Points = []metrics.Point{}
	}
	return api.Active{RunID: &id, View: &v}
}
func (a *Application) Active() api.Active {
	a.mu.Lock()
	r := a.active
	closing := a.closing
	a.mu.Unlock()
	if r == nil {
		return api.Active{Closing: closing}
	}
	v := activeView(r.id, r.runner.View())
	v.Closing = closing
	return v
}
func (a *Application) View(ctx context.Context, id string) (engine.View, error) {
	a.mu.Lock()
	r := a.active
	a.mu.Unlock()
	if r != nil && r.id == id {
		return *activeView(id, r.runner.View()).View, nil
	}
	record, err := a.options.Store.Get(ctx, id)
	if err != nil {
		return engine.View{}, err
	}
	// Reconnect/idempotent historical reads must not inherit the current editable draft's profile.
	v := engine.View{RunID: id, Mode: record.Snapshot.Draft.Profile.Mode, RetryAccounting: record.Snapshot.Draft.Profile.RetryAccounting, State: record.State, Reason: record.Reason, Durable: true, WorkersReleased: true, WriterReleased: true, DurableSequence: record.LatestSequence, Points: []metrics.Point{}}
	if record.DurableCut != nil {
		v.DurableCut = *record.DurableCut
	}
	if cp := record.Checkpoint; cp != nil {
		v.Revision = cp.Sequence
		v.Desired = cp.Desired
		v.SchedulerMisses = cp.SchedulerMisses
		v.CapacityMisses = cp.CapacityMisses
		for _, d := range cp.Aggregates {
			if d.Kind == "overall" {
				v.Aggregate = d.Aggregate
			}
		}
	}
	return v, nil
}
func (a *Application) Stop(ctx context.Context, id string, force bool) (engine.View, error) {
	a.mu.Lock()
	r := a.active
	a.mu.Unlock()
	if r != nil && r.id == id {
		v, err := r.runner.Stop(ctx, force)
		return *activeView(id, v).View, err
	}
	return a.View(ctx, id)
}

func (a *Application) Close(ctx context.Context) error {
	a.closeOnce.Do(func() {
		a.mu.Lock()
		a.closing = true
		a.mu.Unlock()
		// No new Add can occur after closing; long native operations finish before resource closure.
		a.ops.Wait()
		a.mu.Lock()
		r := a.active
		a.mu.Unlock()
		if r != nil {
			_, _ = r.runner.Stop(ctx, false)
			select {
			case <-r.released:
			case <-ctx.Done():
				a.cancel()
				<-r.released
			}
			v := r.runner.View()
			a.mu.Lock()
			discarded := a.discardAcknowledged
			a.mu.Unlock()
			if (!v.Durable || v.PersistenceError != "") && !discarded {
				a.closeErr = errors.New("Результаты последнего запуска сохранены не полностью.")
			}
		}
		a.cancel()
		if a.options.CloseCPU != nil {
			a.closeErr = errors.Join(a.closeErr, a.options.CloseCPU())
		}
		a.closeErr = errors.Join(a.closeErr, a.options.Store.Close())
		a.options.Logger.Print("Application workers joined and resources closed")
	})
	return a.closeErr
}

func (a *Application) Exit() {
	a.exitOnce.Do(func() {
		a.mu.Lock()
		a.closing = true
		a.mu.Unlock()
		go func() {
			a.ops.Wait()
			a.mu.Lock()
			r := a.active
			a.mu.Unlock()
			if r != nil {
				_, _ = r.runner.Stop(a.ctx, false)
				<-r.released
				v := r.runner.View()
				if !v.Durable || v.PersistenceError != "" {
					a.options.Logger.Print("Exit paused; available unsaved report remains accessible")
					return
				}
			}
			if a.options.RequestExit != nil {
				a.options.RequestExit()
			}
		}()
	})
}

func (a *Application) DiscardAndExit() error {
	a.mu.Lock()
	r := a.active
	if !a.closing || r == nil || a.reserved {
		a.mu.Unlock()
		return conflict("discard_unavailable", "Закрытие без сохранения сейчас недоступно.")
	}
	select {
	case <-r.released:
	default:
		a.mu.Unlock()
		return conflict("release_pending", "Дождитесь освобождения рабочих процессов и записи.")
	}
	v := r.runner.View()
	if !v.WorkersReleased || !v.WriterReleased || v.Durable && v.PersistenceError == "" {
		a.mu.Unlock()
		return conflict("discard_unavailable", "Нет освобождённого несохранённого результата.")
	}
	if a.discardAcknowledged {
		a.mu.Unlock()
		return nil
	}
	a.discardAcknowledged = true
	a.mu.Unlock()
	if a.options.RequestExit != nil {
		a.options.RequestExit()
	}
	return nil
}
