package neko

import "testing"

func TestParseSize(t *testing.T) {
	for in, want := range map[string][3]int{
		"1920x1080": {1920, 1080, 1}, " 1024X576 ": {1024, 576, 1},
		"": {0, 0, 0}, "1920": {0, 0, 0}, "x1080": {0, 0, 0}, "0x576": {0, 0, 0}, "1920x1080@30": {0, 0, 0},
	} {
		w, h, ok := ParseSize(in)
		okInt := 0
		if ok {
			okInt = 1
		}
		if [3]int{w, h, okInt} != want {
			t.Errorf("ParseSize(%q) = %d, %d, %v; want %v", in, w, h, ok, want)
		}
	}
}
