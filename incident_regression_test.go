package mimemsg

import (
	"bytes"
	"errors"
	"testing"
)

// 事故一：同一 Subject 里多个 RFC 2047 encoded-word，中间有空格或
// 折行（极端情况下折行落在 encoded-word token 内部）时，必须无缺字、
// 无多重空格地拼回。
func TestIncidentFoldedEncodedWords(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			"adjacent-b-words-with-space",
			"=?utf-8?B?5L2g5aW9?= =?utf-8?B?77yM?= =?utf-8?B?5LiW55WM?=",
			"你好，世界",
		},
		{
			"fold-between-words",
			"=?utf-8?B?5L2g5aW9?=\r\n =?utf-8?B?77yM5LiW55WM?=",
			"你好，世界",
		},
		{
			"fold-inside-b-word",
			"=?utf-8?B?5L2g\r\n 5aW9?= =?utf-8?B?77yM5LiW55WM?=",
			"你好，世界",
		},
		{
			"fold-inside-charset",
			"=?utf-8\r\n ?B?5L2g5aW9?= =?utf-8?B?5LiW55WM?=",
			"你好世界",
		},
		{
			"tab-fold-inside-b-word",
			"=?utf-8?b?5L2g\r\n\t5aW9?=",
			"你好",
		},
		{
			"fold-between-q-words",
			"=?utf-8?Q?=E4=BD=A0=E5=A5=BD?=\r\n =?utf-8?Q?=E4=B8=96=E7=95=8C?=",
			"你好世界",
		},
		{
			"double-space-between-words",
			"=?utf-8?B?5L2g5aW9?=  =?utf-8?B?5LiW55WM?=",
			"你好世界",
		},
	}
	for _, c := range cases {
		got, err := decodeEncodedWords(c.raw)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.name, err)
		}
		if got != c.want {
			t.Fatalf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	// 词与普通文本之间的空格必须保留，不能因为拼接把空格吞掉。
	got, err := decodeEncodedWords("Re: =?utf-8?B?5L2g5aW9?= =?utf-8?B?5LiW55WM?= (fwd)")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Re: 你好世界 (fwd)" {
		t.Fatalf("text-around-words: %q", got)
	}

	// 端到端：折行 Subject 经过完整 Parse 路径。
	msg := []byte("Subject: =?utf-8?B?5L2g5aW9?= \r\n" +
		" =?utf-8?B?77yM?= =?utf-8?B?5LiW55WM?=\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\nbody\r\n")
	p := mustParse(t, string(msg))
	if s := p.Header.Get("Subject"); s != "你好，世界" {
		t.Fatalf("parsed subject = %q", s)
	}
}

// 事故四：multipart 结束 boundary 少了收尾的两条横线（输入恰好
// 结束在 "--boundary" 处）时，不能丢最后一段，也不能当致命错误；
// 行为与 Go 标准库 mime/multipart 对齐。定界行后还粘着非空白字符
// 或根本没有后续定界行时，依旧报 ErrMissingClosingBoundary。
func TestIncidentTruncatedClosingBoundary(t *testing.T) {
	msg := "Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nfirst\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nsecond\r\n" +
		"--B"
	p := mustParse(t, msg)
	if len(p.Parts) != 2 {
		t.Fatalf("parts = %d, want 2 (last part lost)", len(p.Parts))
	}
	if string(p.Parts[0].Body) != "first" || string(p.Parts[1].Body) != "second" {
		t.Fatalf("bodies = %q, %q", p.Parts[0].Body, p.Parts[1].Body)
	}

	// 单段、裸 LF 同样宽容。
	bareLF := "Content-Type: multipart/mixed; boundary=B\n\n" +
		"--B\nContent-Type: text/plain\n\nonly\n--B"
	p2 := mustParse(t, bareLF)
	if len(p2.Parts) != 1 || string(p2.Parts[0].Body) != "only" {
		t.Fatalf("bare-LF parts = %+v", p2.Parts)
	}

	// 收尾行后还跟垃圾字符：仍然是缺失收尾。
	glued := "Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nx\r\n--Bxx"
	if _, err := Parse([]byte(glued)); !errors.Is(err, ErrMissingClosingBoundary) {
		t.Fatalf("glued EOF: err = %v", err)
	}

	// 完全没有第二个定界行：仍然致命。
	missing := "Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nx\r\n"
	if _, err := Parse([]byte(missing)); !errors.Is(err, ErrMissingClosingBoundary) {
		t.Fatalf("missing delimiter: err = %v", err)
	}
}

// 事故五：alternative 解 -> 编 -> 解（多轮）后，plain 必须还在
// html 前面，且两段媒体类型与正文字节都不漂移，不能顺序对调还宣称
// 往返无损。混合 CTE（QP / base64）与中文正文一起覆盖。
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
			t.Fatalf("round %d order swapped: %s before %s",
				round, nxt.Parts[0].MediaType(), nxt.Parts[1].MediaType())
		}
		if got := bytes.TrimRight(nxt.Parts[0].Body, "\r\n"); !bytes.Equal(got, wantPlain) {
			t.Fatalf("round %d plain = %q, want %q", round, got, wantPlain)
		}
		if got := bytes.TrimRight(nxt.Parts[1].Body, "\r\n"); !bytes.Equal(got, wantHTML) {
			t.Fatalf("round %d html = %q, want %q", round, got, wantHTML)
		}
		cur = nxt
	}

	// 反向顺序（html 在前）同样必须原样保留，库不做"为你好"式重排。
	reversed := "Content-Type: multipart/alternative; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/html\r\n\r\n<p>html</p>\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nplain\r\n" +
		"--B--\r\n"
	rp := mustParse(t, reversed)
	out, err := Marshal(rp)
	if err != nil {
		t.Fatal(err)
	}
	rp2 := mustParse(t, string(out))
	if rp2.Parts[0].MediaType() != "text/html" || rp2.Parts[1].MediaType() != "text/plain" {
		t.Fatalf("reversed order was normalized: %s before %s",
			rp2.Parts[0].MediaType(), rp2.Parts[1].MediaType())
	}
}

// 事故二：RFC 2231 filename*0* / filename*1* 断行，charset'lang'
// 前缀只出现在第 0 段；后续段是纯数据（可能恰好含有单引号），
// 必须先做 percent 解码、按序拼字节，再用首段 charset 统一解释。
func TestIncidentRFC2231SegmentedCharset(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		want    string
		wantErr error
	}{
		{
			"charset-only-on-seg0",
			"attachment; filename*0*=utf-8''%E4%B8%AD%E6%96%87; filename*1*=%E6%96%87%E4%BB%B6.txt",
			"中文文件.txt",
			nil,
		},
		{
			"seg1-contains-apostrophes",
			"attachment; filename*0*=utf-8''a; filename*1*=l'avenir'",
			"al'avenir'",
			nil,
		},
		{
			"seg0-with-language",
			"attachment; filename*0*=utf-8'zh'%E6%88%91; filename*1*=%E7%9A%84.dat",
			"我的.dat",
			nil,
		},
		{
			"unsupported-charset-from-seg0",
			"attachment; filename*0*=iso-8859-1''caf; filename*1*=%E9",
			"",
			ErrUnsupportedCharset,
		},
	}
	for _, c := range cases {
		_, params, err := parseDisposition(c.value)
		if c.wantErr != nil {
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("%s: err = %v, want %v", c.name, err, c.wantErr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.name, err)
		}
		if params["filename"] != c.want {
			t.Fatalf("%s: filename = %q, want %q", c.name, params["filename"], c.want)
		}
	}

	// 端到端往返：拼好的 UTF-8 文件名再封再解，仍然是同一串。
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
	if back := mustParse(t, string(out)); back.Filename() != "中文文件.txt" {
		t.Fatalf("round-trip filename = %q", back.Filename())
	}
}

// 事故三：quoted-printable 软换行位置被网关塞了 WSP（"= \r\n"），
// 以及软换行后紧跟未编码的中文 UTF-8 字节，都要正确拼接，不能烂码
// 或吞换行；同时严格模式仍然拒绝真正的坏十六进制。
func TestIncidentQuotedPrintableSoftBreaks(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"soft-break-with-space", "a= \r\nb\r\n", "ab\r\n"},
		{"soft-break-with-tab", "a=\t\r\nb\r\n", "ab\r\n"},
		{"soft-break-with-multiple-wsp", "a=  \r\nb\r\n", "ab\r\n"},
		{"soft-break-bare-lf", "a=\nb", "ab"},
		{"wsp-soft-break-then-encoded-utf8", "a=  \r\n=E4=B8=AD\r\n", "a中\r\n"},
		{"soft-break-then-raw-utf8", "prefix=\r\n\xe4\xb8\xad\xe6\x96\x87\r\n", "prefix中文\r\n"},
		{"encoded-utf8-soft-break-raw-utf8", "=E4=B8=AD=\r\n\xe6\x96\x87\r\n", "中文\r\n"},
		{"raw-utf8-line-then-normal-line", "\xe4\xb8\xad\r\n\xe6\x96\x87\r\n", "中\r\n文\r\n"},
		{"encoded-tab-at-line-end", "x =09\r\ny\r\n", "x \t\r\ny\r\n"},
	}
	for _, c := range cases {
		got, err := decodeStrictQP([]byte(c.in))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.name, err)
		}
		if string(got) != c.want {
			t.Fatalf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	// 严格性不能丢：真坏的十六进制、EOF 悬挂的等号仍然报错。
	for _, in := range []string{"abc=", "=XY", "abc=4x"} {
		if _, err := decodeStrictQP([]byte(in)); !errors.Is(err, ErrInvalidQuotedPrintable) {
			t.Fatalf("%q: err = %v, want ErrInvalidQuotedPrintable", in, err)
		}
	}

	// 端到端：QP 正文软换行处带 WSP，下一行直接是原始 UTF-8 字节。
	msg := "Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
		"=E4=BD=A0=E5=A5=BD= \r\n" +
		"\xe4\xb8\x96\xe7\x95\x8c\r\n"
	p := mustParse(t, msg)
	if body, err := p.Text(); err != nil || body != "你好世界\r\n" {
		t.Fatalf("qp e2e body=%q err=%v", body, err)
	}
}
