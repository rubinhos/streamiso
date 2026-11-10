// Package compat keeps the text and JSON rules the catalog has always followed (they come from its first
// version, in Python): str.strip and str.title, truthiness, round half to even, textwrap.wrap. The API's
// answers and the files in DATA_DIR depend on them, so they stay exactly as they are.
package compat

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// IsSpace is Python's str.isspace() for one character (it also counts \x1c-\x1f).
func IsSpace(r rune) bool { return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f) }

func Strip(s string) string { return strings.TrimFunc(s, IsSpace) }

// CollapseSpace is re.sub(r"\s+", " ", s).
func CollapseSpace(s string) string {
	var b strings.Builder
	inSpace := false
	for _, r := range s {
		if IsSpace(r) {
			if !inSpace {
				b.WriteByte(' ')
			}
			inSpace = true
			continue
		}
		inSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

// Split is str.split() with no argument.
func Split(s string) []string { return strings.FieldsFunc(s, IsSpace) }

func isCased(r rune) bool { return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r) }

// Title is str.title(): a letter after a cased letter is lowered, any other is title-cased.
func Title(s string) string {
	var b strings.Builder
	prev := false
	for _, r := range s {
		if prev {
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(unicode.ToTitle(r))
		}
		prev = isCased(r)
	}
	return b.String()
}

// Lower is str.lower(): like strings.ToLower, but the dotted capital I becomes "i" plus a
// combining dot, as in Python.
func Lower(s string) string { return strings.ToLower(strings.ReplaceAll(s, "İ", "i̇")) }

// Casefold approximates str.casefold() (lower case, plus the German sharp s).
func Casefold(s string) string { return strings.ReplaceAll(Lower(s), "ß", "ss") }

// Slice is s[:n] in Python (n counts characters, not bytes).
func Slice(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// Truthy is Python's bool(value) for decoded JSON.
func Truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		f, err := x.Float64()
		return err != nil || f != 0
	case float64:
		return x != 0
	case int:
		return x != 0
	case int64:
		return x != 0
	case map[string]any:
		return len(x) > 0
	case []any:
		return len(x) > 0
	case []string:
		return len(x) > 0
	}
	return true
}

// isNumberText tells an integer literal ("597") from a float one ("597.0", "1e2") in JSON.
func IsIntText(n json.Number) bool { return !strings.ContainsAny(string(n), ".eE") }

// FloatRepr is repr() of a Python float.
func FloatRepr(f float64) string {
	if math.IsInf(f, 1) {
		return "inf"
	}
	if math.IsInf(f, -1) {
		return "-inf"
	}
	if math.IsNaN(f) {
		return "nan"
	}
	a := math.Abs(f)
	if a != 0 && (a >= 1e16 || a < 1e-4) {
		return strconv.FormatFloat(f, 'e', -1, 64)
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// Str is str(value) for decoded JSON.
func Str(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case string:
		return x
	case json.Number:
		if IsIntText(x) {
			if i, err := strconv.ParseInt(string(x), 10, 64); err == nil {
				return strconv.FormatInt(i, 10)
			}
			return string(x)
		}
		f, _ := x.Float64()
		return FloatRepr(f)
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10)
		}
		return FloatRepr(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// ToFloat reads a JSON number (or a Go number) as float64.
func ToFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

// IsInt tells whether a decoded JSON value is a Python int (not a float, not a bool).
func IsInt(v any) bool {
	switch x := v.(type) {
	case json.Number:
		return IsIntText(x)
	case int, int64:
		return true
	}
	return false
}

// Equal is Python's == for decoded JSON scalars (2 == 2.0, None == None).
func Equal(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	fa, oka := ToFloat(a)
	fb, okb := ToFloat(b)
	if oka && okb {
		return fa == fb
	}
	if ba, ok := a.(bool); ok {
		if bb, ok := b.(bool); ok {
			return ba == bb
		}
		if okb { // True == 1 in Python
			return (ba && fb == 1) || (!ba && fb == 0)
		}
		return false
	}
	if bb, ok := b.(bool); ok && oka {
		return (bb && fa == 1) || (!bb && fa == 0)
	}
	sa, oka2 := a.(string)
	sb, okb2 := b.(string)
	return oka2 && okb2 && sa == sb
}

// Round is Python's round(x): half to even.
func Round(x float64) int64 { return int64(math.RoundToEven(x)) }

// Round2 is Python's round(x, 2), correctly rounded from the exact binary value.
func Round2(x float64) float64 {
	f, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'f', 2, 64), 64)
	return f
}

// MarshalJSON writes JSON without escaping <, > and & (like Python's json.dumps).
func MarshalJSON(v any, indent string) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent != "" {
		enc.SetIndent("", indent)
	}
	_ = enc.Encode(v)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// DecodeJSON parses JSON keeping numbers as json.Number, so they are written back unchanged.
func DecodeJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, &json.SyntaxError{}
	}
	return v, nil
}

// FirstTruthy is (a or b or ... or def).
func FirstTruthy(def any, vals ...any) any {
	for _, v := range vals {
		if Truthy(v) {
			return v
		}
	}
	return def
}

func GetOr(m map[string]any, key string, def any) any {
	if v, ok := m[key]; ok {
		return v
	}
	return def
}

// StrOr is (value or default) for a value used as text.
func StrOr(v any, def string) string {
	if !Truthy(v) {
		return def
	}
	return Str(v)
}

func IsAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Int is int(value) for a decoded JSON value (0 when it is not a number).
func Int(v any) int64 {
	switch x := v.(type) {
	case bool:
		if x {
			return 1
		}
		return 0
	case string:
		n, _ := strconv.ParseInt(Strip(x), 10, 64)
		return n
	}
	f, _ := ToFloat(v)
	return int64(f)
}

func CopyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Stem is Path(name).stem: the last component without its last suffix.
func Stem(path string) string {
	name := path[strings.LastIndex(path, "/")+1:]
	if i := strings.LastIndex(name, "."); i > 0 && i < len(name)-1 {
		return name[:i]
	}
	return name
}

// SplitLines is str.splitlines().
func SplitLines(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case '\n', '\r', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			return true
		}
		return false
	})
}
