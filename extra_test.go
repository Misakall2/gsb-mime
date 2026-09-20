package mimemsg

import (
	"bytes"
	"errors"
	"testing"
)

// 裸 LF 分隔的消息也必须能解开。
func TestBareLFInput(t *testing.T) {
	msg := "Content-Type: multipart/mixed; boundary=B\n\n" +
		"--B\nContent-Type: text/plain\n\nhi\n" +
		"--B--\n"
	p := mustParse(t, msg)
	if len(p.Parts) != 1 || string(p.Parts[0].Body) != "hi" {
		t.Fatalf("bare LF parts: %+v", p.Parts)
	}
}

// 空 multipart（第一个定界行就是收尾行）合法。
func TestEmptyMultipart(t *testing.T) {
	p := mustParse(t, "Content-Type: multipart/mixed; boundary=B\r\n\r\n--B--\r\n")
	if len(p.Parts) != 0 {
		t.Fatalf("expected zero parts, got %d", len(p.Parts))
	}
}

// preamble 与 epilogue 不产生段。
func TestPreambleEpilogueIgnored(t *testing.T) {
	msg := "Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"this is preamble\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nx\r\n" +
		"--B--\r\nepilogue after close"
	p := mustParse(t, msg)
	if len(p.Parts) != 1 || string(p.Parts[0].Body) != "x" {
		t.Fatalf("parts: %+v", p.Parts)
	}
}

// 从 Part 树直接构造一封中文附件邮件，再解析回来。
func TestBuildTreeAndRoundTrip(t *testing.T) {
	root := &Part{Header: Header{}}
	root.SetMediaType("multipart/mixed")
	plain := &Part{Body: []byte("正文内容")}
	plain.SetMediaType("text/plain")
	plain.SetContentTypeParam("charset", "utf-8")
	att := &Part{Body: bytes.Repeat([]byte{0xAB, 0xCD}, 300)}
	att.SetMediaType("application/octet-stream")
	att.SetFileName("数据.bin")
	root.Parts = []*Part{plain, att}
	root.Header.Set("Subject", "网关测试")

	out, err := Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	p := mustParse(t, string(out))
	if len(p.Parts) != 2 {
		t.Fatalf("parts = %d", len(p.Parts))
	}
	if txt, _ := p.Parts[0].Text(); txt != "正文内容" {
		t.Fatalf("plain = %q", txt)
	}
	if p.Parts[1].Filename() != "数据.bin" {
		t.Fatalf("filename = %q", p.Parts[1].Filename())
	}
	if !bytes.Equal(p.Parts[1].Body, att.Body) {
		t.Fatal("attachment bytes lost")
	}
	if p.Header.Get("Subject") != "网关测试" {
		t.Fatalf("subject = %q", p.Header.Get("Subject"))
	}
}

// 再封时用不认识的 CTE 必须报错，不能静默产出。
func TestMarshalBadCTE(t *testing.T) {
	p := &Part{mediaType: "text/plain", Body: []byte("hi"), CTE: "x-weird"}
	if _, err := Marshal(p); !errors.Is(err, ErrUnsupportedEncoding) {
		t.Fatalf("err = %v", err)
	}
}

// 畸形头字段必须报错。
func TestMalformedHeader(t *testing.T) {
	if _, err := Parse([]byte("Content-Type text/plain\r\n\r\nx")); !errors.Is(err, ErrMalformedHeader) {
		t.Fatalf("err = %v", err)
	}
}
