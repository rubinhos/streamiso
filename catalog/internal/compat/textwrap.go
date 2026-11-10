// textwrap.wrap(text, width) with Python's defaults (break_long_words, break_on_hyphens,
// drop_whitespace), used for the placeholder covers.

package compat

import (
	"strings"
	"unicode"
)

func IsWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' }

// isLetterRune is textwrap's [^\d\W]: a word character that is not a digit.
func isLetterRune(r rune) bool { return IsWordRune(r) && !unicode.IsDigit(r) }

// splitChunks splits text into whitespace runs and domain.Words, breaking a hyphenated word after its
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
		if !IsSpace(r) {
			return false
		}
	}
	return true
}

func Wrap(text string, width int) []string {
	rs := []rune(strings.ReplaceAll(text, "\t", "        "))
	for i, r := range rs {
		if IsSpace(r) {
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
