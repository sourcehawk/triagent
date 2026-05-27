package watches

import "strings"

const watchNameTitleCap = 32

// FormatWatchTitle builds the "[watch: <name>] <first sentence>" title
// the launcher applies to investigations spawned from a watch. Truncates
// the watch name to keep the title compact; falls back to "untitled"
// when the briefing is empty.
func FormatWatchTitle(watchName, briefing string) string {
	name := watchName
	if len(name) > watchNameTitleCap {
		name = name[:watchNameTitleCap]
	}
	first := strings.TrimSpace(firstSentence(briefing))
	if first == "" {
		first = "untitled"
	}
	return "[watch: " + name + "] " + first
}

func firstSentence(s string) string {
	for i, r := range s {
		if r == '.' || r == '!' || r == '?' {
			return s[:i+1]
		}
	}
	return s
}
