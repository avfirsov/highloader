package request

import (
	"encoding/base64"
	"fmt"
	"highloader/internal/domain"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

func validName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range []byte(s) {
		if c > 127 || !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c))) {
			return false
		}
	}
	return true
}

func applyLayer(out map[string]domain.EffectiveHeader, rows []domain.HeaderRow, source string) error {
	seen := map[string]bool{}
	for _, r := range rows {
		if !r.Enabled {
			continue
		}
		k := strings.ToLower(r.Name)
		if e := domain.ValidateHeader(r.Name, r.Value); e != nil {
			return e
		}
		if seen[k] {
			return fmt.Errorf("повторный заголовок %q в слое %s", r.Name, source)
		}
		seen[k] = true
		out[k] = domain.EffectiveHeader{Name: http.CanonicalHeaderKey(r.Name), Value: r.Value, Deleted: r.Suppress, Source: source, Locked: source == "file"}
	}
	return nil
}

func resolvePolicy(common domain.Policy, over domain.PolicyOverride) (domain.Policy, map[string]string, error) {
	p := common
	p.Statuses = append([]domain.StatusRange{}, common.Statuses...)
	sources := map[string]string{}
	dst := reflect.ValueOf(&p).Elem()
	src := reflect.ValueOf(over)
	typ := dst.Type()
	for i := 0; i < dst.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		sources[name] = "common"
		v := src.FieldByName(typ.Field(i).Name)
		if !v.IsNil() {
			dst.Field(i).Set(v.Elem())
			sources[name] = "private"
		}
	}
	p.Statuses = append([]domain.StatusRange{}, p.Statuses...)
	const maxMS = int64(math.MaxInt64 / 1000000)
	if p.TimeoutMS <= 0 || p.TimeoutMS > maxMS || p.DeadlineMS <= 0 || p.DeadlineMS > maxMS {
		return p, sources, fmt.Errorf("таймаут и общий deadline должны быть положительными и представимыми")
	}
	if p.MaxRetries < 0 || p.MaxRetries == math.MaxInt {
		return p, sources, fmt.Errorf("неверное число повторов")
	}
	if p.DelayMode != "fixed" && p.DelayMode != "exponential" {
		return p, sources, fmt.Errorf("неверный режим задержки")
	}
	if p.DelayMS < 0 || p.MaxDelayMS < 0 || p.DelayMS > maxMS || p.MaxDelayMS > maxMS || p.MaxDelayMS < p.DelayMS {
		return p, sources, fmt.Errorf("неверные границы задержки")
	}
	for _, r := range p.Statuses {
		if r.From < 100 || r.To > 599 || r.From > r.To || r.From <= 299 && r.To >= 200 {
			return p, sources, fmt.Errorf("диапазон повторов должен содержать HTTP 100–599, исключая 2xx")
		}
	}
	return p, sources, nil
}

func auth(a domain.Auth, u *url.URL) ([]domain.HeaderRow, error) {
	rows := []domain.HeaderRow{{Enabled: true, Name: "Content-Type", Value: "application/json"}}
	switch a.Mode {
	case "none":
	case "bearer":
		if a.Token == "" {
			return nil, fmt.Errorf("укажите Bearer token")
		}
		rows = append(rows, domain.HeaderRow{Enabled: true, Name: "Authorization", Value: "Bearer " + a.Token})
	case "basic":
		if strings.Contains(a.Username, ":") {
			return nil, fmt.Errorf("имя Basic Auth не может содержать двоеточие")
		}
		rows = append(rows, domain.HeaderRow{Enabled: true, Name: "Authorization", Value: "Basic " + base64.StdEncoding.EncodeToString([]byte(a.Username+":"+a.Password))})
	case "api_key":
		if a.Name == "" {
			return nil, fmt.Errorf("укажите имя API key")
		}
		switch a.Placement {
		case "header":
			if strings.EqualFold(a.Name, "Content-Type") {
				rows = rows[:0]
			}
			rows = append(rows, domain.HeaderRow{Enabled: true, Name: a.Name, Value: a.Value})
		case "query":
			q, e := url.ParseQuery(u.RawQuery)
			if e != nil {
				return nil, fmt.Errorf("неверная строка параметров URL: %w", e)
			}
			q.Set(a.Name, a.Value)
			u.RawQuery = q.Encode()
		default:
			return nil, fmt.Errorf("неверное размещение API key")
		}
	default:
		return nil, fmt.Errorf("неверный режим авторизации")
	}
	return rows, nil
}

func Resolve(d domain.Draft, s domain.TemplateSource) (domain.EffectiveRequest, error) {
	result := domain.EffectiveRequest{TemplateID: s.ID, Method: d.Method, MethodSource: "common"}
	u, e := url.Parse(d.URL)
	if e != nil || u == nil || !(u.Scheme == "http" || u.Scheme == "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return result, fmt.Errorf("требуется абсолютный HTTP/HTTPS URL без userinfo и фрагмента")
	}
	if _, e = url.ParseQuery(u.RawQuery); e != nil {
		return result, fmt.Errorf("неверные параметры URL")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return result, fmt.Errorf("порт URL должен быть от 1 до 65535")
		}
	}
	over, exists := d.Settings[s.ID]
	result.Weight = 1
	if exists {
		result.Weight = over.Weight
	}
	if over.Method != nil {
		result.Method = *over.Method
		result.MethodSource = "private"
	}
	if !validName(result.Method) {
		return result, fmt.Errorf("неверный HTTP метод")
	}
	if math.IsNaN(result.Weight) || math.IsInf(result.Weight, 0) || result.Weight <= 0 {
		return result, fmt.Errorf("вес должен быть конечным положительным числом")
	}
	generated, e := auth(d.Auth, u)
	if e != nil {
		return result, e
	}
	headers := map[string]domain.EffectiveHeader{}
	if e = applyLayer(headers, generated, "generated"); e != nil {
		return result, e
	}
	if e = applyLayer(headers, d.Headers, "common"); e != nil {
		return result, e
	}
	if e = applyLayer(headers, over.Headers, "private"); e != nil {
		return result, e
	}
	file := make([]domain.HeaderRow, 0, len(s.Headers))
	for _, h := range s.Headers {
		r := domain.HeaderRow{Enabled: true, Name: h.Name, Suppress: h.Value == nil}
		if h.Value != nil {
			r.Value = *h.Value
		}
		file = append(file, r)
	}
	if e = applyLayer(headers, file, "file"); e != nil {
		return result, e
	}
	result.Headers = []domain.EffectiveHeader{}
	for _, h := range headers {
		result.Headers = append(result.Headers, h)
	}
	sort.Slice(result.Headers, func(i, j int) bool { return result.Headers[i].Name < result.Headers[j].Name })
	result.URL = u.String()
	result.Policy, result.PolicySources, e = resolvePolicy(d.Policy, over.Policy)
	return result, e
}
