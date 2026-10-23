package request

import (
	"highloader/internal/domain"
	"math"
	"testing"
	"time"
)

func TestPreflightRejection(t *testing.T) {
	// Mutated fixture -> visible diagnostics -> rejected immutable snapshot.
	cases := map[string]func(*domain.Draft){
		"duration overflow": func(d *domain.Draft) { d.Profile.Steps[0].DurationSeconds = math.MaxInt64/int64(time.Second) + 1 },
		"demand overflow": func(d *domain.Draft) {
			d.Profile.Steps[0].DurationSeconds = 2
			d.Profile.Steps[0].Level = math.MaxInt64
		},
		"bad query":    func(d *domain.Draft) { d.URL = "http://fixture.invalid/?a=%xx" },
		"bad port":     func(d *domain.Draft) { d.URL = "http://fixture.invalid:65536/" },
		"empty method": func(d *domain.Draft) { d.Method = "" },
		"bad private header": func(d *domain.Draft) {
			d.Settings["one"] = domain.TemplateSettings{Weight: 1, Headers: []domain.HeaderRow{{Enabled: true, Name: "Bad Name"}}}
		},
		"bad file header":      func(d *domain.Draft) { d.Templates[0].Headers = []domain.FileHeader{{Name: "Bad Name"}} },
		"inflight overflow":    func(d *domain.Draft) { d.Capacity = math.MaxInt64 },
		"empty identity":       func(d *domain.Draft) { d.Templates[0].ID = "" },
		"duplicate source":     func(d *domain.Draft) { d.Templates = append(d.Templates, d.Templates[0]) },
		"bad token":            func(d *domain.Draft) { d.Templates[0].Body = []byte(`{"a":"{{unknown}}"}`) },
		"bad generated header": func(d *domain.Draft) { d.Auth = domain.Auth{Mode: "bearer", Token: "x\ny"} },
		"quota overflow":       func(d *domain.Draft) { d.Samples.Success = math.MaxInt64; d.Samples.Failure = 1 },
		"candidate overflow":   func(d *domain.Draft) { d.Samples.BodyCapBytes = math.MaxInt64 / 2; d.Capacity = 3 },
		"retained overflow": func(d *domain.Draft) {
			d.Capacity = 1
			d.Samples.BodyCapBytes = 1
			d.Samples.Success = math.MaxInt64 / 2
			d.Samples.Failure = 0
		},
		"weight sum": func(d *domain.Draft) {
			s := d.Templates[0]
			s.ID = "two"
			d.Templates = append(d.Templates, s)
			d.SelectedIDs = append(d.SelectedIDs, "two")
			d.Settings["one"] = domain.TemplateSettings{Weight: math.MaxFloat64}
			d.Settings["two"] = domain.TemplateSettings{Weight: math.MaxFloat64}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d, _ := fixture()
			mutate(&d)
			_, _, issues := Preflight(d)
			if len(issues) == 0 {
				t.Fatal("missing diagnostic")
			}
			frozen, err := Freeze(d, "run", "test", time.Time{})
			if err == nil || frozen.RunID != "" {
				t.Fatal("invalid draft frozen")
			}
		})
	}
	t.Log("Malformed identities, tokens, headers, weights and resource overflow block snapshots")
}
