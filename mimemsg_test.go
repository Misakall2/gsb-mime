package mimemsg

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func mustParse(t *testing.T, data string) *Part {
	t.Helper()
	p, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	return p
}

func assertErrorIs(t *testing.T, err error, target error, ctx ...any) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("expected error %v, got %v ctx=%v", target, err, ctx)
	}
}

func b64Std(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// 1. 纯文本：QP + base64 两种传输编码，QP 软换行必须拼接。
func TestPlainTextTransferDecoding(t *testing.T) {
	msg := "Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
		"caf=C3=A9 =\r\n" +
		"au lait"
	p := mustParse(t, msg)
	body, err := p.Text()
	if err != nil {
		t.Fatal(err)
	}
	if body != "café au lait" {
		t.Fatalf("QP decode = %q", body)
	}

	b64 := "Content-Type: text/plain\r\nContent-Transfer-Encoding: base64\r\n\r\n5rWL6K+V"
	p = mustParse(t, b64)
	body, err = p.Text()
	if err != nil {
		t.Fatal(err)
	}
	if body != "测试" {
		t.Fatalf("base64 decode = %q", body)
	}
}

// 2. 一层附件，字节原样。
func TestSingleAttachment(t *testing.T) {
	payload := []byte{0x00, 0x01, 0xFF, 0xFE, 'a', '\n', 'b'}
	msg := "Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nhello\r\n" +
		"--B\r\nContent-Type: application/octet-stream; name=blob.bin\r\n" +
		"Content-Disposition: attachment; filename=blob.bin\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		b64Std(payload) + "\r\n--B--\r\n"
	p := mustParse(t, msg)
	if len(p.Parts) != 2 {
		t.Fatalf("parts = %d", len(p.Parts))
	}
	att := p.Parts[1]
	if att.Filename() != "blob.bin" || att.Disposition() != "attachment" {
		t.Fatalf("filename=%q disp=%q", att.Filename(), att.Disposition())
	}
	if !bytes.Equal(att.Body, payload) {
		t.Fatalf("attachment bytes mismatch: %v", att.Body)
	}
}

func assertTreeEqual(t *testing.T, a, b *Part) {
	t.Helper()
	if a.MediaType() != b.MediaType() {
		t.Fatalf("media type: %s != %s", a.MediaType(), b.MediaType())
	}
	if len(a.Parts) != len(b.Parts) {
		t.Fatalf("part count under %s: %d != %d", a.MediaType(), len(a.Parts), len(b.Parts))
	}
	if a.isMultipart() {
		for i := range a.Parts {
			assertTreeEqual(t, a.Parts[i], b.Parts[i])
		}
		return
	}
	if !bytes.Equal(a.Body, b.Body) {
		t.Fatalf("body mismatch under %s: %q != %q", a.MediaType(), a.Body, b.Body)
	}
	if a.Filename() != b.Filename() {
		t.Fatalf("filename: %q != %q", a.Filename(), b.Filename())
	}
	if a.ContentID() != b.ContentID() {
		t.Fatalf("cid: %q != %q", a.ContentID(), b.ContentID())
	}
}

// 3. 三层嵌套 mixed -> alternative -> related，cid 图片不丢段，顺序保留。
func TestNestedThreeLevels(t *testing.T) {
	msg := "Content-Type: multipart/mixed; boundary=OUTER\r\n\r\n" +
		"--OUTER\r\n" +
		"Content-Type: multipart/alternative; boundary=MIDDLE\r\n\r\n" +
		"--MIDDLE\r\nContent-Type: text/plain\r\n\r\nplain body\r\n" +
		"--MIDDLE\r\n" +
		"Content-Type: multipart/related; boundary=INNER; type=text/html\r\n\r\n" +
		"--INNER\r\nContent-Type: text/html\r\n\r\n<img src=\"cid:logo1\">\r\n" +
		"--INNER\r\nContent-Type: image/png\r\nContent-ID: <logo1>\r\n" +
		"Content-Transfer-Encoding: base64\r\nContent-Disposition: inline\r\n\r\n" +
		"iVBORwo=\r\n--INNER--\r\n" +
		"--MIDDLE--\r\n" +
		"--OUTER\r\nContent-Type: application/pdf; name=x.pdf\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\nJVBERi0xLg==\r\n--OUTER--\r\n"
	p := mustParse(t, msg)

	if p.MediaType() != "multipart/mixed" || len(p.Parts) != 2 {
		t.Fatalf("root wrong: %s %d", p.MediaType(), len(p.Parts))
	}
	alt := p.Parts[0]
	if alt.MediaType() != "multipart/alternative" || len(alt.Parts) != 2 {
		t.Fatalf("alt wrong: %s %d", alt.MediaType(), len(alt.Parts))
	}
	if alt.Parts[0].MediaType() != "text/plain" || alt.Parts[1].MediaType() != "multipart/related" {
		t.Fatal("alternative 子段顺序被调换")
	}
	rel := alt.Parts[1]
	if rel.MediaType() != "multipart/related" || len(rel.Parts) != 2 {
		t.Fatalf("related wrong: %s %d", rel.MediaType(), len(rel.Parts))
	}
	txt, _ := rel.Parts[0].Text()
	if !strings.Contains(txt, "cid:logo1") {
		t.Fatalf("html body lost: %q", txt)
	}
	img := rel.Parts[1]
	if img.ContentID() != "<logo1>" {
		t.Fatalf("cid = %q", img.ContentID())
	}
	if !bytes.Equal(img.Body, []byte{0x89, 'P', 'N', 'G', '\n'}) {
		t.Fatalf("image bytes = %x", img.Body)
	}

	out, err := Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	assertTreeEqual(t, p, p2)
}

// 4. 中文文件名：RFC 2047 encoded-word 与 RFC 2231 filename* 两条路径。
func TestChineseFilename(t *testing.T) {
	msg := "Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\n" +
		"Content-Type: text/plain\r\n" +
		"Content-Disposition: attachment; filename=\"=?utf-8?B?5rWL6K+VLnR4dA==?=\"\r\n\r\n" +
		"a\r\n" +
		"--B\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment; filename*=utf-8''%E6%8A%A5%E5%91%8A.bin\r\n\r\n" +
		"b\r\n--B--\r\n"
	p := mustParse(t, msg)
	if got := p.Parts[0].Filename(); got != "测试.txt" {
		t.Fatalf("2047 filename = %q", got)
	}
	if got := p.Parts[1].Filename(); got != "报告.bin" {
		t.Fatalf("2231 filename = %q", got)
	}

	out, err := Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	p2 := mustParse(t, string(out))
	if p2.Parts[0].Filename() != "测试.txt" || p2.Parts[1].Filename() != "报告.bin" {
		t.Fatalf("filenames after round-trip: %q %q", p2.Parts[0].Filename(), p2.Parts[1].Filename())
	}
}

// 5. RFC 2231 filename*0* filename*1* 断行拼回。
func TestRFC2231Continuation(t *testing.T) {
	msg := "Content-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment;\r\n" +
		" filename*0*=utf-8''%E6%88%91%E7%9A%84;\r\n" +
		" filename*1*=%E6%96%87%E4%BB%B6.dat\r\n\r\n" +
		"x"
	p := mustParse(t, msg)
	if got := p.Filename(); got != "我的文件.dat" {
		t.Fatalf("continuation filename = %q", got)
	}
	out, err := Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	p2 := mustParse(t, string(out))
	if p2.Filename() != "我的文件.dat" {
		t.Fatalf("round-trip filename = %q", p2.Filename())
	}
}

// 2231 序号跳跃必须报错。
func TestRFC2231BrokenContinuation(t *testing.T) {
	msg := "Content-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment; " +
		"filename*0*=us-ascii''a; filename*2*=b\r\n\r\nx"
	_, err := Parse([]byte(msg))
	assertErrorIs(t, err, ErrMalformedParameter)
}

// 6. boundary：带引号、非法空 boundary、缺收尾。
func TestBoundaryEdgeCases(t *testing.T) {
	quoted := "Content-Type: multipart/mixed; boundary=\"  weird B  \"\r\n\r\n" +
		"--  weird B  \r\nContent-Type: text/plain\r\n\r\nx\r\n" +
		"--  weird B  --\r\n"
	p := mustParse(t, quoted)
	if len(p.Parts) != 1 || string(p.Parts[0].Body) != "x" {
		t.Fatalf("quoted boundary not handled: %+v", p.Parts)
	}

	empty := "Content-Type: multipart/mixed; boundary=\"\"\r\n\r\nbody"
	_, err := Parse([]byte(empty))
	assertErrorIs(t, err, ErrEmptyBoundary)

	missing := "Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nx\r\n"
	_, err = Parse([]byte(missing))
	assertErrorIs(t, err, ErrMissingClosingBoundary)

	// 收尾行粘在正文里、不在行首，不能算数。
	glued := "Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nx --B-- tail"
	_, err = Parse([]byte(glued))
	assertErrorIs(t, err, ErrMissingClosingBoundary)
}

// 7. QP 非法十六进制报错，不 panic。
func TestInvalidQuotedPrintable(t *testing.T) {
	for _, body := range []string{"hello=XY", "abc=", "abc=4x"} {
		msg := "Content-Type: text/plain\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n" + body
		_, err := Parse([]byte(msg))
		assertErrorIs(t, err, ErrInvalidQuotedPrintable, body)
	}
}

// 8. base64 缺填充/损坏报错，不 panic。
func TestInvalidBase64(t *testing.T) {
	for _, body := range []string{"YWJjZA", "YWJjZ", "!!!", "YWJjZA==xxx"} {
		msg := "Content-Type: text/plain\r\nContent-Transfer-Encoding: base64\r\n\r\n" + body
		_, err := Parse([]byte(msg))
		assertErrorIs(t, err, ErrInvalidBase64, body)
	}
}

// 9. 多段 encoded-word 拼在同一头字段里（Q + B 混合）。
func TestMultipleEncodedWords(t *testing.T) {
	msg := "Content-Type: text/plain; charset=utf-8\r\n" +
		"Subject: =?utf-8?Q?=E4=BD=A0=E5=A5=BD?= =?utf-8?B?5rWL6K+V?=\r\n\r\nbody"
	p := mustParse(t, msg)
	if got := p.Header.Get("Subject"); got != "你好测试" {
		t.Fatalf("multi encoded-word = %q", got)
	}
}

// 10. 未知 charset 必须报错，不能静默当 UTF-8。
func TestUnknownCharset(t *testing.T) {
	msg := "Content-Type: text/plain; charset=iso-8859-1\r\n\r\ncaf\xe9"
	_, err := Parse([]byte(msg))
	assertErrorIs(t, err, ErrUnsupportedCharset)

	cont := "Content-Type: text/plain\r\n" +
		"Content-Disposition: attachment; filename*0*=iso-8859-1''caf; filename*1*=%E9\r\n\r\nx"
	_, err = Parse([]byte(cont))
	assertErrorIs(t, err, ErrUnsupportedCharset)

	ew := "Content-Type: text/plain\r\nSubject: =?iso-8859-1?B?Y2Fm?=\r\n\r\nx"
	_, err = Parse([]byte(ew))
	assertErrorIs(t, err, ErrUnsupportedCharset)
}

// 11. alternative 顺序在解-编-解后保持 plain 在前 html 在后。
func TestAlternativeOrderRoundTrip(t *testing.T) {
	msg := "Content-Type: multipart/alternative; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nplain\r\n" +
		"--B\r\nContent-Type: text/html\r\n\r\n<p>html</p>\r\n" +
		"--B--\r\n"
	p := mustParse(t, msg)
	out, err := Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	p2 := mustParse(t, string(out))
	if p2.Parts[0].MediaType() != "text/plain" || p2.Parts[1].MediaType() != "text/html" {
		t.Fatalf("order changed: %s, %s", p2.Parts[0].MediaType(), p2.Parts[1].MediaType())
	}
}

// 12. related 图片 cid 的完整往返（二进制字节 + UTF-8 主题头）。
func TestRelatedCIDRoundTrip(t *testing.T) {
	img := bytes.Repeat([]byte{0xDE, 0xAD, 0xBE, 0xEF}, 100)
	msg := "Content-Type: multipart/related; boundary=B; type=text/html\r\n" +
		"Subject: =?utf-8?B?5rWL6K+V?=\r\n\r\n" +
		"--B\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<img src=\"cid:p1\">\r\n" +
		"--B\r\nContent-Type: image/png\r\nContent-ID: <p1>\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		chunkedBase64(img) + "\r\n--B--\r\n"
	p := mustParse(t, msg)
	if p.Parts[1].ContentID() != "<p1>" {
		t.Fatalf("cid = %q", p.Parts[1].ContentID())
	}
	if !bytes.Equal(p.Parts[1].Body, img) {
		t.Fatal("image bytes lost")
	}
	if p.Header.Get("Subject") != "测试" {
		t.Fatalf("subject = %q", p.Header.Get("Subject"))
	}
	out, err := Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	p2 := mustParse(t, string(out))
	assertTreeEqual(t, p, p2)
	if p2.Header.Get("Subject") != "测试" {
		t.Fatalf("subject after round-trip = %q", p2.Header.Get("Subject"))
	}
}

func chunkedBase64(b []byte) string {
	s := b64Std(b)
	var sb strings.Builder
	for len(s) > 0 {
		n := 76
		if n > len(s) {
			n = len(s)
		}
		sb.WriteString(s[:n])
		sb.WriteString("\r\n")
		s = s[n:]
	}
	return strings.TrimRight(sb.String(), "\r\n")
}

// 13. 不识别的 Content-Transfer-Encoding 必须报错。
func TestUnsupportedCTE(t *testing.T) {
	msg := "Content-Type: text/plain\r\nContent-Transfer-Encoding: x-rot13\r\n\r\nhello"
	_, err := Parse([]byte(msg))
	assertErrorIs(t, err, ErrUnsupportedEncoding)
}

// 14. 纯文本无附件场景的完整往返。
func TestPlainTextRoundTrip(t *testing.T) {
	msg := "Content-Type: text/plain; charset=utf-8\r\n\r\n你好，网关\r\n"
	p := mustParse(t, msg)
	out, err := Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	p2 := mustParse(t, string(out))
	txt, err := p2.Text()
	if err != nil {
		t.Fatal(err)
	}
	if txt != "你好，网关\r\n" {
		t.Fatalf("plain round-trip = %q", txt)
	}
}
