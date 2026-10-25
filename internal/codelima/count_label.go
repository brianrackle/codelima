package codelima

import "strconv"

// pluralForm chooses explicit English forms, including phrases with a verb.
// Zero uses the plural form; the caller supplies any irregular spelling.
func pluralForm(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

func countLabel(count int, singular, plural string) string {
	return strconv.Itoa(count) + " " + pluralForm(count, singular, plural)
}
