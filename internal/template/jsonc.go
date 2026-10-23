package template

import (
	"bytes"
	"encoding/json"
	"fmt"
	"highloader/internal/domain"
	"io"
	"strings"
	"unicode/utf8"
)

type Parsed struct {
	Body    json.RawMessage
	Headers []domain.FileHeader
}

func decode(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, e := readValue(d)
	if e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, fmt.Errorf("после JSON обнаружены лишние данные (байт %d)", d.InputOffset())
	}
	return v, nil
}

func readValue(d *json.Decoder) (any, error) {
	t, e := d.Token()
	if e != nil {
		return nil, e
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return t, nil
	}
	switch delim {
	case '{':
		out := map[string]any{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return nil, e
			}
			key, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("ключ объекта должен быть строкой")
			}
			if _, ok := out[key]; ok {
				return nil, fmt.Errorf("повторный ключ %q (байт %d)", key, d.InputOffset())
			}
			v, e := readValue(d)
			if e != nil {
				return nil, e
			}
			out[key] = v
		}
		_, e = d.Token()
		return out, e
	case '[':
		out := []any{}
		for d.More() {
			v, e := readValue(d)
			if e != nil {
				return nil, e
			}
			out = append(out, v)
		}
		_, e = d.Token()
		return out, e
	default:
		return nil, fmt.Errorf("неожиданный разделитель %q", delim)
	}
}

func lex(raw []byte) ([]byte, []byte, error) {
	out := append([]byte(nil), raw...)
	var metadata []byte
	seenBody := false
	for i := 0; i < len(out); {
		if out[i] == '"' {
			seenBody = true
			i++
			closed := false
			for i < len(out) {
				if out[i] == '\\' {
					i += 2
					continue
				}
				if out[i] == '"' {
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, nil, fmt.Errorf("незавершённая строка")
			}
			continue
		}
		if out[i] == '/' && i+1 < len(out) && (out[i+1] == '/' || out[i+1] == '*') {
			start := i
			block := out[i+1] == '*'
			i += 2
			contentStart := i
			if block {
				for i+1 < len(out) && !(out[i] == '*' && out[i+1] == '/') {
					i++
				}
				if i+1 >= len(out) {
					return nil, nil, fmt.Errorf("незавершённый комментарий (байт %d)", start)
				}
				content := strings.TrimSpace(string(out[contentStart:i]))
				if strings.HasPrefix(content, "@highloader") {
					if seenBody || metadata != nil {
						return nil, nil, fmt.Errorf("@highloader допускается один раз перед телом (байт %d)", start)
					}
					metadata = []byte(strings.TrimSpace(strings.TrimPrefix(content, "@highloader")))
				}
				i += 2
			} else {
				for i < len(out) && out[i] != '\n' {
					i++
				}
			}
			for j := start; j < i; j++ {
				if out[j] != '\n' && out[j] != '\r' {
					out[j] = ' '
				}
			}
			continue
		}
		if !bytes.ContainsRune([]byte(" \t\r\n"), rune(out[i])) {
			seenBody = true
		}
		i++
	}
	for i := 0; i < len(out); i++ {
		if out[i] == '"' {
			i++
			for i < len(out) {
				if out[i] == '\\' {
					i += 2
					continue
				}
				if out[i] == '"' {
					break
				}
				i++
			}
			continue
		}
		if out[i] == ',' {
			j := i + 1
			for j < len(out) && bytes.ContainsRune([]byte(" \t\r\n"), rune(out[j])) {
				j++
			}
			if j < len(out) && (out[j] == ']' || out[j] == '}') {
				previous := i - 1
				for previous >= 0 && bytes.ContainsRune([]byte(" \t\r\n"), rune(out[previous])) {
					previous--
				}
				if previous >= 0 && !bytes.ContainsRune([]byte("[{,:"), rune(out[previous])) {
					out[i] = ' '
				}
			}
		}
	}
	return out, metadata, nil
}

func Parse(raw []byte) (Parsed, error) {
	if !utf8.Valid(raw) {
		return Parsed{}, fmt.Errorf("источник должен быть UTF-8")
	}
	raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	body, meta, e := lex(raw)
	if e != nil {
		return Parsed{}, e
	}
	v, e := decode(body)
	if e != nil {
		return Parsed{}, fmt.Errorf("тело: %w", e)
	}
	result := Parsed{Headers: []domain.FileHeader{}}
	result.Body, e = json.Marshal(v)
	if e != nil {
		return Parsed{}, e
	}
	if meta != nil {
		m, e := decode(meta)
		if e != nil {
			return Parsed{}, fmt.Errorf("@highloader: %w", e)
		}
		obj, ok := m.(map[string]any)
		if !ok {
			return Parsed{}, fmt.Errorf("@highloader должен быть объектом")
		}
		for key := range obj {
			if key != "headers" {
				return Parsed{}, fmt.Errorf("неизвестное поле @highloader.%s", key)
			}
		}
		if h, exists := obj["headers"]; exists {
			headers, ok := h.(map[string]any)
			if !ok {
				return Parsed{}, fmt.Errorf("@highloader.headers должен быть объектом")
			}
			seen := map[string]bool{}
			for name, val := range headers {
				key := strings.ToLower(name)
				if seen[key] {
					return Parsed{}, fmt.Errorf("повторное имя заголовка %q", name)
				}
				seen[key] = true
				var p *string
				if val != nil {
					s, ok := val.(string)
					if !ok {
						return Parsed{}, fmt.Errorf("заголовок %q должен быть строкой или null", name)
					}
					p = &s
				}
				value := ""
				if p != nil {
					value = *p
				}
				if e := domain.ValidateHeader(name, value); e != nil {
					return Parsed{}, fmt.Errorf("@highloader.headers: %w", e)
				}
				result.Headers = append(result.Headers, domain.FileHeader{Name: name, Value: p})
			}
		}
	}
	return result, nil
}
