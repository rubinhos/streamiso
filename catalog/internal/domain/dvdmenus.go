package domain

// A DVD's menus, as its IFO files list them. A DVD has one main menu (the Title menu, in the disc's video
// manager) and, in each title set (VTS: a group of titles, such as the film or the extras), up to five
// menus of its own: Root, Chapters, Audio, Subtitles and Angle. Many discs leave most of them out, and
// some keep every menu in the video manager, with no Root menu in the film's title set.

// The menu ids of the API, in the order they are offered.
const (
	MenuTitle     = "title"
	MenuRoot      = "root"
	MenuChapters  = "chapters"
	MenuAudio     = "audio"
	MenuSubtitles = "subtitles"
	MenuAngle     = "angle"
)

// A Blu-ray's menus are drawn by the disc's own program, which the player reaches by two keys only: its main
// menu (Top menu, as a DVD's Title menu) and its pop-up menu over the film. Both work during the film.
const MenuPopup = "popup"

var BlurayMenus = []string{MenuTitle, MenuPopup}

var setMenuOrder = []string{MenuRoot, MenuChapters, MenuAudio, MenuSubtitles, MenuAngle}

// MenuSeekpoint is how VLC's DVD module calls each menu: as this "chapter" of its menu title (title 0).
// VLC calls the Root menu first, when it goes to title 0, and then the menu of that chapter.
var MenuSeekpoint = map[string]int{MenuRoot: 1, MenuTitle: 2, MenuChapters: 3, MenuSubtitles: 4, MenuAudio: 5, MenuAngle: 6}

// DVDLayout is what a DVD's IFO files say about its titles and menus.
type DVDLayout struct {
	TitleMenu bool                    // the video manager has a Title menu (the disc's main menu)
	TitleSets []int                   // the title set of each title, from title 1
	SetMenus  map[int]map[string]bool // each title set's own menus (root, chapters, audio, subtitles, angle)
}

// SetOf is the title set that holds title n (from 1), or 0.
func (l *DVDLayout) SetOf(title int64) int {
	if l == nil || title < 1 || int(title) > len(l.TitleSets) {
		return 0
	}
	return l.TitleSets[title-1]
}

// Menus are the menus that can be opened now: title is the title playing (0 in a menu, as VLC reports it),
// and set the title set of the last title played (0 if unknown).
//
// During a title only the menus of its own title set count, and only if that set has a Root menu: VLC
// calls the Root menu before any other, and when that fails it takes the menu's number as a chapter of the
// film and jumps there. In a menu a failed call does nothing, so with the title set unknown every title
// set's menus are offered.
func (l *DVDLayout) Menus(title int64, set int) []string {
	out := []string{}
	if l == nil {
		return out
	}
	if l.TitleMenu {
		out = append(out, MenuTitle)
	}
	have := map[string]bool{}
	if title > 0 {
		if m := l.SetMenus[l.SetOf(title)]; m[MenuRoot] {
			have = m
		}
	} else if m, ok := l.SetMenus[set]; ok && set > 0 {
		have = m
	} else {
		for _, m := range l.SetMenus {
			for k, v := range m {
				have[k] = have[k] || v
			}
		}
	}
	for _, id := range setMenuOrder {
		if have[id] {
			out = append(out, id)
		}
	}
	return out
}

// HasMenu tells whether id is among menus.
func HasMenu(menus []string, id string) bool {
	for _, m := range menus {
		if m == id {
			return true
		}
	}
	return false
}
