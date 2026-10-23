package app

import (
	"context"
	"highloader/internal/api"
	"highloader/internal/history"
	"highloader/internal/report"
)

func (a *Application) History(ctx context.Context, limit, offset int) (api.HistoryPage, error) {
	rows, err := a.options.Store.List(ctx, limit, offset)
	if rows == nil {
		rows = []history.Summary{}
	}
	page := api.HistoryPage{Items: rows}
	if len(rows) == limit {
		n := offset + len(rows)
		page.NextOffset = &n
	}
	return page, err
}

func (a *Application) Delete(ctx context.Context, id string) error {
	if _, err := a.reserve(nil); err != nil {
		return err
	}
	defer a.release()
	err := a.options.Store.Delete(ctx, id)
	if err == nil {
		a.mu.Lock()
		if a.active != nil && a.active.id == id {
			a.active = nil
		}
		a.mu.Unlock()
	}
	return err
}
func (a *Application) Clear(ctx context.Context) error {
	if _, err := a.reserve(nil); err != nil {
		return err
	}
	defer a.release()
	err := a.options.Store.Clear(ctx)
	if err == nil {
		a.mu.Lock()
		a.active = nil
		a.mu.Unlock()
	}
	return err
}

func (a *Application) Report(ctx context.Context, id string, o report.Options) (report.RunReport, error) {
	var h history.Report
	var err error
	if o.Limit > 0 {
		h, err = a.options.Store.ReadReportPage(ctx, id, history.ReportQuery{TemplateID: o.TemplateID, FromIndex: o.FromIndex, Limit: o.Limit, SampleOffset: o.SampleOffset, SampleLimit: o.SampleLimit, ExpectedSequence: o.ExpectedSequence})
	} else {
		h, err = a.options.Store.ReadReport(ctx, id)
	}
	if err != nil {
		return report.RunReport{}, err
	}
	a.mu.Lock()
	r := a.active
	a.mu.Unlock()
	if r != nil && r.id == id {
		live := r.runner.Report()
		h = report.Overlay(h, live)
		if live.Sequence > h.LatestSequence && (h.Checkpoint == nil || h.Checkpoint.Sequence != live.Sequence) {
			return report.RunReport{}, &api.Error{Code: "invalid_overlay", Message: "Доступный срез не прошёл проверку согласованности.", Status: 503}
		}
	}
	if o.ExpectedSequence != nil && h.Checkpoint != nil && h.Checkpoint.Sequence != *o.ExpectedSequence {
		return report.RunReport{}, history.ErrStale
	}
	result, err := report.Build(h, o)
	if err != nil {
		return report.RunReport{}, &api.Error{Code: "invalid_report", Message: "Параметры отчёта не соответствуют сохранённому запуску.", Status: 422}
	}
	return result, nil
}

func (a *Application) Compare(ctx context.Context, left, right string, mapping map[string]string) (report.Comparison, error) {
	l, err := a.Report(ctx, left, report.Options{Limit: 600, SampleLimit: 100})
	if err != nil {
		return report.Comparison{}, err
	}
	r, err := a.Report(ctx, right, report.Options{Limit: 600, SampleLimit: 100})
	if err != nil {
		return report.Comparison{}, err
	}
	result, err := report.Compare(l, r, mapping)
	if err != nil {
		return report.Comparison{}, &api.Error{Code: "invalid_mapping", Message: "Сопоставление шаблонов некорректно.", Status: 422}
	}
	return result, nil
}

func (a *Application) Export(ctx context.Context, s api.Selection) ([]byte, error) {
	if len(a.options.ReportJS) == 0 || len(a.options.ReportCSS) == 0 {
		return nil, &api.Error{Code: "report_assets_missing", Message: "Автономный отчёт недоступен: отсутствует собранный JS/CSS интерфейс отчёта.", Status: 503}
	}
	var data any
	var err error
	if s.RunID != "" {
		data, err = a.Report(ctx, s.RunID, report.Options{})
	} else {
		var l, r report.RunReport
		l, err = a.Report(ctx, s.LeftID, report.Options{})
		if err == nil {
			r, err = a.Report(ctx, s.RightID, report.Options{})
		}
		if err == nil {
			data, err = report.Compare(l, r, s.Mapping)
		}
	}
	if err != nil {
		return nil, err
	}
	return report.Export(data, s.IncludeAuthorization, a.options.ReportJS, a.options.ReportCSS)
}
