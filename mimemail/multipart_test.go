package mimemail

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestSingleAttachmentChineseFilename2231(t *testing.T) {
	data := []byte{1, 2, 3, 4, 255}
	raw := "Content-Type: multipart/mixed; boundary=\"B1\"\r\n\r\n" +
		"preamble\r\n" +
		"--B1\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		"see attachment\r\n" +
		"--B1\r\n" +
		"Content-Type: application/pdf\r\n" +
		"Content-Disposition: attachment;\r\n" +
		" filename*0*=UTF-8''%E6%8A%A5%E5%91%8A;\r\n" +
		" filename*1*=%2Epdf\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		wrapAt76(base64.StdEncoding.EncodeToString(data)) +
		"--B1--\r\n" +
		"epilogue\r\n"
	msg, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(msg.Root.Parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(msg.Root.Parts))
	}
	att := msg.Root.Parts[1]
	if att.FileName != "报告.pdf" {
		t.Fatalf("filename = %q, want 报告.pdf", att.FileName)
	}
	if att.Disposition != "attachment" {
		t.Fatalf("disposition = %q", att.Disposition)
	}
	if !bytes.Equal(att.Body, data) {
		t.Fatalf("attachment bytes mismatch: %v", att.Body)
	}
	roundTripTree(t, msg)
}

func TestNestedThreeDeepMixedAlternativeRelated(t *testing.T) {
	img := bytes.Repeat([]byte{0x89, 'P', 'N', 'G', 0, 1, 2}, 10)
	raw := "Subject: deep\r\n" +
		"Content-Type: multipart/mixed; boundary=MA\r\n\r\n" +
		"--MA\r\n" +
		"Content-Type: multipart/alternative; boundary=ALT\r\n\r\n" +
		"--ALT\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		"plain version\r\n" +
		"--ALT\r\n" +
		"Content-Type: multipart/related; boundary=REL; type=\"text/html\"\r\n\r\n" +
		"--REL\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<img src=\"cid:logo42\">\r\n" +
		"--REL\r\n" +
		"Content-Type: image/png\r\n" +
		"Content-ID: <logo42>\r\n" +
		"Content-Disposition: inline; filename*=UTF-8''%E5%9B%BE%E6%A0%87.png\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		wrapAt76(base64.StdEncoding.EncodeToString(img)) +
		"--REL--\r\n" +
		"--ALT--\r\n" +
		"--MA\r\n" +
		"Content-Type: text/plain\r\n\r\n" +
		"trailer part\r\n" +
		"--MA--\r\n"
	msg, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	mixed := msg.Root
	if mixed.Type != "multipart/mixed" || len(mixed.Parts) != 2 {
		t.Fatalf("mixed tree wrong: %q %d children", mixed.Type, len(mixed.Parts))
	}
	alt := mixed.Parts[0]
	if alt.Type != "multipart/alternative" || len(alt.Parts) != 2 {
		t.Fatalf("alternative tree wrong: %q %d children", alt.Type, len(alt.Parts))
	}
	if alt.Parts[0].Type != "text/plain" {
		t.Fatalf("alternative first child = %q, want text/plain", alt.Parts[0].Type)
	}
	related := alt.Parts[1]
	if related.Type != "multipart/related" || len(related.Parts) != 2 {
		t.Fatalf("related tree wrong: %q %d children", related.Type, len(related.Parts))
	}
	if related.Parts[0].Type != "text/html" {
		t.Fatalf("related first child = %q, want text/html", related.Parts[0].Type)
	}
	image := related.Parts[1]
	if image.Type != "image/png" || image.ContentID != "logo42" {
		t.Fatalf("cid part wrong: type=%q cid=%q", image.Type, image.ContentID)
	}
	if image.FileName != "图标.png" {
		t.Fatalf("image filename = %q", image.FileName)
	}
	if !bytes.Equal(image.Body, img) {
		t.Fatal("image bytes mismatch")
	}
	if mixed.Parts[1].Type != "text/plain" || string(mixed.Parts[1].Body) != "trailer part" {
		t.Fatalf("mixed trailing part wrong: %q", mixed.Parts[1].Body)
	}

	encoded, err := msg.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	reparsed, err := Parse(encoded)
	if err != nil {
		t.Fatalf("reparse: %v\n%s", err, encoded)
	}
	r2 := reparsed.Root
	if r2.Type != "multipart/mixed" ||
		r2.Parts[0].Type != "multipart/alternative" ||
		r2.Parts[0].Parts[0].Type != "text/plain" ||
		r2.Parts[0].Parts[1].Type != "multipart/related" ||
		r2.Parts[0].Parts[1].Parts[1].ContentID != "logo42" ||
		r2.Parts[0].Parts[1].Parts[1].FileName != "图标.png" ||
		!bytes.Equal(r2.Parts[0].Parts[1].Parts[1].Body, img) {
		t.Fatal("three-deep round-trip lost structure, order, cid, filename or bytes")
	}
}

func TestQuotedBoundary(t *testing.T) {
	raw := "Content-Type: multipart/mixed; boundary=\"Q B\"\r\n\r\n" +
		"--Q B\r\nContent-Type: text/plain\r\n\r\na\r\n" +
		"--Q B\r\nContent-Type: text/plain\r\n\r\nb\r\n" +
		"--Q B--\r\n"
	msg, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(msg.Root.Parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(msg.Root.Parts))
	}
}

func TestEmptyOrMissingBoundaryIsError(t *testing.T) {
	cases := []string{
		"Content-Type: multipart/mixed; boundary=\"\"\r\n\r\n--x\r\n",
		"Content-Type: multipart/mixed\r\n\r\n--x\r\n",
	}
	for i, raw := range cases {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Fatalf("case %d: expected boundary error", i)
		}
	}
}

func TestBoundaryNotFoundIsError(t *testing.T) {
	raw := "Content-Type: multipart/mixed; boundary=ZZ\r\n\r\nno delimiters here\r\n"
	if _, err := Parse([]byte(raw)); err == nil {
		t.Fatal("expected boundary-not-found error")
	}
}

func TestMissingClosingBoundaryIsTolerated(t *testing.T) {
	// Pinned behavior: a missing final closing delimiter is tolerated as long
	// as at least one start delimiter was seen.
	raw := "Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nfirst\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nsecond body"
	msg, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(msg.Root.Parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(msg.Root.Parts))
	}
	if got := string(msg.Root.Parts[0].Body); got != "first" {
		t.Fatalf("first body = %q", got)
	}
	if got := string(msg.Root.Parts[1].Body); got != "second body" {
		t.Fatalf("last body = %q", got)
	}

	encoded, err := msg.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	reparsed, err := Parse(encoded)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if len(reparsed.Root.Parts) != 2 {
		t.Fatalf("round-trip segment count = %d, want 2", len(reparsed.Root.Parts))
	}
}
