package template

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Context struct {
	Time     time.Time
	RunID    string
	Sequence uint64
	UserID   *uint64
	Random   io.Reader
}
type expression struct {
	name     string
	min, max int64
	choices  []any
}
type part struct {
	text  string
	token bool
}
type compiledString []part
type Program struct {
	root        any
	expressions map[string]expression
	usesUser    bool
}

func split(s string) (compiledString, error) {
	out := compiledString{}
	var literal strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], "\\{{") {
			literal.WriteString("{{")
			i += 3
			continue
		}
		if !strings.HasPrefix(s[i:], "{{") {
			literal.WriteByte(s[i])
			i++
			continue
		}
		if literal.Len() > 0 {
			out = append(out, part{text: literal.String()})
			literal.Reset()
		}
		start := i
		i += 2
		quoted := false
		for i < len(s) {
			if s[i] == '\\' && quoted {
				i += 2
				continue
			}
			if s[i] == '"' {
				quoted = !quoted
			}
			if !quoted && strings.HasPrefix(s[i:], "}}") {
				break
			}
			i++
		}
		if i >= len(s) {
			return nil, fmt.Errorf("незавершённый токен в позиции %d", start)
		}
		out = append(out, part{text: s[start+2 : i], token: true})
		i += 2
	}
	if literal.Len() > 0 || len(out) == 0 {
		out = append(out, part{text: literal.String()})
	}
	return out, nil
}

func parseExpression(s string) (expression, error) {
	e := expression{name: s}
	switch s {
	case "sys.uuid", "sys.time.iso", "sys.time.unix_s", "sys.time.unix_ms", "sys.request.seq", "sys.run.id", "sys.user.id":
		return e, nil
	}
	if strings.HasPrefix(s, "sys.random.int(") && strings.HasSuffix(s, ")") {
		args := strings.Split(s[len("sys.random.int("):len(s)-1], ",")
		if len(args) != 2 {
			return e, fmt.Errorf("random.int требует две целочисленные границы")
		}
		var err error
		e.min, err = strconv.ParseInt(strings.TrimSpace(args[0]), 10, 64)
		if err != nil {
			return e, fmt.Errorf("random.int: неверная нижняя граница")
		}
		e.max, err = strconv.ParseInt(strings.TrimSpace(args[1]), 10, 64)
		if err != nil || e.min > e.max {
			return e, fmt.Errorf("random.int: неверный диапазон")
		}
		e.name = "random.int"
		return e, nil
	}
	if strings.HasPrefix(s, "sys.random.choice(") && strings.HasSuffix(s, ")") {
		v, err := decode([]byte(s[len("sys.random.choice(") : len(s)-1]))
		if err != nil {
			return e, fmt.Errorf("random.choice: %w", err)
		}
		a, ok := v.([]any)
		if !ok || len(a) == 0 {
			return e, fmt.Errorf("random.choice требует непустой массив скаляров")
		}
		for _, v := range a {
			switch v.(type) {
			case nil, string, bool, json.Number:
			default:
				return e, fmt.Errorf("random.choice принимает только скаляры")
			}
		}
		e.name = "random.choice"
		e.choices = a
		return e, nil
	}
	return e, fmt.Errorf("неизвестный или неверный токен {{%s}}", s)
}

func compileValue(v any, p *Program) (any, error) {
	switch x := v.(type) {
	case string:
		parts, err := split(x)
		if err != nil {
			return nil, err
		}
		for _, part := range parts {
			if part.token {
				e, err := parseExpression(part.text)
				if err != nil {
					return nil, err
				}
				p.expressions[part.text] = e
				if e.name == "sys.user.id" {
					p.usesUser = true
				}
			}
		}
		return parts, nil
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			r, e := compileValue(v, p)
			if e != nil {
				return nil, e
			}
			out[i] = r
		}
		return out, nil
	case map[string]any:
		out := map[string]any{}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := x[k]
			r, e := compileValue(v, p)
			if e != nil {
				return nil, fmt.Errorf("поле %s: %w", k, e)
			}
			out[k] = r
		}
		return out, nil
	default:
		return v, nil
	}
}

func Compile(body json.RawMessage) (*Program, error) {
	v, e := decode(body)
	if e != nil {
		return nil, e
	}
	p := &Program{expressions: map[string]expression{}}
	p.root, e = compileValue(v, p)
	if e != nil {
		return nil, e
	}
	return p, nil
}

func (p *Program) UsesUserID() bool { return p.usesUser }

func evaluate(e expression, c Context) (any, error) {
	switch e.name {
	case "sys.uuid":
		b := make([]byte, 16)
		if _, err := io.ReadFull(c.Random, b); err != nil {
			return nil, err
		}
		b[6] = (b[6] & 15) | 64
		b[8] = (b[8] & 63) | 128
		return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
	case "sys.time.iso":
		return c.Time.UTC().Format("2006-01-02T15:04:05.000Z"), nil
	case "sys.time.unix_s":
		return json.Number(strconv.FormatInt(c.Time.Unix(), 10)), nil
	case "sys.time.unix_ms":
		return json.Number(strconv.FormatInt(c.Time.UnixMilli(), 10)), nil
	case "sys.request.seq":
		if c.Sequence == 0 {
			return nil, fmt.Errorf("sequence должна начинаться с 1")
		}
		return json.Number(strconv.FormatUint(c.Sequence, 10)), nil
	case "sys.run.id":
		return c.RunID, nil
	case "sys.user.id":
		if c.UserID == nil || *c.UserID == 0 {
			return nil, fmt.Errorf("sys.user.id требует номер виртуального пользователя")
		}
		return json.Number(strconv.FormatUint(*c.UserID, 10)), nil
	case "random.int":
		span := new(big.Int).Sub(big.NewInt(e.max), big.NewInt(e.min))
		span.Add(span, big.NewInt(1))
		n, err := rand.Int(c.Random, span)
		if err != nil {
			return nil, err
		}
		n.Add(n, big.NewInt(e.min))
		return json.Number(n.String()), nil
	case "random.choice":
		n, err := rand.Int(c.Random, big.NewInt(int64(len(e.choices))))
		if err != nil {
			return nil, err
		}
		return e.choices[n.Int64()], nil
	}
	return nil, fmt.Errorf("нарушен контракт скомпилированного токена")
}

func renderValue(v any, p *Program, c Context, cache map[string]any) (any, error) {
	switch x := v.(type) {
	case compiledString:
		var text strings.Builder
		for _, part := range x {
			if !part.token {
				text.WriteString(part.text)
				continue
			}
			value, ok := cache[part.text]
			if !ok {
				var e error
				value, e = evaluate(p.expressions[part.text], c)
				if e != nil {
					return nil, e
				}
				cache[part.text] = value
			}
			if len(x) == 1 {
				return value, nil
			}
			if s, ok := value.(string); ok {
				text.WriteString(s)
			} else {
				b, e := json.Marshal(value)
				if e != nil {
					return nil, e
				}
				text.Write(b)
			}
		}
		return text.String(), nil
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			r, e := renderValue(v, p, c, cache)
			if e != nil {
				return nil, e
			}
			out[i] = r
		}
		return out, nil
	case map[string]any:
		out := map[string]any{}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := x[k]
			r, e := renderValue(v, p, c, cache)
			if e != nil {
				return nil, e
			}
			out[k] = r
		}
		return out, nil
	default:
		return v, nil
	}
}

func (p *Program) Render(c Context) ([]byte, error) {
	if c.Random == nil {
		c.Random = rand.Reader
	}
	v, e := renderValue(p.root, p, c, map[string]any{})
	if e != nil {
		return nil, e
	}
	return json.Marshal(v)
}
