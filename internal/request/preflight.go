package request

import (
	"encoding/json"
	"fmt"
	"highloader/internal/domain"
	"highloader/internal/metrics"
	"highloader/internal/template"
	"math"
	"time"
)

func resources(d domain.Draft) (domain.ResourceEstimate, error) {
	r := domain.ResourceEstimate{}
	add := func(a, b int64) (int64, error) {
		if a < 0 || b < 0 || a > math.MaxInt64-b {
			return 0, fmt.Errorf("переполнение оценки ресурсов")
		}
		return a + b, nil
	}
	mul := func(a, b int64) (int64, error) {
		if a < 0 || b < 0 || a != 0 && b > math.MaxInt64/a {
			return 0, fmt.Errorf("переполнение оценки ресурсов")
		}
		return a * b, nil
	}
	var e error
	var maxBody int64
	for _, s := range d.Templates {
		r.SourceBytes, e = add(r.SourceBytes, int64(len(s.Raw)))
		if e != nil {
			return r, e
		}
		if int64(len(s.Body)) > maxBody {
			maxBody = int64(len(s.Body))
		}
	}
	// Conservatively reserve escaped/expanded scalar space, including context IDs.
	maxBody, e = mul(maxBody, 64)
	if e != nil {
		return r, e
	}
	r.InflightBodyBytes, e = mul(maxBody, d.Capacity)
	if e != nil {
		return r, e
	}
	cap2, e := mul(d.Samples.BodyCapBytes, 2)
	if e != nil {
		return r, e
	}
	r.CandidateBytes, e = mul(cap2, d.Capacity)
	if e != nil {
		return r, e
	}
	quota, e := add(d.Samples.Success, d.Samples.Failure)
	if e != nil {
		return r, e
	}
	quota, e = mul(quota, int64(len(d.SelectedIDs)))
	if e != nil {
		return r, e
	}
	r.RetainedSampleBytes, e = mul(quota, cap2)
	if e != nil {
		return r, e
	}
	for _, n := range []int64{r.SourceBytes, r.InflightBodyBytes, r.CandidateBytes, r.RetainedSampleBytes} {
		r.TotalBytes, e = add(r.TotalBytes, n)
		if e != nil {
			return r, e
		}
	}
	// Schema2 reserves full finite histogram domains, bounded batch copies and one detached report.
	templates := int64(len(d.SelectedIDs))
	keys, e := mul(templates, 2)
	if e != nil {
		return r, e
	}
	keys, e = add(keys, d.Capacity)
	if e != nil {
		return r, e
	}
	keys, e = add(keys, 2)
	if e != nil {
		return r, e
	}
	keys, e = mul(keys, 2)
	if e != nil {
		return r, e
	}
	emergency, e := mul(d.Capacity, 2)
	if e != nil {
		return r, e
	}
	keys, e = add(keys, emergency)
	if e != nil {
		return r, e
	}
	// Ten immutable batches + emergency; writer/pending/report/serialization copies are budgeted conservatively.
	values, e := mul(keys, 11*4)
	if e != nil {
		return r, e
	}
	values, e = add(values, 600)
	if e != nil {
		return r, e
	}
	aggregates, e := add(d.Capacity, templates)
	if e != nil {
		return r, e
	}
	aggregates, e = add(aggregates, 14)
	if e != nil {
		return r, e
	}
	values, e = add(values, aggregates)
	if e != nil {
		return r, e
	}
	histogramBytes, e := mul(int64(metrics.MaxBins()), 32)
	if e != nil {
		return r, e
	}
	histogramBytes, e = add(histogramBytes, 128)
	if e != nil {
		return r, e
	}
	valueBytes, e := mul(histogramBytes, 7)
	if e != nil {
		return r, e
	}
	valueBytes, e = add(valueBytes, 512)
	if e != nil {
		return r, e
	}
	evidenceBytes, e := mul(values, valueBytes)
	if e != nil {
		return r, e
	}
	// Header candidates and retained headers share the adapter's explicit 1 MiB response-header ceiling.
	headerOwners, e := add(d.Capacity, quota)
	if e != nil {
		return r, e
	}
	headerBytes, e := mul(headerOwners, 1<<20)
	if e != nil {
		return r, e
	}
	evidenceBytes, e = add(evidenceBytes, headerBytes)
	if e != nil {
		return r, e
	}
	// Twelve CPU-only amendments, including bounded identity/reason and four detached owners.
	evidenceBytes, e = add(evidenceBytes, 12*4*1024)
	if e != nil {
		return r, e
	}
	r.TotalBytes, e = add(r.TotalBytes, evidenceBytes)
	if e != nil {
		return r, e
	}
	for _, s := range d.Profile.Steps {
		ms, e := mul(s.DurationSeconds, 1000)
		if e != nil {
			return r, e
		}
		r.DurationMS, e = add(r.DurationMS, ms)
		if e != nil || r.DurationMS > math.MaxInt64/int64(time.Millisecond) {
			return r, fmt.Errorf("суммарная длительность не представима")
		}
		if _, e = mul(s.DurationSeconds, s.Level); e != nil {
			return r, e
		}
	}
	return r, nil
}

func Preflight(d domain.Draft) ([]domain.EffectiveRequest, domain.ResourceEstimate, []domain.Diagnostic) {
	errors := []domain.Diagnostic{}
	fail := func(field, message string) {
		errors = append(errors, domain.Diagnostic{Field: field, Message: message})
	}
	effective := []domain.EffectiveRequest{}
	if d.SchemaVersion != 1 {
		fail("schemaVersion", "Неподдерживаемая версия конфигурации")
	}
	if d.Capacity <= 0 {
		fail("capacity", "Вместимость должна быть положительной")
	}
	if d.Samples.Success < 0 || d.Samples.Failure < 0 || d.Samples.BodyCapBytes < 0 {
		fail("samples", "Квоты и размер образца не могут быть отрицательными")
	}
	if d.Profile.Mode != "open" && d.Profile.Mode != "closed" {
		fail("profile.mode", "Выберите open или closed")
	}
	if d.Profile.RetryAccounting != "within" && d.Profile.RetryAccounting != "above" {
		fail("profile.retryAccounting", "Выберите within или above")
	}
	switch d.SelectionMode {
	case "round_robin", "uniform_random", "shuffled_round", "weighted_random":
	default:
		fail("selectionMode", "Неизвестный режим выбора")
	}
	positive := false
	for i, s := range d.Profile.Steps {
		field := fmt.Sprintf("profile.steps.%d", i)
		if s.DurationSeconds <= 0 || s.Level < 0 {
			fail(field, "Длительность — положительные целые секунды, уровень — неотрицательный")
		}
		if s.Level > 0 {
			positive = true
		}
		if d.Profile.Mode == "closed" && s.Level > d.Capacity {
			fail(field, "Число пользователей превышает вместимость")
		}
	}
	if len(d.Profile.Steps) == 0 || !positive {
		fail("profile.steps", "Нужен хотя бы один шаг с положительным уровнем")
	}
	sources := map[string]domain.TemplateSource{}
	for _, s := range d.Templates {
		if s.ID == "" {
			fail("templates", "Пустой идентификатор шаблона")
		}
		if _, ok := sources[s.ID]; ok {
			fail("templates", "Повторный идентификатор шаблона")
		}
		sources[s.ID] = s
	}
	seen := map[string]bool{}
	if len(d.SelectedIDs) == 0 {
		fail("selectedIds", "Выберите хотя бы один корректный шаблон")
	}
	weightSum := 0.0
	for _, id := range d.SelectedIDs {
		field := "templates." + id
		if seen[id] {
			fail(field, "Шаблон выбран повторно")
			continue
		}
		seen[id] = true
		s, ok := sources[id]
		if !ok {
			fail(field, "Выбранный шаблон отсутствует")
			continue
		}
		if !s.Valid {
			fail(field, "Выбранный шаблон содержит ошибки")
			continue
		}
		p, e := template.Compile(s.Body)
		if e != nil {
			fail(field, e.Error())
			continue
		}
		if d.Profile.Mode == "open" && p.UsesUserID() {
			fail(field, "sys.user.id доступен только в closed-режиме")
		}
		r, e := Resolve(d, s)
		if e != nil {
			fail(field, e.Error())
			continue
		}
		weightSum += r.Weight
		effective = append(effective, r)
	}
	if math.IsInf(weightSum, 0) {
		fail("settings.weight", "Сумма весов не представима")
	}
	estimate, e := resources(d)
	if e != nil {
		fail("resources", e.Error())
	}
	return effective, estimate, errors
}

func Freeze(d domain.Draft, runID, appVersion string, now time.Time) (domain.RunSnapshot, error) {
	var copy domain.Draft
	raw, e := json.Marshal(d)
	if e != nil {
		return domain.RunSnapshot{}, e
	}
	if e = json.Unmarshal(raw, &copy); e != nil {
		return domain.RunSnapshot{}, e
	}
	effective, estimate, diagnostics := Preflight(copy)
	if len(diagnostics) > 0 {
		return domain.RunSnapshot{}, fmt.Errorf("%s: %s", diagnostics[0].Field, diagnostics[0].Message)
	}
	if runID == "" {
		return domain.RunSnapshot{}, fmt.Errorf("run ID не может быть пустым")
	}
	return domain.RunSnapshot{SchemaVersion: 1, RunID: runID, CreatedAt: now.UTC().Format(time.RFC3339Nano), AppVersion: appVersion, Draft: copy, Effective: effective, Resources: estimate}, nil
}
