package app

import (
	"context"
	"highloader/internal/api"
	"highloader/internal/domain"
	"highloader/internal/request"
	"highloader/internal/template"
	"math"
)

func (a *Application) Draft() domain.Draft {
	a.mu.Lock()
	defer a.mu.Unlock()
	return copyValue(a.draft)
}

func (a *Application) Update(rev uint64, e api.EditableDraft) (domain.Draft, error) {
	d, err := a.reserve(&rev)
	if err != nil {
		return domain.Draft{}, err
	}
	defer a.release()
	d.SelectedIDs = e.SelectedIDs
	d.Settings = e.Settings
	d.URL = e.URL
	d.Method = e.Method
	d.Auth = e.Auth
	d.Headers = e.Headers
	d.Policy = e.Policy
	d.Profile = e.Profile
	d.SelectionMode = e.SelectionMode
	d.Capacity = e.Capacity
	d.Samples = e.Samples
	if d.SelectedIDs == nil {
		d.SelectedIDs = []string{}
	}
	if d.Settings == nil {
		d.Settings = map[string]domain.TemplateSettings{}
	}
	if d.Headers == nil {
		d.Headers = []domain.HeaderRow{}
	}
	if d.Profile.Steps == nil {
		d.Profile.Steps = []domain.Step{}
	}
	if d.Policy.Statuses == nil {
		d.Policy.Statuses = []domain.StatusRange{}
	}
	for id, s := range d.Settings {
		if s.Headers == nil {
			s.Headers = []domain.HeaderRow{}
			d.Settings[id] = s
		}
	}
	return a.replace(rev, d)
}

func (a *Application) Validate() api.Validation {
	e, r, d := request.Preflight(a.Draft())
	if e == nil {
		e = []domain.EffectiveRequest{}
	}
	if d == nil {
		d = []domain.Diagnostic{}
	}
	return api.Validation{Effective: e, Resources: r, Diagnostics: d}
}

func (a *Application) Import(ctx context.Context, rev uint64) (api.ImportResult, error) {
	d, err := a.reserve(&rev)
	if err != nil {
		return api.ImportResult{}, err
	}
	defer a.release()
	path, canceled, err := a.options.PickFolder()
	if err != nil {
		return api.ImportResult{}, err
	}
	if canceled {
		return api.ImportResult{Draft: d, Canceled: true}, nil
	}
	if err = ctx.Err(); err != nil {
		return api.ImportResult{}, err
	}
	imported, err := template.ImportDirectory(path)
	if err != nil {
		return api.ImportResult{}, err
	}
	d.Templates = imported.Templates
	d.SelectedIDs = imported.SelectedIDs
	d.Settings = imported.Settings
	d, err = a.replace(rev, d)
	return api.ImportResult{Draft: d}, err
}

func (a *Application) Repeat(ctx context.Context, id string, rev uint64) (domain.Draft, error) {
	if _, err := a.reserve(&rev); err != nil {
		return domain.Draft{}, err
	}
	defer a.release()
	d, err := a.options.Store.Repeat(ctx, id)
	if err != nil {
		return domain.Draft{}, err
	}
	return a.replace(rev, d)
}

func (a *Application) replace(rev uint64, d domain.Draft) (domain.Draft, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing || !a.reserved || a.draft.Revision != rev {
		return domain.Draft{}, conflict("stale_revision", "Операция устарела или приложение завершает работу.")
	}
	if rev == math.MaxUint64 {
		return domain.Draft{}, conflict("revision_exhausted", "Исчерпан диапазон версий настроек.")
	}
	d.Revision = rev + 1
	a.draft = copyValue(d)
	a.options.Logger.Print("Draft revision advanced with authoritative originals")
	return copyValue(a.draft), nil
}
