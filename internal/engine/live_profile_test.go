package engine

import (
	"encoding/json"
	"testing"
)

func TestLiveProfileOwnership(t *testing.T) {
	for _, mode := range []string{"open", "closed"} {
		for _, accounting := range []string{"above", "within"} {
			t.Run(mode+"/"+accounting, func(t *testing.T) {
				s := fixture()
				s.Draft.Profile.Mode, s.Draft.Profile.RetryAccounting = mode, accounting
				r, _, _ := controlled(t, s)
				s.Draft.Profile.Mode, s.Draft.Profile.RetryAccounting = "different", "different"
				for _, state := range []string{"preparing", "running", "completed"} {
					if state != "preparing" {
						r.state = state
						r.publish()
					}
					b, err := json.Marshal(r.View())
					if err != nil {
						t.Fatal(err)
					}
					var wire map[string]any
					if err = json.Unmarshal(b, &wire); err != nil {
						t.Fatal(err)
					}
					if wire["mode"] != mode || wire["retryAccounting"] != accounting {
						t.Fatalf("%s lost owning profile: mode=%v accounting=%v", state, wire["mode"], wire["retryAccounting"])
					}
				}
				t.Log("Initial, running and final wire views retain frozen mode/accounting")
			})
		}
	}
}
