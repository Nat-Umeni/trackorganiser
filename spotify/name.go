package spotify

import (
	"strings"
	"unicode"
)

func sanitizeName(nameToSanitize string) string {
	name := strings.TrimSpace(nameToSanitize)

	cleanName := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`"<>:/\|?*`, r) || unicode.IsControl(r) {
			return '-'
		}

		return r
	}, name)

	cleanName = strings.TrimSpace(cleanName)
	cleanName = strings.TrimRight(cleanName, " .")

	return cleanName
}
