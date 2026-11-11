package domain

import (
	"regexp"
	"strings"
)

// languageNames are the names VLC gives the ISO 639-2 codes that discs use (both the B and T forms).
var languageNames = map[string]string{
	"ara": "Arabic", "bul": "Bulgarian", "cat": "Catalan", "ces": "Czech", "cze": "Czech", "chi": "Chinese",
	"zho": "Chinese", "dan": "Danish", "deu": "German", "ger": "German", "ell": "Greek", "gre": "Greek",
	"eng": "English", "est": "Estonian", "fin": "Finnish", "fra": "French", "fre": "French", "heb": "Hebrew",
	"hin": "Hindi", "hrv": "Croatian", "hun": "Hungarian", "ice": "Icelandic", "isl": "Icelandic",
	"ind": "Indonesian", "ita": "Italian", "jpn": "Japanese", "kor": "Korean", "lav": "Latvian",
	"lit": "Lithuanian", "may": "Malay", "msa": "Malay", "nld": "Dutch", "dut": "Dutch", "nor": "Norwegian",
	"pol": "Polish", "por": "Portuguese", "ron": "Romanian", "rum": "Romanian", "rus": "Russian",
	"slk": "Slovak", "slo": "Slovak", "slv": "Slovenian", "spa": "Spanish", "srp": "Serbian", "swe": "Swedish",
	"tha": "Thai", "tur": "Turkish", "ukr": "Ukrainian", "vie": "Vietnamese",
}

// LanguageName is the English name of an ISO 639-2 language code, or the code in capitals if unknown.
func LanguageName(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if name, ok := languageNames[code]; ok {
		return name
	}
	return strings.ToUpper(code)
}

var trackLanguageRE = regexp.MustCompile(`\s*(-\s*)?\[[^\]]*\]\s*$`) // " - [English]" at the end of a name

// RelabelTracks gives VLC's tracks ({"id", "name", ...}, as "Track 2 - [English]") the languages of the
// disc, in order. Tracks with a negative id (VLC's "Disable") keep their name. Nothing changes unless there
// is exactly one language per track, so a list that does not belong to these tracks is never applied.
func RelabelTracks(tracks []map[string]any, languages []string) []map[string]any {
	var real []map[string]any
	for _, t := range tracks {
		if id, _ := t["id"].(int); id >= 0 {
			real = append(real, t)
		}
	}
	if len(real) == 0 || len(real) != len(languages) {
		return tracks
	}
	out := make([]map[string]any, 0, len(tracks))
	i := 0
	for _, t := range tracks {
		if id, _ := t["id"].(int); id < 0 {
			out = append(out, t)
			continue
		}
		c := make(map[string]any, len(t))
		for k, v := range t {
			c[k] = v
		}
		name, _ := t["name"].(string)
		c["name"] = trackLanguageRE.ReplaceAllString(name, "") + " - [" + LanguageName(languages[i]) + "]"
		i++
		out = append(out, c)
	}
	return out
}
