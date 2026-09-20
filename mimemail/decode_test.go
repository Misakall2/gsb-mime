package mimemail

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestParsePlainTextQuotedPrintable(t *testing.T) {
	raw := "Subject: =?UTF-8?B?5L2g5aW9?=\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n" +
		"\r\n" +
		"=E4=BD=A0=E5=A5=BD=EF=BC=8C=E4=B8=96=E7=95=8C\r\n"
	msg, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := msg.Header.Get("Subject"); got != "你好" {
		t.Fatalf("Subject = %q, want 你好", got)
	}
	if msg.Root.Type != "text/plain" {
		t.Fatalf("root type = %q", msg.Root.Type)
	}
	if string(msg.Root.Body) != "你好，世界" {
		t.Fatalf("body = %q", msg.Root.Body)
	}
	roundTripTree(t, msg)
}

func TestQuotedPrintableSoftBreaks(t *testing.T) {
	input := []byte("Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
		"hello=\r\nworld\r\nsecond=\r\nline\r\n")
	msg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := string(msg.Root.Body), "helloworld\nsecondline"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestQuotedPrintableBadHex(t *testing.T) {
	input := []byte("Content-Transfer-Encoding: quoted-printable\r\n\r\nabc=ZZdef")
	_, err := Parse(input)
	if err == nil {
		t.Fatal("expected error for invalid QP hexadecimal, got nil")
	}
	if !strings.Contains(err.Error(), "hexadecimal") {
		t.Fatalf("error does not mention hexadecimal: %v", err)
	}
}

func TestBase64Body(t *testing.T) {
	payload := []byte{0, 1, 2, 250, 251, 252, 'a', 'b'}
	input := []byte("Content-Type: application/octet-stream\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		wrapAt76(base64.StdEncoding.EncodeToString(payload)))
	msg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !bytes.Equal(msg.Root.Body, payload) {
		t.Fatalf("body = %v, want %v", msg.Root.Body, payload)
	}
	roundTripTree(t, msg)
}

func TestBase64MissingPaddingIsError(t *testing.T) {
	// "YWJjZA" decodes toward abcd but is missing the trailing "==".
	input := []byte("Content-Transfer-Encoding: base64\r\n\r\nYWJjZA\r\n")
	_, err := Parse(input)
	if err == nil {
		t.Fatal("expected missing-padding error, got nil")
	}
	if !strings.Contains(err.Error(), "padding") {
		t.Fatalf("error should mention padding: %v", err)
	}
}

func TestRFC2047EncodedWords(t *testing.T) {
	// Q encoding, B encoding and adjacent encoded-words split across a fold.
	input := []byte("Subject: =?utf-8?Q?=E6=8A=A5=E5=91=8A?=\r\n" +
		"X-Split: =?UTF-8?B?5L2g?= =?UTF-8?B?5aW9?=\r\n\r\nbody")
	msg, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := msg.Header.Get("Subject"); got != "报告" {
		t.Fatalf("Q word = %q, want 报告", got)
	}
	if got := msg.Header.Get("X-Split"); got != "你好" {
		t.Fatalf("split words = %q, want 你好 (bytes % x)", got, []byte(got))
	}
}

func TestUnknownCharsetIsError(t *testing.T) {
	input := []byte("Subject: =?ISO-8859-1?B?aGVsbG8=?=\r\n\r\nx")
	if _, err := Parse(input); err == nil {
		t.Fatal("expected unsupported encoded-word charset error")
	}
	input2 := []byte("Content-Type: text/plain\r\n" +
		"Content-Disposition: attachment; filename*=ISO-8859-1''caf%E9\r\n\r\nx")
	if _, err := Parse(input2); err == nil {
		t.Fatal("expected unsupported RFC 2231 charset error")
	}
}
