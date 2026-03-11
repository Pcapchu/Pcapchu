package common

import "unicode"

// Lang represents a detected language.
type Lang string

const (
	LangZH    Lang = "zh"
	LangEN    Lang = "en"
	LangMixed Lang = "mixed"
	LangOther Lang = "other"
)

// DetectLang returns the dominant language of s based on character analysis.
func DetectLang(s string) Lang {
	var hasChinese, hasEnglish bool
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			hasChinese = true
		} else if unicode.IsLetter(r) && r < 128 {
			hasEnglish = true
		}
	}
	switch {
	case hasChinese && hasEnglish:
		return LangMixed
	case hasChinese:
		return LangZH
	case hasEnglish:
		return LangEN
	default:
		return LangOther
	}
}
