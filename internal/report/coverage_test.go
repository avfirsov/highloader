package report

import (
	"encoding/base64"
	"highloader/internal/domain"
	"highloader/internal/engine"
	"highloader/internal/history"
	"highloader/internal/metrics"
	"strings"
	"testing"
)

func TestComparisonIdentityChoices(t *testing.T) {
	for _, c := range []struct {
		name    string
		right   []domain.TemplateSource
		mapping map[string]string
		matched bool
		invalid bool
	}{
		{name: "unique digest", right: []domain.TemplateSource{{ID: "right", Digest: "digest"}}, matched: true},
		{name: "ambiguous digest", right: []domain.TemplateSource{{ID: "right", Digest: "digest"}, {ID: "other", Digest: "digest"}}},
		{name: "filename alone", right: []domain.TemplateSource{{ID: "right", Filename: "same.json", Digest: "different"}}},
		{name: "explicit override", right: []domain.TemplateSource{{ID: "right", Digest: "different"}}, mapping: map[string]string{"left": "right"}, matched: true},
		{name: "missing target", right: []domain.TemplateSource{{ID: "right"}}, mapping: map[string]string{"left": "absent"}, invalid: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			l, e := Build(fixture(), Options{})
			if e != nil {
				t.Fatal(e)
			}
			r := l
			l.Templates = append([]DimensionSummary(nil), l.Templates...)
			l.Templates[0].TemplateID = "left"
			l.Snapshot.Draft.Templates = []domain.TemplateSource{{ID: "left", Filename: "same.json", Digest: "digest"}}
			r.Templates = nil
			r.Snapshot.Draft.Templates = c.right
			for _, src := range c.right {
				r.Templates = append(r.Templates, DimensionSummary{TemplateID: src.ID, Summary: r.Summary})
			}
			out, e := Compare(l, r, c.mapping)
			if c.invalid {
				if e == nil {
					t.Fatal("invalid mapping accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			matched, unmatched := false, false
			for _, d := range out.Deltas {
				if d.Key == "template.left.initial" {
					matched = true
				}
			}
			for _, d := range out.Differences {
				if d.Path == "template.unmatched" && d.Left == "left" {
					unmatched = true
				}
			}
			if matched != c.matched || unmatched == c.matched {
				t.Fatalf("matched=%v unmatched=%v", matched, unmatched)
			}
		})
	}
	t.Log("unique digests and explicit mappings match; ambiguous and filename-only sources remain unmatched")
}

func TestReportRangeAndPopulation(t *testing.T) {
	h := fixture()
	h.Samples = []engine.Sample{{TemplateID: "other", Sequence: 1}, {TemplateID: "t", Sequence: 2}, {TemplateID: "t", Sequence: 3}, {TemplateID: "t", Sequence: 4}}
	for _, population := range []string{"http", "technical"} {
		r, e := Build(h, Options{TemplateID: "t", Population: population, SampleOffset: 1, SampleLimit: 1})
		if e != nil {
			t.Fatal(e)
		}
		if len(r.Samples) != 1 || r.Samples[0].Sequence != 3 || r.Range.TotalSamples != "3" || r.Range.NextSampleOffset == nil || *r.Range.NextSampleOffset != 2 || r.LogicalPopulationNote == "" || r.Summary.LogicalLatency.P50MS != nil {
			t.Fatal(r)
		}
		for _, points := range r.ChartScopes {
			for _, p := range points {
				if p.P50MS != nil || p.P99MS != nil {
					t.Fatal("unfiltered latency exposed", p)
				}
			}
		}
	}
	beyond, e := Build(h, Options{FromIndex: 99, SampleOffset: 99})
	if e != nil || len(beyond.Points) != 0 || len(beyond.Samples) != 0 || beyond.Range.TotalBuckets != "2" || beyond.Range.TotalSamples != "4" || beyond.Range.NextIndex != nil || beyond.Range.NextSampleOffset != nil {
		t.Fatal(beyond, e)
	}
	for _, o := range []Options{{FromIndex: -1}, {Limit: -1}, {SampleOffset: -1}, {SampleLimit: -1}, {TemplateID: "missing"}, {Population: "bad"}} {
		if _, e := Build(h, o); e == nil {
			t.Fatal("accepted", o)
		}
	}
	t.Log("filter-before-offset, continuation and unavailable logical population latency")
}

func TestExportInputGuards(t *testing.T) {
	r, e := Build(fixture(), Options{})
	if e != nil {
		t.Fatal(e)
	}
	js, css := []byte("/* test renderer */"), []byte("/* test style */")
	for _, assets := range [][2][]byte{{nil, css}, {js, nil}} {
		if _, e := Export(r, false, assets[0], assets[1]); e == nil {
			t.Fatal("missing asset accepted")
		}
	}
	if _, e := Export(make(chan int), false, js, css); e == nil {
		t.Fatal("unsupported value accepted")
	}
	partial, e := Build(fixture(), Options{Limit: 1})
	if e != nil {
		t.Fatal(e)
	}
	c, e := Compare(r, partial, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := Export(c, false, js, css); e == nil {
		t.Fatal("partial comparison exported")
	}
	out, e := Export(c, true, js, css)
	if e == nil || strings.Contains(string(out), "highloader-report-data") {
		t.Fatal("authorization opt-in bypassed completeness")
	}
	t.Log("missing assets and partial comparisons cannot produce misleading exports")
}

func TestOpaqueSourceRedaction(t *testing.T) {
	for _, c := range []struct {
		name, raw string
		removed   bool
	}{
		{"standard label", "broken Authorization: opaque-value", true},
		{"custom label", "broken X-Custom-Credential: opaque-value", true},
		{"harmless", "broken document without sensitive metadata", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, e := Build(fixture(), Options{})
			if e != nil {
				t.Fatal(e)
			}
			r.Snapshot.Draft.Auth = domain.Auth{Mode: "api-key", Name: "X-Custom-Credential", Value: "different-known-value", Placement: "header"}
			r.Snapshot.Draft.Templates = []domain.TemplateSource{{Raw: []byte(c.raw)}}
			for _, include := range []bool{false, true} {
				out, e := Export(r, include, []byte("test"), []byte("test"))
				if e != nil {
					t.Fatal(e)
				}
				tree := embedded(t, out).(map[string]any)
				sources := tree["snapshot"].(map[string]any)["draft"].(map[string]any)["templates"].([]any)
				decoded, e := base64.StdEncoding.DecodeString(sources[0].(map[string]any)["raw"].(string))
				if e != nil {
					t.Fatal(e)
				}
				if include || !c.removed {
					if string(decoded) != c.raw {
						t.Fatal("original lost", string(decoded))
					}
				} else if string(decoded) != removed {
					t.Fatal("opaque credential retained", string(decoded))
				}
			}
		})
	}
	t.Log("opaque credential labels removed by default; harmless and opt-in originals preserved")
}

func TestOverlaySamplePageReplacement(t *testing.T) {
	h := fixture()
	h.SchemaVersion = 2
	h.LatestSequence = 1
	h.Samples = []engine.Sample{{Sequence: 2, Attempt: 1, Request: metrics.Body{Data: []byte("old"), Present: true}}}
	next := 4
	h.Page = &history.ReportPage{Limit: 2, Sequence: 1, TotalBuckets: 2, TotalSamples: 9, SampleOffset: 3, NextSampleOffset: &next}
	cp := *h.Checkpoint
	cp.Sequence = 2
	cp.Samples = []engine.Sample{{Sequence: 2, Attempt: 1, Request: metrics.Body{Data: []byte("new"), Present: true}}, {Sequence: 99, Attempt: 1}}
	out := Overlay(h, cp)
	if len(out.Samples) != 1 || string(out.Samples[0].Request.Data) != "new" || string(h.Samples[0].Request.Data) != "old" {
		t.Fatal(out.Samples, h.Samples)
	}
	r, e := Build(out, Options{Limit: 2, SampleOffset: 3, SampleLimit: 1})
	if e != nil || len(r.Samples) != 1 || r.Range.TotalSamples != "9" || r.Range.NextSampleOffset == nil || *r.Range.NextSampleOffset != 4 {
		t.Fatal(r.Range, e)
	}
	h.Page = nil
	full := Overlay(h, cp)
	if len(full.Samples) != 2 || full.Samples[1].Sequence != 99 {
		t.Fatal(full.Samples)
	}
	t.Log("matching samples replaced; page bounded and total/cursor stable; full evidence admits new keys")
}

func TestComparisonRejectsInvalidRateIntervals(t *testing.T) {
	for _, kind := range []string{"overlap", "zero interval", "missing rate"} {
		t.Run(kind, func(t *testing.T) {
			l, e := Build(fixture(), Options{})
			if e != nil {
				t.Fatal(e)
			}
			r, e := Build(fixture(), Options{})
			if e != nil {
				t.Fatal(e)
			}
			points := r.ChartScopes[""]
			switch kind {
			case "overlap":
				points[1].StartMS = points[0].EndMS - 1
			case "zero interval":
				points[0].EndMS = points[0].StartMS
			case "missing rate":
				points[0].TotalRate = nil
			}
			r.ChartScopes[""] = points
			c, e := Compare(l, r, nil)
			if e != nil {
				t.Fatal(e)
			}
			found := false
			for _, d := range c.Deltas {
				if d.Key == "summary.observedIntervals.totalPerSecond" {
					found = true
					if d.Absolute != nil || d.Percent != nil {
						t.Fatal(d)
					}
				}
			}
			if !found {
				t.Fatal("rate comparison missing")
			}
		})
	}
	t.Log("invalid interval evidence retains unavailable throughput deltas")
}
