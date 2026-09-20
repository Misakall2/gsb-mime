package mimemail

import "strings"

func wrapAt76(s string) string {
	var b strings.Builder
	for len(s) > 0 {
		n := 76
		if n > len(s) {
			n = len(s)
		}
		b.WriteString(s[:n])
		b.WriteString("\r\n")
		s = s[n:]
	}
	return b.String()
}
