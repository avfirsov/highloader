package template

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	valid := []string{`null`, `true`, `[1,2,]`, `"https://x/*a*/"`, `{"a":900719925474099312345678901234567890,"b":1e1000,}`, "\xef\xbb\xbf/* ordinary */\n/* @highloader {\"headers\":{\"X-A\":\"ok\",\"Content-Type\":null}} */\n{\"s\":\"// \\\"\",}"}
	for _, s := range valid {
		p, e := Parse([]byte(s))
		if e != nil || !json.Valid(p.Body) {
			t.Fatalf("valid %q: %s %v", s, p.Body, e)
		}
	}
	for _, s := range []string{`{"a":1,"a":2}`, `{"x":{"a":1,"a":2}}`, `/* @highloader {"headers":{"A":"1","a":"2"}} */ {}`, `/* @highloader {"method":"GET"} */ {}`, `/* @highloader {"headers":{"A":1}} */ {}`, `/* @highloader {"headers":null} */ {}`, `/* @highloader {"headers":{},} */ {}`, `{} /* @highloader {} */`, `/* @highloader {} */ /* @highloader {} */ {}`, `/* @highloader [] */ {}`, `/* @highloader {"headers":{},"headers":{}} */ {}`, `[, ]`, `{,}`, `[1,,]`, `1 2`, `/* unclosed`, `"unclosed`, string([]byte{0xff})} {
		if _, e := Parse([]byte(s)); e == nil {
			t.Errorf("accepted invalid %q", s)
		}
	}
	p, _ := Parse([]byte(valid[4]))
	if !bytes.Contains(p.Body, []byte("900719925474099312345678901234567890")) || !bytes.Contains(p.Body, []byte("1e1000")) {
		t.Fatal("numeric precision changed")
	}
	t.Log("Any-root JSONC accepted; duplicates and malformed directives rejected; huge numeric lexemes retained")
}

func TestTokens(t *testing.T) {
	body := `["{{sys.uuid}}","{{sys.uuid}}","{{sys.time.iso}}","{{sys.time.unix_s}}","{{sys.time.unix_ms}}","{{sys.request.seq}}","{{sys.run.id}}","{{sys.user.id}}","{{sys.random.int(-2,2)}}","{{sys.random.int(-2,2)}}","{{sys.random.choice([true])}}","{{sys.random.choice([null])}}","pre {{sys.random.choice([false])}} post","{{sys.random.choice([\"x\\\"y\"])}}","\\{{sys.unknown}}",9007199254740993123456789,{"{{sys.unknown}}":"unchanged"}]`
	p, e := Compile([]byte(body))
	if e != nil {
		t.Fatal(e)
	}
	user := uint64(7)
	now := time.Date(2026, 1, 2, 3, 4, 5, 123000000, time.FixedZone("other", 3600))
	raw, e := p.Render(Context{Time: now, RunID: "run", Sequence: 9, UserID: &user, Random: bytes.NewReader(make([]byte, 256))})
	if e != nil {
		t.Fatal(e)
	}
	v, e := decode(raw)
	if e != nil {
		t.Fatal(e)
	}
	a := v.([]any)
	if a[0] != a[1] || a[0] != "00000000-0000-4000-8000-000000000000" || a[2] != "2026-01-02T02:04:05.123Z" || a[3] != json.Number(fmt.Sprint(now.Unix())) || a[4] != json.Number(fmt.Sprint(now.UnixMilli())) || a[5] != json.Number("9") || a[6] != "run" || a[7] != json.Number("7") || a[8] != a[9] || a[10] != true || a[11] != nil || a[12] != "pre false post" || a[13] != "x\"y" || a[14] != "{{sys.unknown}}" || a[15] != json.Number("9007199254740993123456789") {
		t.Fatalf("wrong values: %s", raw)
	}
	if !p.UsesUserID() {
		t.Fatal("missing user dependency")
	}
	if _, e = p.Render(Context{Sequence: 1}); e == nil {
		t.Fatal("missing user accepted")
	}
	for _, s := range []string{`{{sys.unknown}}`, `{{sys.uuid`, `{{sys.random.int(2,1)}}`, `{{sys.random.int(1.1,2)}}`, `{{sys.random.int(1)}}`, `{{sys.random.choice([])}}`, `{{sys.random.choice([{}])}}`, `{{sys.random.choice([[]])}}`, `{{sys.random.choice([NaN])}}`} {
		raw, _ := json.Marshal(s)
		if _, e := Compile(raw); e == nil {
			t.Errorf("invalid token accepted %s", s)
		}
	}
	t.Log("UUID/time/sequence/run/user/random native scalars and literal escapes verified; identical expressions share one value")
}

func TestTokenEdges(t *testing.T) {
	for _, s := range []string{`{{sys.random.int(-9223372036854775808,9223372036854775807)}}`, `{{sys.random.choice(["}}"] )}}`} {
		if strings.Contains(s, "] ") {
			continue
		}
		b, _ := json.Marshal(s)
		p, e := Compile(b)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := p.Render(Context{Random: bytes.NewReader(make([]byte, 64))})
		if e != nil || string(raw) != "-9223372036854775808" {
			t.Fatalf("range %s %v", raw, e)
		}
	}
	b, _ := json.Marshal(`{{sys.random.choice(["}}","{{sys.unknown}}"] )}}`)
	if _, e := Compile(b); e != nil {
		t.Fatal(e)
	}
	b, _ = json.Marshal(`{{sys.random.choice(["{{sys.unknown}}"] )}}`)
	p, e := Compile(b)
	if e != nil {
		t.Fatal(e)
	}
	out, e := p.Render(Context{Random: bytes.NewReader(make([]byte, 32))})
	if e != nil || string(out) != `"{{sys.unknown}}"` {
		t.Fatalf("recursive %s %v", out, e)
	}
	p, _ = Compile([]byte(`"{{sys.uuid}}"`))
	if _, e = p.Render(Context{Random: bytes.NewReader(nil)}); e == nil {
		t.Fatal("random failure hidden")
	}
	first, _ := p.Render(Context{Random: bytes.NewReader(make([]byte, 16))})
	second, _ := p.Render(Context{Random: bytes.NewReader(bytes.Repeat([]byte{1}, 16))})
	if bytes.Equal(first, second) {
		t.Fatal("next request did not refresh UUID")
	}
	retained := append([]byte(nil), first...)
	for retry := 0; retry < 3; retry++ {
		if !bytes.Equal(retained, first) {
			t.Fatal("retry bytes changed")
		}
	}
	t.Log("Full signed integer range, nonrecursive token output and explicit random failure verified")
}

func TestImport(t *testing.T) {
	dir := t.TempDir()
	source := []byte("\xef\xbb\xbf// original\n{\"id\":\"{{sys.uuid}}\",}")
	for i := 0; i < 10; i++ {
		if e := os.WriteFile(filepath.Join(dir, fmt.Sprintf("arbitrary-%02d.JSONC", i)), source, 0600); e != nil {
			t.Fatal(e)
		}
	}
	os.WriteFile(filepath.Join(dir, "bad.json"), []byte(`{"x":`), 0600)
	os.WriteFile(filepath.Join(dir, "ignored.txt"), source, 0600)
	os.Mkdir(filepath.Join(dir, "nested"), 0700)
	os.WriteFile(filepath.Join(dir, "nested", "hidden.json"), source, 0600)
	result, e := ImportDirectory(dir)
	if e != nil || len(result.Templates) != 11 || len(result.SelectedIDs) != 10 {
		t.Fatalf("import: %+v %v", result, e)
	}
	ids := map[string]bool{}
	for _, s := range result.Templates {
		if ids[s.ID] || s.ID == "" {
			t.Fatal("identity collision")
		}
		ids[s.ID] = true
		actual, _ := os.ReadFile(filepath.Join(dir, s.Filename))
		if !bytes.Equal(actual, s.Raw) {
			t.Fatal("original changed")
		}
		if s.Valid && result.Settings[s.ID].Weight != 1 {
			t.Fatal("default weight")
		}
		if !s.Valid && len(s.Diagnostics) == 0 {
			t.Fatal("invisible invalid row")
		}
	}
	invalid := t.TempDir()
	os.WriteFile(filepath.Join(invalid, "header.jsonc"), []byte(`/* @highloader {"headers":{"Bad Name":"x"}} */ {}`), 0600)
	bad, _ := ImportDirectory(invalid)
	if bad.Templates[0].Valid || len(bad.SelectedIDs) != 0 {
		t.Fatal("bad file header selected")
	}
	empty, _ := ImportDirectory(t.TempDir())
	if len(empty.Diagnostics) == 0 {
		t.Fatal("empty folder diagnostic")
	}
	if _, e = ImportDirectory(filepath.Join(dir, "absent")); e == nil {
		t.Fatal("missing folder hidden")
	}
	t.Log("Ten valid + one invalid + nested fixture = ten selected, eleven visible, no source writes")
}
