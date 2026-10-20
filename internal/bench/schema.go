package bench

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// Validate checks value (decoded with encoding/json into any) against a JSON
// Schema document. It implements the subset used for structured LLM output:
// type, enum, const, properties, required, additionalProperties, items,
// min/max for numbers, strings and arrays, pattern, anyOf/oneOf/allOf and
// local $ref ("#/$defs/..", "#/definitions/.."). It returns human-readable
// violations; an empty slice means the value is valid.
func Validate(schema map[string]any, value any) []string {
	v := &validator{root: schema}
	v.check(schema, value, "$", 0)
	return v.errs
}

type validator struct {
	root map[string]any
	errs []string
}

func (v *validator) fail(path, format string, a ...any) {
	if len(v.errs) < 20 {
		v.errs = append(v.errs, path+": "+fmt.Sprintf(format, a...))
	}
}

func (v *validator) resolve(ref string) map[string]any {
	if !strings.HasPrefix(ref, "#/") {
		return nil
	}
	var cur any = v.root
	for _, part := range strings.Split(ref[2:], "/") {
		part = strings.NewReplacer("~1", "/", "~0", "~").Replace(part)
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	m, _ := cur.(map[string]any)
	return m
}

func typeOf(x any) string {
	switch t := x.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		if t == math.Trunc(t) {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

func typeMatches(want, got string) bool {
	return want == got || (want == "number" && got == "integer")
}

func (v *validator) sub(schema map[string]any, value any, path string, depth int) bool {
	save := v.errs
	v.errs = nil
	v.check(schema, value, path, depth+1)
	ok := len(v.errs) == 0
	v.errs = save
	return ok
}

func (v *validator) check(s map[string]any, x any, path string, depth int) {
	if depth > 64 {
		v.fail(path, "schema nesting too deep")
		return
	}
	if ref, ok := s["$ref"].(string); ok {
		target := v.resolve(ref)
		if target == nil {
			v.fail(path, "unresolvable $ref %q", ref)
			return
		}
		v.check(target, x, path, depth+1)
	}
	got := typeOf(x)
	switch t := s["type"].(type) {
	case string:
		if !typeMatches(t, got) {
			v.fail(path, "expected %s, got %s", t, got)
			return
		}
	case []any:
		okType := false
		for _, e := range t {
			if es, _ := e.(string); typeMatches(es, got) {
				okType = true
			}
		}
		if !okType {
			v.fail(path, "type %s not in %v", got, t)
			return
		}
	}
	if en, ok := s["enum"].([]any); ok {
		found := false
		for _, e := range en {
			if equalJSON(e, x) {
				found = true
			}
		}
		if !found {
			v.fail(path, "value not in enum")
		}
	}
	if c, ok := s["const"]; ok && !equalJSON(c, x) {
		v.fail(path, "value differs from const")
	}
	for _, kw := range []string{"allOf", "anyOf", "oneOf"} {
		list, ok := s[kw].([]any)
		if !ok {
			continue
		}
		matches := 0
		for _, e := range list {
			if es, ok := e.(map[string]any); ok && v.sub(es, x, path, depth) {
				matches++
			}
		}
		switch {
		case kw == "allOf" && matches != len(list):
			v.fail(path, "does not satisfy all of allOf")
		case kw == "anyOf" && matches == 0:
			v.fail(path, "does not satisfy any of anyOf")
		case kw == "oneOf" && matches != 1:
			v.fail(path, "satisfies %d branches of oneOf, want 1", matches)
		}
	}

	switch val := x.(type) {
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				if name, _ := r.(string); name != "" {
					if _, present := val[name]; !present {
						v.fail(path, "missing required property %q", name)
					}
				}
			}
		}
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if ps, ok := props[k].(map[string]any); ok {
				v.check(ps, val[k], path+"."+k, depth+1)
				continue
			}
			switch ap := s["additionalProperties"].(type) {
			case bool:
				if !ap {
					v.fail(path, "unexpected property %q", k)
				}
			case map[string]any:
				v.check(ap, val[k], path+"."+k, depth+1)
			}
		}
	case []any:
		if items, ok := s["items"].(map[string]any); ok {
			for i, e := range val {
				v.check(items, e, fmt.Sprintf("%s[%d]", path, i), depth+1)
			}
		}
		if n, ok := num(s["minItems"]); ok && float64(len(val)) < n {
			v.fail(path, "fewer than %v items", n)
		}
		if n, ok := num(s["maxItems"]); ok && float64(len(val)) > n {
			v.fail(path, "more than %v items", n)
		}
	case string:
		l := float64(len([]rune(val)))
		if n, ok := num(s["minLength"]); ok && l < n {
			v.fail(path, "shorter than %v chars", n)
		}
		if n, ok := num(s["maxLength"]); ok && l > n {
			v.fail(path, "longer than %v chars", n)
		}
		if p, ok := s["pattern"].(string); ok {
			if re, err := regexp.Compile(p); err == nil && !re.MatchString(val) {
				v.fail(path, "does not match pattern %q", p)
			}
		}
	case float64:
		if n, ok := num(s["minimum"]); ok && val < n {
			v.fail(path, "%v below minimum %v", val, n)
		}
		if n, ok := num(s["maximum"]); ok && val > n {
			v.fail(path, "%v above maximum %v", val, n)
		}
		if n, ok := num(s["exclusiveMinimum"]); ok && val <= n {
			v.fail(path, "%v not above %v", val, n)
		}
		if n, ok := num(s["exclusiveMaximum"]); ok && val >= n {
			v.fail(path, "%v not below %v", val, n)
		}
	}
}

func num(x any) (float64, bool) { f, ok := x.(float64); return f, ok }

func equalJSON(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}
