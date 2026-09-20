package mimemail

import (
	"bytes"
	"net/textproto"
	"strings"
	"testing"
)

func roundTripTree(t *testing.T, msg *Message) {
	t.Helper()
	encoded, err := msg.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	reparsed, err := Parse(encoded)
	if err != nil {
		t.Fatalf("reparse: %v\n%s", err, encoded)
	}
	if !partsEqual(reparsed.Root, msg.Root) {
		t.Fatalf("round-trip mismatch:\n%s", diffPart(reparsed.Root, msg.Root))
	}
}

func partsEqual(a, b *Part) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Type != b.Type || a.ContentID != b.ContentID || a.FileName != b.FileName ||
		a.Disposition != b.Disposition || !bytes.Equal(a.Body, b.Body) {
		return false
	}
	if len(a.Parts) != len(b.Parts) {
		return false
	}
	for i := range a.Parts {
		if !partsEqual(a.Parts[i], b.Parts[i]) {
			return false
		}
	}
	return true
}

func diffPart(a, b *Part) string {
	var sb strings.Builder
	writePart(&sb, a, 0)
	sb.WriteString("--- want ---\n")
	writePart(&sb, b, 0)
	return sb.String()
}

func writePart(sb *strings.Builder, p *Part, indent int) {
	if p == nil {
		return
	}
	pad := strings.Repeat("  ", indent)
	sb.WriteString(pad + p.Type)
	if p.ContentID != "" {
		sb.WriteString(" cid=" + p.ContentID)
	}
	if p.FileName != "" {
		sb.WriteString(" file=" + p.FileName)
	}
	sb.WriteString(" body=" + describeBytes(p.Body) + "\n")
	for _, child := range p.Parts {
		writePart(sb, child, indent+1)
	}
}

func describeBytes(b []byte) string {
	if len(b) > 24 {
		return "<" + itoa(len(b)) + " bytes>"
	}
	return strconvQuote(string(b))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func strconvQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func TestEncodeChineseSubjectDecodesBack(t *testing.T) {
	msg := &Message{
		Header: textproto.MIMEHeader{
			"Subject": []string{"合同 2026"},
		},
		Root: &Part{
			Type:       "text/plain",
			TypeParams: map[string]string{"charset": "utf-8"},
			Body:       []byte("正文内容"),
		},
	}
	encoded, err := msg.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if bytes.Contains(encoded, []byte("合同")) {
		t.Fatalf("non-ASCII must be encoded in headers/body wire format:\n%s", encoded)
	}
	reparsed, err := Parse(encoded)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if got := reparsed.Header.Get("Subject"); got != "合同 2026" {
		t.Fatalf("subject round-trip = %q", got)
	}
	if string(reparsed.Root.Body) != "正文内容" {
		t.Fatalf("body round-trip = %q", reparsed.Root.Body)
	}
}
