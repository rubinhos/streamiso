package domain

import "streamiso/internal/compat"

// Genres TMDB leaves in English in these languages (its pt-BR TV list says "Kids"), plus "Concert",
// an editor suggestion TMDB does not have, and "Politics" (see CombinedGenres), named for the genre
// tags. A genre TMDB translates is named from its own lists (see loadGenreAliases).
var LocalGenreNames = map[string]map[string]string{
	"pt": {"Kids": "Infantil", "News": "Notícias", "Soap": "Novela", "Talk": "Talk Show",
		"Concert": "Concerto", "Politics": "Política"},
}

// TMDB's series genres that join two genres. A disc's tags list them apart, so a series is found
// under "Science Fiction" and "Fantasy" as a film is, and no tag repeats part of another.
var CombinedGenres = map[string][]string{
	"Action & Adventure": {"Action", "Adventure"},
	"Sci-Fi & Fantasy":   {"Science Fiction", "Fantasy"},
	"War & Politics":     {"War", "Politics"},
}

// GenreTags is a disc's genres as tags: in TMDB_LANGUAGE, the series genres that join two split in
// two, and each tag once ("Comedy" and "Comédia" are one). Like genre_tags in the Python catalog.
func GenreTags(genres any, aliases map[string]string) []any {
	combined := map[string][]string{}
	for name, parts := range CombinedGenres {
		combined[name] = parts
		if local, ok := aliases[name]; ok { // a language whose TMDB names the joined genre
			combined[local] = parts
		}
	}
	var list []string
	switch gs := genres.(type) {
	case []string:
		list = gs
	case []any:
		for _, g := range gs {
			if s, ok := g.(string); ok {
				list = append(list, s)
			}
		}
	}
	out := []any{}
	seen := map[string]bool{}
	for _, g := range list {
		parts, ok := combined[g]
		if !ok {
			parts = []string{g}
		}
		for _, part := range parts {
			tag := part
			if a, ok := aliases[part]; ok {
				tag = a
			}
			if !seen[compat.Casefold(tag)] {
				seen[compat.Casefold(tag)] = true
				out = append(out, tag)
			}
		}
	}
	return out
}
