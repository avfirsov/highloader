package api

import (
	"context"
	"highloader/internal/domain"
	"highloader/internal/engine"
	"highloader/internal/history"
	"highloader/internal/report"
)

type EditableDraft struct {
	SelectedIDs   []string                           `json:"selectedIds"`
	Settings      map[string]domain.TemplateSettings `json:"settings"`
	URL           string                             `json:"url"`
	Method        string                             `json:"method"`
	Auth          domain.Auth                        `json:"auth"`
	Headers       []domain.HeaderRow                 `json:"headers"`
	Policy        domain.Policy                      `json:"policy"`
	Profile       domain.Profile                     `json:"profile"`
	SelectionMode string                             `json:"selectionMode"`
	Capacity      int64                              `json:"capacity"`
	Samples       domain.Samples                     `json:"samples"`
}
type Active struct {
	RunID   *string      `json:"runId"`
	View    *engine.View `json:"view"`
	Closing bool         `json:"closing"`
}
type Validation struct {
	Effective   []domain.EffectiveRequest `json:"effective"`
	Resources   domain.ResourceEstimate   `json:"resources"`
	Diagnostics []domain.Diagnostic       `json:"diagnostics"`
}
type ImportResult struct {
	Draft    domain.Draft `json:"draft"`
	Canceled bool         `json:"canceled"`
}
type HistoryPage struct {
	Items      []history.Summary `json:"items"`
	NextOffset *int              `json:"nextOffset"`
}
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e *Error) Error() string { return e.Message }

type Selection struct {
	RunID                string            `json:"runId"`
	LeftID               string            `json:"leftId"`
	RightID              string            `json:"rightId"`
	Mapping              map[string]string `json:"mapping,omitempty"`
	IncludeAuthorization bool              `json:"includeAuthorization"`
}
type Service interface {
	Draft() domain.Draft
	Update(uint64, EditableDraft) (domain.Draft, error)
	Validate() Validation
	Import(context.Context, uint64) (ImportResult, error)
	Start(context.Context, uint64, string) (Active, error)
	Active() Active
	View(context.Context, string) (engine.View, error)
	Stop(context.Context, string, bool) (engine.View, error)
	History(context.Context, int, int) (HistoryPage, error)
	Repeat(context.Context, string, uint64) (domain.Draft, error)
	Delete(context.Context, string) error
	Clear(context.Context) error
	Report(context.Context, string, report.Options) (report.RunReport, error)
	Compare(context.Context, string, string, map[string]string) (report.Comparison, error)
	Export(context.Context, Selection) ([]byte, error)
	Exit()
	DiscardAndExit() error
}
