package request

import (
	"bytes"
	"encoding/json"
	"highloader/internal/domain"
	"highloader/internal/template"
	"math"
	"net/url"
	"strings"
	"testing"
	"time"
)

func fixture() (domain.Draft, domain.TemplateSource) {
	raw := []byte(`{"big":90071992547409931234567890123,"id":"{{sys.uuid}}"}`)
	p, _ := template.Parse(raw)
	s := domain.TemplateSource{ID: "one", Filename: "arbitrary.json", Raw: raw, Body: p.Body, Headers: p.Headers, Valid: true}
	d := domain.NewDraft()
	d.URL = "https://example.test/path?key=old&key=second&Key=retained"
	d.Templates = []domain.TemplateSource{s}
	d.SelectedIDs = []string{s.ID}
	return d, s
}

func TestResolve(t *testing.T) {
	d, s := fixture()
	d.Auth = domain.Auth{Mode: "bearer", Token: "token"}
	d.Headers = []domain.HeaderRow{{Enabled: true, Name: "authorization", Value: "common"}, {Enabled: true, Name: "X-Common", Value: "c"}}
	method := "GET"
	d.Settings[s.ID] = domain.TemplateSettings{Weight: 1, Method: &method, Headers: []domain.HeaderRow{{Enabled: true, Name: "Authorization", Value: "private"}, {Enabled: false, Name: "X-Common", Value: "disabled"}, {Enabled: true, Name: "Content-Type", Suppress: true}}}
	file := "file"
	s.Headers = []domain.FileHeader{{Name: "AUTHORIZATION", Value: &file}, {Name: "x-common", Value: nil}}
	r, e := Resolve(d, s)
	if e != nil {
		t.Fatal(e)
	}
	if r.Method != "GET" || r.MethodSource != "private" {
		t.Fatal(r)
	}
	h := map[string]domain.EffectiveHeader{}
	for _, v := range r.Headers {
		h[v.Name] = v
	}
	if h["Authorization"].Value != "file" || !h["Authorization"].Locked || h["Authorization"].Source != "file" || !h["X-Common"].Deleted || !h["Content-Type"].Deleted || h["Content-Type"].Source != "private" {
		t.Fatal(h)
	}
	d.Settings = map[string]domain.TemplateSettings{}
	s.Headers = nil
	r, e = Resolve(d, s)
	if e != nil || r.MethodSource != "common" {
		t.Fatal(e)
	}
	for _, v := range r.Headers {
		if v.Name == "Authorization" && v.Value != "common" {
			t.Fatal(v)
		}
	}
	d.Headers = nil
	r, _ = Resolve(d, s)
	for _, v := range r.Headers {
		if v.Name == "Authorization" && v.Value != "Bearer token" {
			t.Fatal(v)
		}
	}
	t.Log("Generated/common/private/file precedence and locked tombstones match configured request")
}

func TestAuth(t *testing.T) {
	cases := []struct {
		a           domain.Auth
		name, value string
	}{{domain.Auth{Mode: "none"}, "Authorization", ""}, {domain.Auth{Mode: "bearer", Token: "abc"}, "Authorization", "Bearer abc"}, {domain.Auth{Mode: "basic", Username: "u", Password: "p"}, "Authorization", "Basic dTpw"}, {domain.Auth{Mode: "api_key", Placement: "header", Name: "X-Key", Value: "secret"}, "X-Key", "secret"}, {domain.Auth{Mode: "api_key", Placement: "header", Name: "Content-Type", Value: "custom"}, "Content-Type", "custom"}}
	for _, c := range cases {
		d, s := fixture()
		d.Auth = c.a
		r, e := Resolve(d, s)
		if e != nil {
			t.Fatal(e)
		}
		got := ""
		for _, h := range r.Headers {
			if h.Name == c.name && !h.Deleted {
				got = h.Value
			}
		}
		if got != c.value {
			t.Fatalf("%s: %q", c.a.Mode, got)
		}
	}
	d, s := fixture()
	d.Auth = domain.Auth{Mode: "api_key", Placement: "query", Name: "key", Value: "a +/?"}
	d.Headers = []domain.HeaderRow{{Enabled: true, Name: "key", Value: "header"}}
	r, e := Resolve(d, s)
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(r.URL)
	if len(u.Query()["key"]) != 1 || u.Query().Get("key") != "a +/?" || u.Query().Get("Key") != "retained" {
		t.Fatal(r.URL)
	}
	for _, a := range []domain.Auth{{Mode: "bad"}, {Mode: "bearer"}, {Mode: "basic", Username: "a:b"}, {Mode: "api_key", Name: "x", Placement: "bad"}, {Mode: "api_key", Placement: "header"}} {
		d.Auth = a
		if _, e := Resolve(d, s); e == nil {
			t.Errorf("accepted auth %+v", a)
		}
	}
	t.Log("None/Bearer/Basic/header API key/query API key preserve exact intended contributions")
}

func TestPolicyAndInvalidSettings(t *testing.T) {
	d, s := fixture()
	timeout, delay, maxDelay, deadline := int64(5000), int64(0), int64(200), int64(8000)
	retries := 2
	network, timeouts, jitter := false, false, true
	mode := "exponential"
	statuses := []domain.StatusRange{{From: 503, To: 503}}
	d.Settings[s.ID] = domain.TemplateSettings{Weight: 2, Policy: domain.PolicyOverride{TimeoutMS: &timeout, MaxRetries: &retries, Statuses: &statuses, Network: &network, Timeouts: &timeouts, DelayMode: &mode, DelayMS: &delay, MaxDelayMS: &maxDelay, Jitter: &jitter, DeadlineMS: &deadline}}
	r, e := Resolve(d, s)
	if e != nil || r.Policy.Network || r.Policy.Timeouts || r.Policy.DelayMS != 0 || r.Policy.TimeoutMS != 5000 || r.Policy.MaxRetries != 2 || !r.Policy.Jitter {
		t.Fatalf("policy %+v %v", r, e)
	}
	for _, source := range r.PolicySources {
		if source != "private" {
			t.Fatal(r.PolicySources)
		}
	}
	d.Settings = map[string]domain.TemplateSettings{}
	r, _ = Resolve(d, s)
	if r.Policy.TimeoutMS != 30000 || r.Policy.MaxRetries != 0 || r.PolicySources["network"] != "common" {
		t.Fatal(r)
	}
	mutations := []func(*domain.Draft){func(d *domain.Draft) { d.URL = "ftp://example.test" }, func(d *domain.Draft) { d.URL = "http://u:p@example.test" }, func(d *domain.Draft) { d.URL = "https://example.test/#x" }, func(d *domain.Draft) { d.Method = "BAD METHOD" }, func(d *domain.Draft) {
		d.Headers = []domain.HeaderRow{{Enabled: true, Name: "x", Value: "a"}, {Enabled: true, Name: "X", Value: "b"}}
	}, func(d *domain.Draft) {
		d.Headers = []domain.HeaderRow{{Enabled: true, Name: "Content-Length", Value: "5"}}
	}, func(d *domain.Draft) { d.Headers = []domain.HeaderRow{{Enabled: true, Name: "x", Value: "a\r\nb"}} }, func(d *domain.Draft) { d.Policy.TimeoutMS = 0 }, func(d *domain.Draft) { d.Policy.MaxRetries = -1 }, func(d *domain.Draft) { d.Policy.DelayMode = "bad" }, func(d *domain.Draft) { d.Policy.MaxDelayMS = 0 }, func(d *domain.Draft) { d.Policy.Statuses = []domain.StatusRange{{From: 200, To: 599}} }, func(d *domain.Draft) { d.Settings[s.ID] = domain.TemplateSettings{Weight: math.Inf(1)} }}
	for i, mutate := range mutations {
		fresh, source := fixture()
		mutate(&fresh)
		if _, e := Resolve(fresh, source); e == nil {
			t.Errorf("invalid setting %d accepted", i)
		}
	}
	t.Log("All ten policy fields inherit independently; explicit false/zero retained; invalid requests rejected")
}

func TestFreeze(t *testing.T) {
	d, s := fixture()
	d.Revision = math.MaxUint64
	d.Templates = append(d.Templates, domain.TemplateSource{ID: "invalid", Filename: "bad", Raw: []byte("broken")})
	snapshot, e := Freeze(d, "run", "test", time.Unix(0, 0))
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(snapshot)
	if !bytes.Contains(raw, []byte(`"revision":"18446744073709551615"`)) || !bytes.Contains(raw, []byte("90071992547409931234567890123")) {
		t.Fatal(string(raw))
	}
	d.Templates[0].Raw[0] = '!'
	d.Templates[0].Body[0] = '!'
	d.SelectedIDs[0] = "other"
	d.Policy.Statuses[0].From = 501
	if snapshot.Draft.Templates[0].Raw[0] != '{' || snapshot.Draft.Templates[0].Body[0] != '{' || snapshot.Draft.SelectedIDs[0] != s.ID || snapshot.Effective[0].Policy.Statuses[0].From != 429 || len(snapshot.Draft.Templates) != 2 {
		t.Fatal("snapshot aliases draft")
	}
	if snapshot.Resources.TotalBytes <= 0 || snapshot.Resources.DurationMS != 60000 {
		t.Fatal(snapshot.Resources)
	}
	t.Log("Snapshot preserves huge-number/source/revision precision and isolates all originals/effective fields from draft mutations")
}

func TestPreflight(t *testing.T) {
	mutations := []func(*domain.Draft){func(d *domain.Draft) { d.SelectedIDs = nil }, func(d *domain.Draft) { d.SelectedIDs = []string{"absent"} }, func(d *domain.Draft) { d.SelectedIDs = []string{"one", "one"} }, func(d *domain.Draft) { d.Templates[0].Valid = false }, func(d *domain.Draft) { d.Templates[0].Body = []byte(`"{{sys.user.id}}"`) }, func(d *domain.Draft) { d.Profile.Mode = "closed"; d.Profile.Steps[0].Level = 1001 }, func(d *domain.Draft) { d.Profile.Steps[0].DurationSeconds = 0 }, func(d *domain.Draft) { d.Profile.Steps[0].Level = 0 }, func(d *domain.Draft) { d.Capacity = 0 }, func(d *domain.Draft) { d.Samples.BodyCapBytes = math.MaxInt64 }, func(d *domain.Draft) { d.Profile.Steps[0].DurationSeconds = math.MaxInt64 }, func(d *domain.Draft) { d.Samples.Success = -1 }, func(d *domain.Draft) { d.Profile.Mode = "bad" }, func(d *domain.Draft) { d.Profile.RetryAccounting = "bad" }, func(d *domain.Draft) { d.SelectionMode = "bad" }, func(d *domain.Draft) { d.SchemaVersion = 2 }}
	for i, mutate := range mutations {
		d, _ := fixture()
		mutate(&d)
		_, _, issues := Preflight(d)
		if len(issues) == 0 {
			t.Errorf("invalid preflight case %d admitted", i)
		}
	}
	d, _ := fixture()
	d.Profile.Mode = "closed"
	d.Templates[0].Body = []byte(`"{{sys.user.id}}"`)
	_, _, issues := Preflight(d)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	d.Profile.Mode = "open"
	d.Templates[0].Body = []byte(`"\\{{sys.user.id}}"`)
	_, _, issues = Preflight(d)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	d.Samples = domain.Samples{}
	_, est, issues := Preflight(d)
	if len(issues) != 0 || est.CandidateBytes != 0 || est.RetainedSampleBytes != 0 {
		t.Fatal(est, issues)
	}
	if _, e := Freeze(d, "", "v", time.Time{}); e == nil || !strings.Contains(e.Error(), "ID") {
		t.Fatal(e)
	}
	t.Log("Selection/mode/profile/overflow guards enforced; zero sampling valid; escaped user token allowed in open mode")
}

func TestCompletionResourceBound(t *testing.T) {
	d := domain.NewDraft()
	d.Capacity = 1
	d.Samples = domain.Samples{}
	a, err := resources(d)
	if err != nil {
		t.Fatal(err)
	}
	d.Capacity = 2
	b, err := resources(d)
	if err != nil || b.TotalBytes <= a.TotalBytes || a.TotalBytes < 600*7*7007*32 {
		t.Fatal("missing evidence budget", a, b, err)
	}
	d.Capacity = math.MaxInt64 / 4
	if _, err = resources(d); err == nil {
		t.Fatal("completion reserve overflow accepted")
	}
	t.Log("histogram/cache/batch/report reserve included; extreme key arithmetic rejected")
}
