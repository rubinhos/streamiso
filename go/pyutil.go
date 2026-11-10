package main

// Small helpers that reproduce the Python behaviours the catalog relies on (str.strip, str.title,
// truthiness, round, textwrap.wrap...), so the Go version answers exactly like the Python one.

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// isPySpace is Python's str.isspace() for one character (it also counts \x1c-\x1f).
func isPySpace(r rune) bool { return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f) }

func pyStrip(s string) string { return strings.TrimFunc(s, isPySpace) }

// collapseSpace is re.sub(r"\s+", " ", s).
func collapseSpace(s string) string {
	var b strings.Builder
	inSpace := false
	for _, r := range s {
		if isPySpace(r) {
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

// pySplit is str.split() with no argument.
func pySplit(s string) []string { return strings.FieldsFunc(s, isPySpace) }

func isCased(r rune) bool { return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r) }

// pyTitle is str.title(): a letter after a cased letter is lowered, any other is title-cased.
func pyTitle(s string) string {
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

// pyLower is str.lower(): like strings.ToLower, but the dotted capital I becomes "i" plus a
// combining dot, as in Python.
func pyLower(s string) string { return strings.ToLower(strings.ReplaceAll(s, "İ", "i̇")) }

// casefold approximates str.casefold() (lower case, plus the German sharp s).
func casefold(s string) string { return strings.ReplaceAll(pyLower(s), "ß", "ss") }

// runeSlice is s[:n] in Python (n counts characters, not bytes).
func runeSlice(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// truthy is Python's bool(value) for decoded JSON.
func truthy(v any) bool {
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
func isIntText(n json.Number) bool { return !strings.ContainsAny(string(n), ".eE") }

// pyFloatRepr is repr() of a Python float.
func pyFloatRepr(f float64) string {
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

// pyStr is str(value) for decoded JSON.
func pyStr(v any) string {
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
		if isIntText(x) {
			if i, err := strconv.ParseInt(string(x), 10, 64); err == nil {
				return strconv.FormatInt(i, 10)
			}
			return string(x)
		}
		f, _ := x.Float64()
		return pyFloatRepr(f)
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10)
		}
		return pyFloatRepr(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// toFloat reads a JSON number (or a Go number) as float64.
func toFloat(v any) (float64, bool) {
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

// isPyInt tells whether a decoded JSON value is a Python int (not a float, not a bool).
func isPyInt(v any) bool {
	switch x := v.(type) {
	case json.Number:
		return isIntText(x)
	case int, int64:
		return true
	}
	return false
}

// pyEqual is Python's == for decoded JSON scalars (2 == 2.0, None == None).
func pyEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	fa, oka := toFloat(a)
	fb, okb := toFloat(b)
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

// pyRound is Python's round(x): half to even.
func pyRound(x float64) int64 { return int64(math.RoundToEven(x)) }

// pyRound2 is Python's round(x, 2), correctly rounded from the exact binary value.
func pyRound2(x float64) float64 {
	f, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'f', 2, 64), 64)
	return f
}

// jsonMarshal writes JSON without escaping <, > and & (like Python's json.dumps).
func jsonMarshal(v any, indent string) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent != "" {
		enc.SetIndent("", indent)
	}
	_ = enc.Encode(v)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// decodeJSON parses JSON keeping numbers as json.Number, so they are written back unchanged.
func decodeJSON(data []byte) (any, error) {
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

// ---------------------------------------------------------------------------------------------
// textwrap.wrap(text, width) with Python's defaults (break_long_words, break_on_hyphens,
// drop_whitespace), used for the placeholder covers.

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' }

// isLetterRune is textwrap's [^\d\W]: a word character that is not a digit.
func isLetterRune(r rune) bool { return isWordRune(r) && !unicode.IsDigit(r) }

// splitChunks splits text into whitespace runs and words, breaking a hyphenated word after its
// hyphens as textwrap's wordsep_re does ("Guarda-Chuva" -> "Guarda-", "Chuva").
func splitChunks(text []rune) [][]rune {
	var out [][]rune
	n := len(text)
	i := 0
	for i < n {
		if text[i] == ' ' {
			j := i
			for j < n && text[j] == ' ' {
				j++
			}
			out = append(out, text[i:j])
			i = j
			continue
		}
		j := i
		for j < n && text[j] != ' ' {
			// a break after a hyphen: two letters (or letter-letter-) before it, letter -? letter after
			if text[j] == '-' && j > i {
				before := (j >= 2 && isLetterRune(text[j-1]) && isLetterRune(text[j-2])) ||
					(j >= 3 && isLetterRune(text[j-1]) && text[j-2] == '-' && isLetterRune(text[j-3]))
				after := false
				if j+1 < n && isLetterRune(text[j+1]) {
					if j+2 < n && isLetterRune(text[j+2]) {
						after = true
					} else if j+3 < n && text[j+2] == '-' && isLetterRune(text[j+3]) {
						after = true
					}
				}
				if before && after {
					j++
					break
				}
			}
			j++
		}
		out = append(out, text[i:j])
		i = j
	}
	return out
}

func isBlank(c []rune) bool {
	for _, r := range c {
		if !isPySpace(r) {
			return false
		}
	}
	return true
}

func textwrapWrap(text string, width int) []string {
	rs := []rune(strings.ReplaceAll(text, "\t", "        "))
	for i, r := range rs {
		if isPySpace(r) {
			rs[i] = ' '
		}
	}
	chunks := splitChunks(rs)
	// reversed, as textwrap pops from the end
	for l, r := 0, len(chunks)-1; l < r; l, r = l+1, r-1 {
		chunks[l], chunks[r] = chunks[r], chunks[l]
	}
	var lines []string
	for len(chunks) > 0 {
		var cur [][]rune
		curLen := 0
		if isBlank(chunks[len(chunks)-1]) && len(lines) > 0 {
			chunks = chunks[:len(chunks)-1]
		}
		for len(chunks) > 0 {
			l := len(chunks[len(chunks)-1])
			if curLen+l <= width {
				cur = append(cur, chunks[len(chunks)-1])
				chunks = chunks[:len(chunks)-1]
				curLen += l
			} else {
				break
			}
		}
		if len(chunks) > 0 && len(chunks[len(chunks)-1]) > width {
			// _handle_long_word
			spaceLeft := width - curLen
			if width < 1 {
				spaceLeft = 1
			}
			chunk := chunks[len(chunks)-1]
			end := spaceLeft
			if len(chunk) > spaceLeft {
				hyphen := -1
				for k := spaceLeft - 1; k >= 0; k-- {
					if chunk[k] == '-' {
						hyphen = k
						break
					}
				}
				if hyphen > 0 {
					for _, c := range chunk[:hyphen] {
						if c != '-' {
							end = hyphen + 1
							break
						}
					}
				}
			}
			cur = append(cur, chunk[:end])
			chunks[len(chunks)-1] = chunk[end:]
			curLen += end
		}
		if len(cur) > 0 && isBlank(cur[len(cur)-1]) {
			curLen -= len(cur[len(cur)-1])
			cur = cur[:len(cur)-1]
		}
		if len(cur) > 0 {
			var b strings.Builder
			for _, c := range cur {
				b.WriteString(string(c))
			}
			lines = append(lines, b.String())
		}
	}
	return lines
}
