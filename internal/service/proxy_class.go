package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/b0balYson/dropsite/internal/domain"
)

// proxyPolicy is the built-in policy of an endpoint class. DailyBudget 0
// means no budget.
type proxyPolicy struct {
	PerMinute    int
	DailyBudget  int
	SessionQuota int
}

// Class policies (change abuse-protection). Owners may override each value
// within the bounds below.
var proxyPolicies = map[domain.ProxyClass]proxyPolicy{
	domain.ProxyClassRead:        {PerMinute: 60, DailyBudget: 10000},
	domain.ProxyClassAI:          {PerMinute: 20, DailyBudget: 1000, SessionQuota: 50},
	domain.ProxyClassSend:        {PerMinute: 5, DailyBudget: 100, SessionQuota: 50},
	domain.ProxyClassTransparent: {PerMinute: ProxyCallsPerIP},
}

// Bounds of owner overrides and of request templates.
const (
	proxyMaxPerMinute     = 600
	proxyMaxDailyBudget   = 1_000_000
	proxyMaxSessionQuota  = 1000
	proxyMaxTemplateBytes = 16 << 10
	proxyMaxFields        = 20
	proxyDefaultStrLen    = 1000
	proxyMaxStrLen        = 20000
	proxyDefaultMsgLen    = 4000
	proxyDefaultMsgItems  = 20
	proxyMaxMsgItems      = 100
	proxyMaxResponsePath  = 200
)

var (
	proxyFieldNameRe   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	proxyPlaceholderRe = regexp.MustCompile(`\{\{\s*([^{}]*?)\s*\}\}`)
	proxyRespSegRe     = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	proxyTemplateVerbs = []string{"POST", "PUT", "PATCH"}
)

// ProxyEffectiveLimits returns the limits of e with the class defaults
// filled in.
func ProxyEffectiveLimits(c domain.ProxyConfig) domain.ProxyLimits {
	p := proxyPolicies[c.Class]
	out := domain.ProxyLimits{PerMinute: p.PerMinute, DailyBudget: p.DailyBudget, SessionQuota: p.SessionQuota}
	if c.Limits.PerMinute > 0 {
		out.PerMinute = c.Limits.PerMinute
	}
	if c.Limits.DailyBudget > 0 {
		out.DailyBudget = c.Limits.DailyBudget
	}
	if c.Limits.SessionQuota > 0 && c.Class.Templated() {
		out.SessionQuota = c.Limits.SessionQuota
	}
	return out
}

func normalizeProxyLimits(l domain.ProxyLimits, class domain.ProxyClass) (domain.ProxyLimits, error) {
	if l.PerMinute < 0 || l.PerMinute > proxyMaxPerMinute {
		return l, proxyInvalid(fmt.Sprintf("per_minute must be 0 (class default) to %d", proxyMaxPerMinute))
	}
	if l.DailyBudget < 0 || l.DailyBudget > proxyMaxDailyBudget {
		return l, proxyInvalid(fmt.Sprintf("daily_budget must be 0 (class default) to %d", proxyMaxDailyBudget))
	}
	if l.SessionQuota < 0 || l.SessionQuota > proxyMaxSessionQuota {
		return l, proxyInvalid(fmt.Sprintf("session_quota must be 0 (class default) to %d", proxyMaxSessionQuota))
	}
	if !class.Templated() {
		l.SessionQuota = 0
	}
	return l, nil
}

var errProxyClass = proxyInvalid("class must be ai (a model API with a request template), send (a message to a " +
	"fixed recipient with a template), read (GET-only data) or transparent (any allowed call; only for " +
	"password-protected sites)")

// applyProxyClass validates the class part of out (methods, paths and
// template follow from the class).
func applyProxyClass(out *domain.ProxyConfig, c domain.ProxyConfig) error {
	out.Class = domain.ProxyClass(strings.ToLower(strings.TrimSpace(string(c.Class))))
	if !out.Class.Valid() {
		return errProxyClass
	}
	var err error
	if out.Limits, err = normalizeProxyLimits(c.Limits, out.Class); err != nil {
		return err
	}
	switch {
	case out.Class.Templated():
		if c.Template == nil {
			return proxyInvalid("class " + string(out.Class) + " needs a request template: the fixed body with " +
				"{{field}} placeholders and the fields a visitor may send")
		}
		t, err := normalizeProxyTemplate(*c.Template)
		if err != nil {
			return err
		}
		out.Template = &t
		out.Methods = []string{"POST"}
		out.Paths = nil
		if t.Path != "" {
			out.Paths = []string{t.Path}
		}
	case c.Template != nil:
		return proxyInvalid("a request template is used only by the classes ai and send")
	case out.Class == domain.ProxyClassRead:
		if len(c.Methods) == 0 {
			out.Methods = []string{"GET", "HEAD"}
		}
		for _, m := range out.Methods {
			if m != "GET" && m != "HEAD" {
				return proxyInvalid("class read allows only GET and HEAD")
			}
		}
	}
	return nil
}

// normalizeProxyTemplate validates a request template and fills defaults.
func normalizeProxyTemplate(t domain.ProxyTemplate) (domain.ProxyTemplate, error) {
	raw, err := json.Marshal(t)
	if err != nil || len(raw) > proxyMaxTemplateBytes {
		return t, proxyInvalid("the request template must be at most 16 KB of JSON")
	}
	t.Method = strings.ToUpper(strings.TrimSpace(t.Method))
	if t.Method == "" {
		t.Method = "POST"
	}
	if !slices.Contains(proxyTemplateVerbs, t.Method) {
		return t, proxyInvalid("template method must be POST, PUT or PATCH")
	}
	if t.Path = strings.TrimSpace(t.Path); t.Path != "" {
		ps, err := normalizeProxyPaths([]string{t.Path})
		if err != nil || strings.Contains(t.Path, "{") {
			return t, proxyInvalid("template path must start with / and contain no placeholders, spaces, ?, # or %")
		}
		t.Path = ps[0]
	}
	if len(t.Fields) == 0 || len(t.Fields) > proxyMaxFields {
		return t, proxyInvalid("a template declares 1 to 20 fields a visitor may send")
	}
	fields := make(map[string]domain.ProxyField, len(t.Fields))
	for name, f := range t.Fields {
		nf, err := normalizeProxyField(name, f)
		if err != nil {
			return t, err
		}
		fields[name] = nf
	}
	t.Fields = fields
	body, err := compactTemplateBody(t.Body)
	if err != nil {
		return t, err
	}
	t.Body = body
	if err := checkPlaceholders(body, fields); err != nil {
		return t, err
	}
	if t.Response, err = normalizeResponsePath(t.Response); err != nil {
		return t, err
	}
	return t, nil
}

func normalizeProxyField(name string, f domain.ProxyField) (domain.ProxyField, error) {
	if !proxyFieldNameRe.MatchString(name) {
		return f, proxyInvalid("field names are 1-32 characters: a-z, 0-9 and _, starting with a letter: " + name)
	}
	f.Type = strings.ToLower(strings.TrimSpace(f.Type))
	bad := func(msg string) (domain.ProxyField, error) { return f, proxyInvalid("field " + name + ": " + msg) }
	switch f.Type {
	case domain.ProxyFieldString:
		f.MaxItems = 0
		if f.MaxLength == 0 {
			f.MaxLength = proxyDefaultStrLen
		}
	case domain.ProxyFieldMessages:
		if f.MaxLength == 0 {
			f.MaxLength = proxyDefaultMsgLen
		}
		if f.MaxItems == 0 {
			f.MaxItems = proxyDefaultMsgItems
		}
		if f.MaxItems < 1 || f.MaxItems > proxyMaxMsgItems {
			return bad(fmt.Sprintf("max_items must be 1 to %d", proxyMaxMsgItems))
		}
	case domain.ProxyFieldNumber, domain.ProxyFieldInteger, domain.ProxyFieldBoolean:
		f.MaxLength, f.MaxItems = 0, 0
		return f, nil
	default:
		return bad("type must be string, number, integer, boolean or messages")
	}
	if f.MaxLength < 1 || f.MaxLength > proxyMaxStrLen {
		return bad(fmt.Sprintf("max_length must be 1 to %d", proxyMaxStrLen))
	}
	return f, nil
}

func compactTemplateBody(body json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, proxyInvalid("a template needs a body: the JSON the API expects, with {{field}} placeholders")
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, body); err != nil {
		return nil, proxyInvalid("template body is not valid JSON")
	}
	return buf.Bytes(), nil
}

// checkPlaceholders makes every placeholder name a declared field, every
// field used, and messages fields used only as a whole value.
func checkPlaceholders(body json.RawMessage, fields map[string]domain.ProxyField) error {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return proxyInvalid("template body is not valid JSON")
	}
	used := map[string]bool{}
	var walkErr error
	walkJSONStrings(v, func(s string) {
		for _, m := range proxyPlaceholderRe.FindAllStringSubmatch(s, -1) {
			f, ok := fields[m[1]]
			if !ok {
				walkErr = proxyInvalid("placeholder {{" + m[1] + "}} names no declared field")
				return
			}
			if f.Type == domain.ProxyFieldMessages && strings.TrimSpace(s) != m[0] {
				walkErr = proxyInvalid("a messages field must be a whole value: \"{{" + m[1] + "}}\"")
				return
			}
			used[m[1]] = true
		}
	})
	if walkErr != nil {
		return walkErr
	}
	names := make([]string, 0, len(fields))
	for n := range fields {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if !used[n] {
			return proxyInvalid("field " + n + " is declared but its placeholder {{" + n + "}} is not in the body")
		}
	}
	return nil
}

func walkJSONStrings(v any, fn func(string)) {
	switch x := v.(type) {
	case string:
		fn(x)
	case []any:
		for _, e := range x {
			walkJSONStrings(e, fn)
		}
	case map[string]any:
		for _, e := range x {
			walkJSONStrings(e, fn)
		}
	}
}

func normalizeResponsePath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", nil
	}
	if len(p) > proxyMaxResponsePath {
		return "", proxyInvalid("response path is at most 200 characters")
	}
	for _, seg := range strings.Split(p, ".") {
		if !proxyRespSegRe.MatchString(seg) {
			return "", proxyInvalid("response must be a dotted path such as choices.0.message.content")
		}
	}
	return p, nil
}

// errProxyField builds the visitor error for a bad template field.
func errProxyField(name, msg string) error {
	return domain.E(domain.KindInvalid, domain.CodeProxyField, "field "+name+": "+msg).
		WithParams(map[string]any{"field": name})
}

// parseProxyFields checks the visitor's JSON object against the declared
// fields and returns the typed values.
func parseProxyFields(t domain.ProxyTemplate, body []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var in map[string]any
	if err := dec.Decode(&in); err != nil || in == nil {
		return nil, domain.E(domain.KindInvalid, domain.CodeProxyField,
			"send a JSON object with the fields of this endpoint")
	}
	names := make([]string, 0, len(in))
	for n := range in {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if _, ok := t.Fields[n]; !ok {
			return nil, errProxyField(n, "not accepted by this endpoint")
		}
	}
	out := make(map[string]any, len(t.Fields))
	for name, f := range t.Fields {
		v, present := in[name]
		if !present || v == nil {
			if f.Required {
				return nil, errProxyField(name, "is required")
			}
			continue
		}
		val, err := checkProxyField(name, f, v)
		if err != nil {
			return nil, err
		}
		out[name] = val
	}
	return out, nil
}

func checkProxyField(name string, f domain.ProxyField, v any) (any, error) {
	switch f.Type {
	case domain.ProxyFieldString:
		s, ok := v.(string)
		if !ok || !utf8.ValidString(s) {
			return nil, errProxyField(name, "must be text")
		}
		if utf8.RuneCountInString(s) > f.MaxLength {
			return nil, errProxyField(name, fmt.Sprintf("is longer than %d characters", f.MaxLength))
		}
		return s, nil
	case domain.ProxyFieldNumber, domain.ProxyFieldInteger:
		n, ok := v.(json.Number)
		if !ok {
			return nil, errProxyField(name, "must be a number")
		}
		if f.Type == domain.ProxyFieldInteger {
			if _, err := n.Int64(); err != nil {
				return nil, errProxyField(name, "must be a whole number")
			}
		} else if _, err := n.Float64(); err != nil {
			return nil, errProxyField(name, "must be a number")
		}
		return n, nil
	case domain.ProxyFieldBoolean:
		b, ok := v.(bool)
		if !ok {
			return nil, errProxyField(name, "must be true or false")
		}
		return b, nil
	case domain.ProxyFieldMessages:
		return checkMessages(name, f, v)
	}
	return nil, errProxyField(name, "has an unknown type")
}

func checkMessages(name string, f domain.ProxyField, v any) (any, error) {
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		return nil, errProxyField(name, "must be a non-empty list of {role, content} messages")
	}
	if len(list) > f.MaxItems {
		return nil, errProxyField(name, fmt.Sprintf("has more than %d messages", f.MaxItems))
	}
	out := make([]any, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok || len(m) != 2 {
			return nil, errProxyField(name, "each message has exactly role and content")
		}
		role, _ := m["role"].(string)
		content, ok := m["content"].(string)
		if (role != "user" && role != "assistant") || !ok {
			return nil, errProxyField(name, "role must be user or assistant and content must be text")
		}
		if utf8.RuneCountInString(content) > f.MaxLength {
			return nil, errProxyField(name, fmt.Sprintf("a message is longer than %d characters", f.MaxLength))
		}
		out = append(out, map[string]any{"role": role, "content": content})
	}
	return out, nil
}

// renderProxyBody replaces the placeholders of the template body with the
// visitor's values: a string that is exactly one placeholder becomes the
// typed value, placeholders inside longer strings become text.
func renderProxyBody(t domain.ProxyTemplate, values map[string]any) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(t.Body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("decode template body: %w", err)
	}
	out, err := json.Marshal(renderValue(v, values))
	if err != nil {
		return nil, fmt.Errorf("encode request body: %w", err)
	}
	return out, nil
}

func renderValue(v any, values map[string]any) any {
	switch x := v.(type) {
	case string:
		if m := proxyPlaceholderRe.FindStringSubmatch(x); m != nil && m[0] == strings.TrimSpace(x) {
			return values[m[1]]
		}
		return proxyPlaceholderRe.ReplaceAllStringFunc(x, func(ph string) string {
			name := proxyPlaceholderRe.FindStringSubmatch(ph)[1]
			return placeholderText(values[name])
		})
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = renderValue(e, values)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = renderValue(e, values)
		}
		return out
	}
	return v
}

func placeholderText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return ""
}

// extractJSONPath follows a dotted path (object keys, array indexes)
// through decoded JSON; ok is false when a step is missing.
func extractJSONPath(v any, path string) (any, bool) {
	if path == "" {
		return v, true
	}
	for _, seg := range strings.Split(path, ".") {
		switch x := v.(type) {
		case map[string]any:
			n, ok := x[seg]
			if !ok {
				return nil, false
			}
			v = n
		case []any:
			i := -1
			if _, err := fmt.Sscanf(seg, "%d", &i); err != nil || i < 0 || i >= len(x) || fmt.Sprint(i) != seg {
				return nil, false
			}
			v = x[i]
		default:
			return nil, false
		}
	}
	return v, true
}

// ProxyTemplateExample is the placeholder of the template field: an AI
// chat with a fixed model, system prompt and answer length.
const ProxyTemplateExample = `{
  "path": "/chat/completions",
  "body": {
    "model": "gpt-4o-mini",
    "max_tokens": 400,
    "messages": [
      {"role": "system", "content": "You answer questions about this site."},
      {"role": "user", "content": "{{question}}"}
    ]
  },
  "fields": {"question": {"type": "string", "max_length": 1000, "required": true}},
  "response": "choices.0.message.content"
}`
