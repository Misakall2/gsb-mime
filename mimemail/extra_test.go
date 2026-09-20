package mimemail

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestASCIIFilenameAndLFFoldRoundTrip(t *testing.T) {
	data := bytes.Repeat([]byte{0xDE, 0xAD}, 40)
	// LF-only line endings, and a folded Content-Disposition header.
	raw := "Content-Type: multipart/mixed; boundary=B\n" +
		"\n" +
		"--B\n" +
		"Content-Type: text/plain\n" +
		"\n" +
		"hi\n" +
		"--B\n" +
		"Content-Type: application/octet-stream\n" +
		"Content-Disposition: attachment;\n" +
		" filename=\"quarterly report.bin\"\n" +
		"Content-Transfer-Encoding: base64\n" +
		"\n" +
		wrapAt76(base64.StdEncoding.EncodeToString(data)) +
		"--B--\n"
	msg, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(msg.Root.Parts) != 2 {
		t.Fatalf("parts = %d", len(msg.Root.Parts))
	}
	att := msg.Root.Parts[1]
	if att.FileName != "quarterly report.bin" {
		t.Fatalf("filename = %q", att.FileName)
	}
	if !bytes.Equal(att.Body, data) {
		t.Fatal("attachment bytes changed")
	}
	roundTripTree(t, msg)
}

func TestUnknownTransferEncodingIsError(t *testing.T) {
	raw := "Content-Transfer-Encoding: weird-encoding\r\n\r\nbody"
	if _, err := Parse([]byte(raw)); err == nil {
		t.Fatal("expected unsupported transfer encoding error")
	}
}

func TestBase64CorruptCharacterIsError(t *testing.T) {
	raw := "Content-Transfer-Encoding: base64\r\n\r\nYWJj****\r\n"
	if _, err := Parse([]byte(raw)); err == nil {
		t.Fatal("expected base64 corruption error")
	}
}
