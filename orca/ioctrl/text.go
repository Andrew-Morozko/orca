package ioctrl

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

func BorderMessage(lines ...string) string {
	center := true
	linesAround := 2

	maxLine := 0
	lineLength := make([]int, len(lines))
	for i := 0; i < len(lines); i++ {
		lines[i] = strings.TrimSpace(lines[i])
		lineLength[i] = utf8.RuneCountInString(lines[i])

		if lineLength[i] > maxLine {
			maxLine = lineLength[i]
		}
	}
	buf := bytes.Buffer{}
	buf.WriteString(strings.Repeat("\n", linesAround+1))

	buf.WriteString(strings.Repeat("*", maxLine+6))
	buf.WriteString("\n")

	buf.WriteString("*  ")
	buf.WriteString(strings.Repeat(" ", maxLine))
	buf.WriteString("  *\n")

	for i := 0; i < len(lines); i++ {
		buf.WriteString("*  ")
		whitespace := maxLine - lineLength[i]
		if center {
			buf.WriteString(strings.Repeat(" ", whitespace/2))
		}
		buf.WriteString(lines[i])
		if center {
			buf.WriteString(strings.Repeat(" ", whitespace-(whitespace/2)))
		} else {
			buf.WriteString(strings.Repeat(" ", whitespace))
		}
		buf.WriteString("  *\n")
	}
	buf.WriteString("*  ")
	buf.WriteString(strings.Repeat(" ", maxLine))
	buf.WriteString("  *\n")

	buf.WriteString(strings.Repeat("*", maxLine+6))
	buf.WriteString("\n")
	buf.WriteString(strings.Repeat("\n", linesAround))
	return buf.String()
}
