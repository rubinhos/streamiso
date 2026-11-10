// The generated cover of a disc without one: its title on a colour taken from the title.

package domain

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strconv"
	"streamiso/internal/compat"
	"strings"
)

// ---------------------------------------------------------------------------------- placeholder
func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", ">", "&gt;", "<", "&lt;").Replace(s)
}

func PlaceholderSVG(title string, year any, square bool) string {
	h := 600
	if square {
		h = 400
	}
	sum := md5.Sum([]byte(title))
	hue64, _ := strconv.ParseInt(hex.EncodeToString(sum[:])[:4], 16, 64)
	hue := int(hue64 % 360)
	lines := compat.Wrap(title, 14)
	if len(lines) > 5 {
		lines = lines[:5]
	}
	if len(lines) == 0 {
		lines = []string{"DVD"}
	}
	startY := 330
	cy := 140
	if square {
		startY, cy = 250, 90
	}
	startY -= (len(lines) - 1) * 24
	var text strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&text, `<text x="200" y="%d" text-anchor="middle" font-family="Helvetica, Arial, sans-serif" font-size="38" font-weight="700" fill="#fff">%s</text>`,
			startY+i*48, xmlEscape(line))
	}
	yearTxt := ""
	if compat.Truthy(year) {
		yearTxt = fmt.Sprintf(`<text x="200" y="%d" text-anchor="middle" font-family="Helvetica, Arial, sans-serif" font-size="26" fill="#ffffffaa">%s</text>`,
			h-60, xmlEscape(compat.Str(year)))
	}
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 %d">`+
		`<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1">`+
		`<stop offset="0" stop-color="hsl(%d,45%%,32%%)"/>`+
		`<stop offset="1" stop-color="hsl(%d,50%%,14%%)"/></linearGradient></defs>`+
		`<rect width="400" height="%d" fill="url(#g)"/>`+
		`<circle cx="200" cy="%d" r="62" fill="none" stroke="#ffffff55" stroke-width="6"/>`+
		`<circle cx="200" cy="%d" r="14" fill="#ffffff55"/>`+
		`%s%s</svg>`, h, hue, (hue+40)%360, h, cy, cy, text.String(), yearTxt)
}
