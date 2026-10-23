package template

import (
	"bytes"
	"testing"
)

func TestTokenFailurePropagation(t *testing.T) {
	// Invalid source -> no program; exhausted entropy -> no body.
	for _, body := range []string{`["{{unknown}}"]`, `{"a":["{{sys.uuid"]}`, `[`, `{"a":}`} {
		if p, err := Compile([]byte(body)); err == nil || p != nil {
			t.Fatalf("accepted %s", body)
		}
	}
	zero := uint64(0)
	for _, body := range []string{`["{{sys.random.int(0,99)}}"]`, `{"a":"{{sys.random.choice([1,2])}}"}`, `["{{sys.request.seq}}"]`, `{"a":"{{sys.user.id}}"}`} {
		p, err := Compile([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		if out, err := p.Render(Context{Random: bytes.NewReader(nil), UserID: &zero}); err == nil || out != nil {
			t.Fatalf("partial output %s %s %v", body, out, err)
		}
	}
	t.Log("Nested syntax, entropy and identity failures return no request body")
}
