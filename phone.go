package main

import "strings"

// callingCode returns the country calling code at the start of an
// international number without "+", using the ITU numbering zones.
func callingCode(number string) string {
	if number == "" {
		return ""
	}
	if number[0] == '1' || number[0] == '7' {
		return number[:1]
	}
	if len(number) < 3 {
		return ""
	}
	const twoDigit = " 20 27 30 31 32 33 34 36 39 40 41 43 44 45 46 47 48 49" +
		" 51 52 53 54 55 56 57 58 60 61 62 63 64 65 66 81 82 84 86 90 91 92 93 94 95 98 "
	if strings.Contains(twoDigit, " "+number[:2]+" ") {
		return number[:2]
	}
	return number[:3]
}

// phoneSeparators are what people type or paste between digits.
var phoneSeparators = strings.NewReplacer(
	" ", "", "\u00a0", "", "\u202f", "", "\t", "",
	"-", "", "\u2010", "", "\u2011", "", "\u2012", "", "\u2013", "", "\u2014", "",
	"/", "", "\\", "", ".", "", "(", "", ")", "", "[", "", "]", "", "'", "",
)

// phoneDigits strips separators; ok is false unless what's left is a phone
// number: an optional "+" and 6 to 15 digits.
func phoneDigits(input string) (s string, ok bool) {
	s = strings.TrimSpace(input)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "tel:"), "TEL:")
	// "+49 (0)171 …": the bracketed trunk 0 isn't dialled internationally
	if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "00") {
		s = strings.Replace(s, "(0)", "", 1)
	}
	s = phoneSeparators.Replace(s)
	digits := strings.TrimPrefix(s, "+")
	if len(digits) < 6 || len(digits) > 17 { // 17: "00" + 15 digits
		return "", false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return s, true
}

// normalizeNumber turns what the user typed into international digits, or ""
// if it isn't a phone number. "+49 171 234", "0049 171 234", "0171/234" and
// "171-234" all become "49171234" for an own number in Germany: without a
// prefix a number is national unless it starts with the own country code.
func normalizeNumber(input, ownNumber string) string {
	s, ok := phoneDigits(input)
	if !ok {
		return ""
	}
	cc := callingCode(ownNumber)
	switch {
	case strings.HasPrefix(s, "+"):
		return s[1:]
	case strings.HasPrefix(s, "00"):
		return s[2:]
	case strings.HasPrefix(s, "0"):
		if cc != "" {
			return cc + s[1:]
		}
	case cc != "" && !strings.HasPrefix(s, cc):
		return cc + s
	}
	return s
}
