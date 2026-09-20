package mimemsg

import (
	"bytes"
	"errors"
	"testing"
)

// 事故一：同一 Subject 中多个 RFC 2047 encoded-word 的空格和折行。
func TestIncidentFoldedEncodedWords(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"space", "=?utf-8?B?5L2g5aW9?= =?utf-8?B?77yM?= =?utf-8?B?5LiW55WM?=", "你好，世界"},
		{"fold-between", "=?utf-8?B?5L2g5aW9?=\r\n =?utf-8?B?77yM5LiW55WM?=", "你好，世界"},
		{"fold-inside-b-word", "=?utf-8?B?5L2g\r\n 5aW9?= =?utf-8?B?77yM5LiW55WM?=", "你好，世界"},
		{"fold-inside-charset", "=?utf-8\r\n ?B?5L2g5aW9?= =?utf-8?B?5LiW55WM?=", "你好世界"},
		{"tab-fold", "=?utf-8?b?5L2g\r\n\t5aW9?=", "你好"},
		{"double-space", "=?utf-8?B?5L2g5aW9?=  =?utf-8?B?5LiW55WM?=", "你好世界"},
		{"q-words", "=?utf-8?Q?=E4=BD=A0=E5=A5=BD?=\r\n =?utf-8?Q?=E4=B8=96=E7=95=8C?=", "你好世界"},
	}
	for _, c := range cases {
		got, err := decodeEncodedWords(c.raw)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Fatalf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	got, err := decodeEncodedWords("Re: =?utf-8?B?5L2g5aW9?= =?utf-8?B?5LiW55WM?= (fwd)")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Re: 你好世界 (fwd)" {
		t.Fatalf("text around encoded words = %q", got)
	}

	msg := "Subject: =?utf-8?B?5L2g5aW9?= \r\n" +
		" =?utf-8?B?77yM?= =?utf-8?B?5LiW\r\n 55WM?=\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\nbody\r\n"
	p := mustParse(t, msg)
	if got := p.Header.Get("Subject"); got != "你好，世界" {
		t.Fatalf("subject = %q", got)
	}
}

// 事故二：RFC 2231 续行只在第 0 段携带 charset，后续段是原始数据。
func TestIncidentRFC2231SegmentedCharset(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		want    string
		wantErr error
	}{
		{"charset-only-on-seg0", "attachment; filename*0*=utf-8''%E4%B8%AD%E6%96%87; filename*1*=%E6%96%87%E4%BB%B6.txt", "中文文件.txt", nil},
		{"seg1-apostrophes", "attachment; filename*0*=utf-8''a; filename*1*=l'avenir'", "al'avenir'", nil},
		{"seg0-language", "attachment; filename*0*=utf-8'zh'%E6%88%91; filename*1*=%E7%9A%84.dat", "我的.dat", nil},
		{"unsupported", "attachment; filename*0*=iso-8859-1''caf; filename*1*=%E9", "", ErrUnsupportedCharset},
	}
	for _, c := range cases {
		_, params, err := parseDisposition(c.value)
		if c.wantErr != nil {
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("%s: err=%v, want %v", c.name, err, c.wantErr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if params["filename"] != c.want {
			t.Fatalf("%s: got %q, want %q", c.name, params["filename"], c.want)
		}
	}

	msg := "Content-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment;\r\n" +
		" filename*0*=utf-8''%E4%B8%AD%E6%96%87;\r\n" +
		" filename*1*=%E6%96%87%E4%BB%B6.txt\r\n\r\nx"
	p := mustParse(t, msg)
	if got := p.Filename(); got != "中文文件.txt" {
		t.Fatalf("filename = %q", got)
	}
	out, err := Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustParse(t, string(out)).Filename(); got != "中文文件.txt" {
		t.Fatalf("round-trip filename = %q", got)
	}
}

// 事故三：QP 软换行处可能被插入 WSP，下一行也可能直接跟 UTF-8 原始字节。
func TestIncidentQuotedPrintableSoftBreaks(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"space-soft-break", "a= \r\nb\r\n", "ab\r\n"},
		{"tab-soft-break", "a=\t\r\nb\r\n", "ab\r\n"},
		{"multiple-wsp", "a=  \r\nb\r\n", "ab\r\n"},
		{"bare-lf", "a=\nb", "ab"},
		{"then-encoded-utf8", "a=  \r\n=E4=B8=AD\r\n", "a中\r\n"},
		{"then-raw-utf8", "prefix=\r\n\xe4\xb8\xad\xe6\x96\x87\r\n", "prefix中文\r\n"},
		{"split-around-raw-utf8", "=E4=B8=AD=\r\n\xe6\x96\x87\r\n", "中文\r\n"},
		{"normal-line-break", "\xe4\xb8\xad\r\n\xe6\x96\x87\r\n", "中\r\n文\r\n"},
		{"encoded-tab", "x =09\r\ny\r\n", "x \t\r\ny\r\n"},
	}
	for _, c := range cases {
		got, err := decodeStrictQP([]byte(c.in))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if string(got) != c.want {
			t.Fatalf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	for _, in := range []string{"abc=", "=XY", "abc=4x"} {
		if _, err := decodeStrictQP([]byte(in)); !errors.Is(err, ErrInvalidQuotedPrintable) {
			t.Fatalf("%q: err=%v", in, err)
		}
	}

	msg := "Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
		"=E4=BD=A0=E5=A5=BD= \r\n" +
		"\xe4\xb8\x96\xe7\x95\x8c\r\n"
	p := mustParse(t, msg)
	if body, err := p.Text(); err != nil || body != "你好世界\r\n" {
		t.Fatalf("body=%q err=%v", body, err)
	}
}

// 事故四：EOF 上的最后 boundary 缺少收尾两条横线时，不丢段也不报错。
func TestIncidentTruncatedClosingBoundary(t *testing.T) {
	msg := "Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nfirst\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nsecond\r\n" +
		"--B"
	p := mustParse(t, msg)
	if len(p.Parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(p.Parts))
	}
	if got := string(p.Parts[0].Body); got != "first" {
		t.Fatalf("first = %q", got)
	}
	if got := string(p.Parts[1].Body); got != "second" {
		t.Fatalf("last = %q", got)
	}

	bareLF := "Content-Type: multipart/mixed; boundary=B\n\n" +
		"--B\nContent-Type: text/plain\n\nonly\n--B"
	p2 := mustParse(t, bareLF)
	if len(p2.Parts) != 1 || string(p2.Parts[0].Body) != "only" {
		t.Fatalf("bare LF parts = %+v", p2.Parts)
	}

	for _, msg := range []string{
		"Content-Type: multipart/mixed; boundary=B\r\n\r\n--B\r\nContent-Type: text/plain\r\n\r\nx\r\n--Bxx",
		"Content-Type: multipart/mixed; boundary=B\r\n\r\n--B\r\nContent-Type: text/plain\r\n\r\nx\r\n",
	} {
		if _, err := Parse([]byte(msg)); !errors.Is(err, ErrMissingClosingBoundary) {
			t.Fatalf("err=%v, want ErrMissingClosingBoundary", err)
		}
	}
}

// 事故五：alternative 多轮解编码后顺序和正文字节都必须保持。
func TestIncidentAlternativeOrderAndLosslessness(t *testing.T) {
	msg := "Content-Type: multipart/alternative; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
		"=E4=BD=A0=E5=A5=BD=EF=BC=8C=E7=94=A8=E6=88=B7\r\n" +
		"--B\r\nContent-Type: text/html; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		b64Std([]byte("<p>你好，<b>用户</b></p>\r\n")) + "\r\n" +
		"--B--\r\n"
	first := mustParse(t, msg)
	wantPlain := bytes.TrimRight(first.Parts[0].Body, "\r\n")
	wantHTML := bytes.TrimRight(first.Parts[1].Body, "\r\n")

	cur := first
	for round := 0; round < 3; round++ {
		out, err := Marshal(cur)
		if err != nil {
			t.Fatalf("round %d marshal: %v", round, err)
		}
		nxt := mustParse(t, string(out))
		if len(nxt.Parts) != 2 {
			t.Fatalf("round %d parts = %d", round, len(nxt.Parts))
		}
		if nxt.Parts[0].MediaType() != "text/plain" || nxt.Parts[1].MediaType() != "text/html" {
			t.Fatalf("round %d order: %s before %s", round, nxt.Parts[0].MediaType(), nxt.Parts[1].MediaType())
		}
		if got := bytes.TrimRight(nxt.Parts[0].Body, "\r\n"); !bytes.Equal(got, wantPlain) {
			t.Fatalf("round %d plain = %q, want %q", round, got, wantPlain)
		}
		if got := bytes.TrimRight(nxt.Parts[1].Body, "\r\n"); !bytes.Equal(got, wantHTML) {
			t.Fatalf("round %d html = %q, want %q", round, got, wantHTML)
		}
		cur = nxt
	}

	reversed := "Content-Type: multipart/alternative; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/html\r\n\r\n<p>html</p>\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nplain\r\n" +
		"--B--\r\n"
	out, err := Marshal(mustParse(t, reversed))
	if err != nil {
		t.Fatal(err)
	}
	back := mustParse(t, string(out))
	if back.Parts[0].MediaType() != "text/html" || back.Parts[1].MediaType() != "text/plain" {
		t.Fatalf("reversed order normalized: %s before %s", back.Parts[0].MediaType(), back.Parts[1].MediaType())
	}
}
