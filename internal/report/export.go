package report

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"highloader/internal/template"
	"net/url"
	"sort"
	"strings"
)

const removed = "[REDACTED]"

type secretSet struct {
	values map[string]bool
	names  map[string]bool
}

func sensitive(k string) bool {
	k = strings.ToLower(k)
	return k == "authorization" || k == "proxy-authorization" || k == "cookie" || k == "set-cookie" || strings.Contains(k, "api-key") || strings.Contains(k, "apikey") || strings.Contains(k, "api_key") || k == "password" || k == "token" || k == "access_token"
}

func Export(data any, includeAuthorization bool, js, css []byte) ([]byte, error) {
	if len(bytes.TrimSpace(js)) == 0 || len(bytes.TrimSpace(css)) == 0 {
		return nil, errors.New("offline report assets are missing; build web/report-dist")
	}
	if !exportComplete(data) {
		return nil, errors.New("export requires complete run reports")
	}
	raw, e := json.Marshal(data)
	if e != nil {
		return nil, e
	}
	if !includeAuthorization {
		var tree any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if e = dec.Decode(&tree); e != nil {
			return nil, e
		}
		s := secretSet{map[string]bool{}, map[string]bool{}}
		discover(tree, &s)
		discover(tree, &s)
		tree = redact(tree, s, "")
		raw, e = json.Marshal(tree)
		if e != nil {
			return nil, e
		}
	}
	// encoding/json escapes HTML delimiters and U+2028/U+2029 in inert data.
	if bytes.Contains(bytes.ToLower(js), []byte("</script")) || bytes.Contains(bytes.ToLower(css), []byte("</style")) {
		return nil, errors.New("unsafe closing tag in offline assets")
	}
	out := []byte("<!doctype html><html lang=\"ru\"><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>Highloader</title><style>")
	out = append(out, css...)
	out = append(out, []byte("</style><body><div id=\"root\"></div><script type=\"application/json\" id=\"highloader-report-data\">")...)
	out = append(out, raw...)
	out = append(out, []byte("</script><script>")...)
	out = append(out, js...)
	out = append(out, []byte("</script></body></html>")...)
	return out, nil
}

func discover(v any, s *secretSet) {
	switch n := v.(type) {
	case map[string]any:
		if _, ok := n["mode"].(string); ok && (n["token"] != nil || n["password"] != nil || n["username"] != nil || n["placement"] != nil) {
			for _, k := range []string{"token", "username", "password", "value"} {
				if x, ok := n[k].(string); ok && x != "" {
					s.values[x] = true
				}
			}
			if name, ok := n["name"].(string); ok && name != "" {
				s.names[strings.ToLower(name)] = true
			}
			u, _ := n["username"].(string)
			p, _ := n["password"].(string)
			if u != "" || p != "" {
				s.values[base64.StdEncoding.EncodeToString([]byte(u+":"+p))] = true
			}
		}
		if name, ok := n["name"].(string); ok && (sensitive(name) || s.names[strings.ToLower(name)]) {
			if val, ok := n["value"].(string); ok && val != "" {
				addSecret(val, s)
				for _, part := range strings.Fields(val) {
					if part != "Bearer" && part != "Basic" {
						s.values[part] = true
					}
				}
			}
		}
		for k, x := range n {
			if strings.EqualFold(k, "raw") {
				if encoded, ok := x.(string); ok {
					if raw, err := base64.StdEncoding.DecodeString(encoded); err == nil {
						if parsed, err := template.Parse(raw); err == nil {
							for _, h := range parsed.Headers {
								if h.Value != nil && (sensitive(h.Name) || s.names[strings.ToLower(h.Name)]) {
									addSecret(*h.Value, s)
								}
							}
							var body any
							d := json.NewDecoder(bytes.NewReader(parsed.Body))
							d.UseNumber()
							if d.Decode(&body) == nil {
								discover(body, s)
							}
						}
					}
				}
			}
			if sensitive(k) || s.names[strings.ToLower(k)] {
				switch val := x.(type) {
				case string:
					if val != "" {
						addSecret(val, s)
					}
				case []any:
					for _, entry := range val {
						if val, ok := entry.(string); ok && val != "" {
							addSecret(val, s)
						}
					}
				}
			}
			discover(x, s)
		}
	case []any:
		for _, x := range n {
			discover(x, s)
		}
	}
}

func redact(v any, s secretSet, key string) any {
	switch n := v.(type) {
	case map[string]any:
		name, _ := n["name"].(string)
		header := sensitive(name) || s.names[strings.ToLower(name)]
		for k, x := range n {
			lower := strings.ToLower(k)
			if lower == "auth" {
				n[k] = map[string]any{"mode": "redacted", "redacted": true}
				continue
			}
			if sensitive(k) || s.names[lower] || header && lower == "value" {
				n[k] = removed
				continue
			}
			if lower == "raw" {
				if encoded, ok := x.(string); ok {
					safe, changed := redactSource(encoded, s)
					n[k] = safe
					if changed {
						n["rawRedacted"] = true
						n["rawFormatting"] = "normalized after credential redaction"
					}
				}
				continue
			}
			if lower == "data" && len(s.values) > 0 {
				if truncated, _ := n["Truncated"].(bool); truncated {
					n[k] = base64.StdEncoding.EncodeToString([]byte(removed))
					n["redacted"] = true
					continue
				}
			}
			n[k] = redact(x, s, k)
		}
		return n
	case []any:
		for i, x := range n {
			n[i] = redact(x, s, key)
		}
		return n
	case string:
		if strings.EqualFold(key, "data") {
			if decoded, e := base64.StdEncoding.DecodeString(n); e == nil {
				return base64.StdEncoding.EncodeToString([]byte(scrub(string(decoded), s)))
			}
		}
		return scrub(n, s)
	default:
		return v
	}
}

func scrub(value string, s secretSet) string {
	if u, e := url.Parse(value); e == nil && u.Scheme != "" {
		q := u.Query()
		changed := false
		for k := range q {
			if sensitive(k) || s.names[strings.ToLower(k)] {
				q.Set(k, removed)
				changed = true
			}
		}
		if u.User != nil {
			u.User = url.User(removed)
			changed = true
		}
		if changed {
			u.RawQuery = q.Encode()
			value = u.String()
		}
	}
	secrets := []string{}
	for x := range s.values {
		if x != "" {
			secrets = append(secrets, x)
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, x := range secrets {
		for _, form := range []string{x, url.QueryEscape(x), url.PathEscape(x), base64.StdEncoding.EncodeToString([]byte(x))} {
			value = strings.ReplaceAll(value, form, removed)
		}
	}
	return value
}

func exportComplete(data any) bool {
	switch r := data.(type) {
	case RunReport:
		return r.TemplateID == "" && (r.Range.FromIndex == "" || r.Range.FromIndex == "0") && r.Range.NextIndex == nil && r.Range.NextSampleOffset == nil && r.Range.TotalSamples == decimal(uint64(len(r.Samples))) && r.Range.TotalBuckets == decimal(uint64(len(r.Points)))
	case Comparison:
		return exportComplete(r.Runs[0]) && exportComplete(r.Runs[1])
	default:
		return false
	}
}

func addSecret(value string, s *secretSet) {
	if value == "" {
		return
	}
	s.values[value] = true
	parts := strings.Fields(value)
	if len(parts) == 2 && (strings.EqualFold(parts[0], "Basic") || strings.EqualFold(parts[0], "Bearer")) {
		s.values[parts[1]] = true
		if strings.EqualFold(parts[0], "Basic") {
			if b, e := base64.StdEncoding.DecodeString(parts[1]); e == nil {
				s.values[string(b)] = true
				for _, part := range strings.SplitN(string(b), ":", 2) {
					if part != "" {
						s.values[part] = true
					}
				}
			}
		}
	}
}

func redactSource(encoded string, s secretSet) (string, bool) {
	raw, e := base64.StdEncoding.DecodeString(encoded)
	if e != nil {
		return base64.StdEncoding.EncodeToString([]byte(removed)), true
	}
	parsed, e := template.Parse(raw)
	if e != nil {
		if scrub(string(raw), s) == string(raw) && !containsCredentialLabel(string(raw), s) {
			return encoded, false
		}
		return base64.StdEncoding.EncodeToString([]byte(removed)), true
	}
	var body any
	dec := json.NewDecoder(bytes.NewReader(parsed.Body))
	dec.UseNumber()
	if dec.Decode(&body) != nil {
		return base64.StdEncoding.EncodeToString([]byte(removed)), true
	}
	before, _ := json.Marshal(body)
	body = redact(body, s, "")
	after, _ := json.Marshal(body)
	changed := !bytes.Equal(before, after) || scrub(string(raw), s) != string(raw)
	headers := map[string]any{}
	for _, h := range parsed.Headers {
		if h.Value == nil {
			headers[h.Name] = nil
			continue
		}
		v := *h.Value
		if sensitive(h.Name) || s.names[strings.ToLower(h.Name)] {
			v = removed
		}
		v = scrub(v, s)
		headers[h.Name] = v
		if v != *h.Value {
			changed = true
		}
	}
	if !changed {
		return encoded, false
	}
	out := after
	if len(headers) > 0 {
		meta, _ := json.Marshal(map[string]any{"headers": headers})
		out = append(append([]byte("/* @highloader "), meta...), []byte(" */\n")...)
		out = append(out, after...)
	}
	return base64.StdEncoding.EncodeToString(out), true
}

func containsCredentialLabel(raw string, s secretSet) bool {
	raw = strings.ToLower(raw)
	for _, key := range []string{"authorization", "password", "token", "api-key", "api_key", "apikey"} {
		if strings.Contains(raw, key) {
			return true
		}
	}
	for key := range s.names {
		if strings.Contains(raw, key) {
			return true
		}
	}
	return false
}
