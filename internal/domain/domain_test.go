package domain

import "testing"

func TestDefaults(t *testing.T) {
	a, b := NewDraft(), NewDraft()
	if a.Method != "POST" || a.Auth.Mode != "none" || a.Profile.Mode != "open" || a.Profile.Steps[0] != (Step{60, 100}) || a.Profile.RetryAccounting != "within" || a.SelectionMode != "round_robin" || a.Capacity != 1000 || a.Samples != (Samples{5, 20, 65536}) {
		t.Fatal(a)
	}
	p := a.Policy
	if p.TimeoutMS != 30000 || p.MaxRetries != 0 || !p.Network || !p.Timeouts || p.DelayMode != "fixed" || p.DelayMS != 1000 || p.MaxDelayMS != 10000 || p.Jitter || p.DeadlineMS != 120000 || len(p.Statuses) != 2 {
		t.Fatal(p)
	}
	a.Profile.Steps[0].Level = 1
	a.Policy.Statuses[0].From = 1
	if b.Profile.Steps[0].Level != 100 || b.Policy.Statuses[0].From != 429 {
		t.Fatal("shared defaults")
	}
	t.Log("Approved defaults and independent ownership verified")
}

func TestHeaders(t *testing.T) {
	for _, name := range []string{"X-Token", "!#$%&'*+-.^_`|~", "Authorization"} {
		if e := ValidateHeader(name, "a\tb"); e != nil {
			t.Fatal(e)
		}
	}
	for _, name := range []string{"", "bad name", "é", "Content-Length", "Transfer-Encoding", "Trailer"} {
		if e := ValidateHeader(name, ""); e == nil {
			t.Fatal(name)
		}
	}
	for _, v := range []string{"\r", "\n", "\x00", "\x7f"} {
		if e := ValidateHeader("X", v); e == nil {
			t.Fatal("accepted control")
		}
	}
	t.Log("Shared HTTP syntax rejects controls and transport framing")
}
