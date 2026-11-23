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

// normalizeNumber turns what the user typed into international digits.
// "+49 171 234", "0049171234" and, for an own number in Germany, "0171234"
// all become "49171234". Other input is taken as already international.
func normalizeNumber(input, ownNumber string) string {
	input = strings.TrimSpace(input)
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, input)
	switch {
	case strings.HasPrefix(input, "+"):
		return digits
	case strings.HasPrefix(digits, "00"):
		return digits[2:]
	case strings.HasPrefix(digits, "0"):
		if cc := callingCode(ownNumber); cc != "" {
			return cc + digits[1:]
		}
	}
	return digits
}
