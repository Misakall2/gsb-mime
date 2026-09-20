package mimemsg

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// 转发一层：已解析的信包进 message/rfc822，解开后内层树完整，
// decode -> encode -> decode 后内层 rfc822、cid 集合、附件字节一致。
func TestForwardMessageRFC822OneLayer(t *testing.T) {
	innerRaw := "Content-Type: multipart/related; boundary=IN; type=text/html\r\n" +
		"Subject: =?utf-8?B?5rWL6K+V?=\r\n\r\n" +
		"--IN\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" +
		"<img src=\"cid:logo\"><p>hi</p>\r\n" +
		"--IN\r\nContent-Type: image/png\r\nContent-ID: <logo>\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\niVBORwo=\r\n" +
		"--IN--\r\n"

	inner := mustParse(t, innerRaw)

	envelope := &Part{Header: Header{}}
	envelope.SetMediaType("multipart/mixed")
	intro := &Part{Body: []byte("fwd note")}
	intro.SetMediaType("text/plain")
	envelope.Parts = []*Part{intro, WrapMessage(inner)}

	raw1, err := Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}

	p1 := mustParse(t, string(raw1))
	if len(p1.Parts) != 2 {
		t.Fatalf("envelope parts = %d", len(p1.Parts))
	}
	fwd := p1.Parts[1]
	if fwd.MediaType() != "message/rfc822" || fwd.Message == nil {
		t.Fatalf("forward part: %s message=%v", fwd.MediaType(), fwd.Message)
	}
	if fwd.Body != nil {
		t.Fatalf("message/rfc822 Body should be nil, got %d bytes", len(fwd.Body))
	}
	wantImg := []byte{0x89, 'P', 'N', 'G', '\n'}
	assertInnerRelated(t, fwd.Message, wantImg)

	raw2, err := Marshal(p1)
	if err != nil {
		t.Fatal(err)
	}
	p2 := mustParse(t, string(raw2))
	assertTreeEqual(t, p1, p2)
	assertInnerRelated(t, p2.Parts[1].Message, wantImg)

	raw3, err := Marshal(p2)
	if err != nil {
		t.Fatal(err)
	}
	p3 := mustParse(t, string(raw3))
	assertTreeEqual(t, p2, p3)

	if cte := p2.Parts[1].CTE; cte != "7bit" && cte != "8bit" {
		t.Fatalf("forward CTE = %q", cte)
	}
}

func assertInnerRelated(t *testing.T, inner *Part, img []byte) {
	t.Helper()
	if inner.MediaType() != "multipart/related" || len(inner.Parts) != 2 {
		t.Fatalf("inner: %s parts=%d", inner.MediaType(), len(inner.Parts))
	}
	if inner.Header.Get("Subject") != "测试" {
		t.Fatalf("inner subject = %q", inner.Header.Get("Subject"))
	}
	if inner.Parts[1].ContentID() != "<logo>" {
		t.Fatalf("inner cid = %q", inner.Parts[1].ContentID())
	}
	if !bytes.Equal(inner.Parts[1].Body, img) {
		t.Fatalf("inner image bytes = % x", inner.Parts[1].Body)
	}
}

// 直接解析手写的 message/rfc822 转发信。
func TestParseMessageRFC822(t *testing.T) {
	msg := "Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nfwd\r\n" +
		"--B\r\nContent-Type: message/rfc822\r\n\r\n" +
		"Content-Type: text/plain\r\n\r\ninner body\r\n" +
		"--B--\r\n"
	p := mustParse(t, msg)
	fwd := p.Parts[1]
	if fwd.MediaType() != "message/rfc822" || fwd.Message == nil {
		t.Fatalf("fwd = %s message=%v", fwd.MediaType(), fwd.Message)
	}
	if got := string(fwd.Message.Body); got != "inner body" {
		t.Fatalf("inner body = %q", got)
	}

	out, err := Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	p2 := mustParse(t, string(out))
	if got := string(p2.Parts[1].Message.Body); got != "inner body" {
		t.Fatalf("inner body after round-trip = %q", got)
	}
}

// message/rfc822 段缺内层头区块必须报错；Marshal 时空 Message 也报错。
func TestMessageRFC822MissingInner(t *testing.T) {
	msg := "Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: message/rfc822\r\n\r\nnot a message\r\n" +
		"--B--\r\n"
	_, err := Parse([]byte(msg))
	assertErrorIs(t, err, ErrMissingInnerMessage)

	bad := &Part{Header: Header{}}
	bad.SetMediaType("message/rfc822")
	if _, err := Marshal(bad); !errors.Is(err, ErrMissingInnerMessage) {
		t.Fatalf("marshal err = %v", err)
	}
}

// message/rfc822 上声明 base64 / QP 属于非法 CTE，不能乱解乱封。
func TestMessageRFC822BadCTE(t *testing.T) {
	msg := "Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: message/rfc822\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		"Content-Type: text/plain\r\n\r\nx\r\n" +
		"--B--\r\n"
	_, err := Parse([]byte(msg))
	assertErrorIs(t, err, ErrUnsupportedEncoding)

	wrapped := WrapMessage(mustParse(t, "Content-Type: text/plain\r\n\r\nx"))
	wrapped.CTE = "quoted-printable"
	if _, err := Marshal(wrapped); !errors.Is(err, ErrUnsupportedEncoding) {
		t.Fatalf("marshal err = %v", err)
	}
}

// related 两张图：RelatedHTML 给出 cid -> 字节映射，HTML 里的 cid
// 引用原样保留；8bit/binary 图片字节不能被当 base64 解。
func TestRelatedHTMLTwoImages(t *testing.T) {
	img1 := bytes.Repeat([]byte{0xDE, 0xAD}, 30)
	img2 := bytes.Repeat([]byte{0x89, 0x00, 0xFF}, 20)
	msg := "Content-Type: multipart/related; boundary=B; type=text/html\r\n\r\n" +
		"--B\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" +
		"<img src=\"cid:a1\"><br><img src=cid:a2>\r\n" +
		"--B\r\nContent-Type: image/png\r\nContent-ID: <a1>\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n\r\n" +
		string(img1) + "\r\n" +
		"--B\r\nContent-Type: image/gif\r\nContent-ID: <a2>\r\n" +
		"Content-Transfer-Encoding: binary\r\n\r\n" +
		string(img2) + "\r\n" +
		"--B--\r\n"
	p := mustParse(t, msg)

	html, res, err := p.RelatedHTML("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "cid:a1") ||
		!strings.Contains(string(html), "cid:a2") {
		t.Fatalf("html cid refs rewritten: %q", html)
	}
	if !bytes.Equal(res["a1"], img1) {
		t.Fatalf("a1 bytes mismatch (%d)", len(res["a1"]))
	}
	if !bytes.Equal(res["a2"], img2) {
		t.Fatalf("a2 bytes mismatch (%d)", len(res["a2"]))
	}

	out, err := Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	p2 := mustParse(t, string(out))
	_, res2, err := p2.RelatedHTML("")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(res2["a1"], img1) || !bytes.Equal(res2["a2"], img2) {
		t.Fatal("cid bytes changed after round-trip")
	}
}

// RelatedHTML 的 start 参数（带不带尖括号都行）。
func TestRelatedHTMLExplicitStart(t *testing.T) {
	msg := "Content-Type: multipart/related; boundary=B; type=text/html; start=\"<root>\"\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nignored\r\n" +
		"--B\r\nContent-Type: text/html\r\nContent-ID: <root>\r\n\r\n<img src=cid:x>\r\n" +
		"--B\r\nContent-Type: image/png\r\nContent-ID: <x>\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\niVBORwo=\r\n" +
		"--B--\r\n"
	p := mustParse(t, msg)

	if _, _, err := p.RelatedHTML("<root>"); err != nil {
		t.Fatalf("start with angle brackets: %v", err)
	}
	if _, _, err := p.RelatedHTML("root"); err != nil {
		t.Fatalf("start bare: %v", err)
	}
	if _, _, err := p.RelatedHTML("missing"); !errors.Is(err, ErrMissingHTMLPart) {
		t.Fatalf("missing start err = %v", err)
	}
}

// 不是 related、缺 html、cid 非法/重复/悬挂/空引用，错误都回调用方。
func TestRelatedHTMLErrors(t *testing.T) {
	plain := mustParse(t, "Content-Type: text/plain\r\n\r\nx")
	if _, _, err := plain.RelatedHTML(""); !errors.Is(err, ErrNotRelated) {
		t.Fatalf("not related err = %v", err)
	}

	noHTML := "Content-Type: multipart/related; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nno html\r\n" +
		"--B\r\nContent-Type: image/png\r\nContent-ID: <x>\r\n\r\nx\r\n" +
		"--B--\r\n"
	if _, _, err := mustParse(t, noHTML).RelatedHTML(""); !errors.Is(err, ErrMissingHTMLPart) {
		t.Fatalf("missing html err = %v", err)
	}

	badCID := "Content-Type: multipart/related; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/html\r\n\r\n<img src=cid:x>\r\n" +
		"--B\r\nContent-Type: image/png\r\nContent-ID: broken\r\n\r\nx\r\n" +
		"--B--\r\n"
	if _, _, err := mustParse(t, badCID).RelatedHTML(""); !errors.Is(err, ErrInvalidCID) {
		t.Fatalf("bad cid err = %v", err)
	}

	dupCID := "Content-Type: multipart/related; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/html\r\n\r\n<img src=cid:x>\r\n" +
		"--B\r\nContent-Type: image/png\r\nContent-ID: <x>\r\n\r\na\r\n" +
		"--B\r\nContent-Type: image/gif\r\nContent-ID: <x>\r\n\r\nb\r\n" +
		"--B--\r\n"
	if _, _, err := mustParse(t, dupCID).RelatedHTML(""); !errors.Is(err, ErrInvalidCID) {
		t.Fatalf("dup cid err = %v", err)
	}

	dangling := "Content-Type: multipart/related; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/html\r\n\r\n<img src=\"cid:ghost\">\r\n" +
		"--B\r\nContent-Type: image/png\r\nContent-ID: <x>\r\n\r\na\r\n" +
		"--B--\r\n"
	if _, _, err := mustParse(t, dangling).RelatedHTML(""); !errors.Is(err, ErrInvalidCID) {
		t.Fatalf("dangling cid err = %v", err)
	}

	emptyRef := "Content-Type: multipart/related; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/html\r\n\r\n<img src=\"cid:\">\r\n" +
		"--B\r\nContent-Type: image/png\r\nContent-ID: <x>\r\n\r\na\r\n" +
		"--B--\r\n"
	if _, _, err := mustParse(t, emptyRef).RelatedHTML(""); !errors.Is(err, ErrInvalidCID) {
		t.Fatalf("empty cid err = %v", err)
	}
}

// 8bit 中文正文与 binary 附件：原样透传，不当 base64 乱解。
// 解-编-解后段数与字节一致；含 8bit 中文的内层转发自动选 8bit CTE。
func Test8BitChineseBody(t *testing.T) {
	// 段尾最后一个 CRLF 属于 multipart 定界帧（既有规则），不进 Body。
	chinese := "中文正文 8bit\r\n第二行"
	bin := []byte{0x00, 0x01, 0xFF, 0xFE, 0xE4, 0xB8, 0xAD}
	raw := []byte("Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n\r\n" +
		chinese + "\r\n" +
		"--B\r\nContent-Type: application/octet-stream\r\n" +
		"Content-Transfer-Encoding: binary\r\n\r\n")
	raw = append(raw, bin...)
	raw = append(raw, []byte("\r\n--B--\r\n")...)

	p := mustParse(t, string(raw))
	if p.Parts[0].CTE != "8bit" || p.Parts[1].CTE != "binary" {
		t.Fatalf("cte = %q, %q", p.Parts[0].CTE, p.Parts[1].CTE)
	}
	if got, err := p.Parts[0].Text(); err != nil || got != chinese {
		t.Fatalf("8bit text = %q err=%v", got, err)
	}
	if !bytes.Equal(p.Parts[1].Body, bin) {
		t.Fatalf("binary body = % x", p.Parts[1].Body)
	}

	out, err := Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	p2 := mustParse(t, string(out))
	if len(p2.Parts) != 2 {
		t.Fatalf("parts after round-trip = %d", len(p2.Parts))
	}
	if p2.Parts[0].CTE != "8bit" || p2.Parts[1].CTE != "binary" {
		t.Fatalf("cte after round-trip = %q, %q", p2.Parts[0].CTE, p2.Parts[1].CTE)
	}
	if got, _ := p2.Parts[0].Text(); got != chinese {
		t.Fatalf("8bit text after round-trip = %q", got)
	}
	if !bytes.Equal(p2.Parts[1].Body, bin) {
		t.Fatal("binary bytes changed after round-trip")
	}

	// 含 8bit 中文的信再包 message/rfc822：封装 CTE 自动选 8bit，
	// 内层中文原样回来。
	env := &Part{Header: Header{}}
	env.SetMediaType("multipart/mixed")
	env.Parts = []*Part{WrapMessage(p)}
	envRaw, err := Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	envP := mustParse(t, string(envRaw))
	fwd := envP.Parts[0]
	if fwd.CTE != "8bit" {
		t.Fatalf("forward CTE = %q, want 8bit", fwd.CTE)
	}
	if got, _ := fwd.Message.Parts[0].Text(); got != chinese {
		t.Fatalf("forwarded inner text = %q", got)
	}
	if !bytes.Equal(fwd.Message.Parts[1].Body, bin) {
		t.Fatal("forwarded inner binary bytes changed")
	}
}

// 转发路径里 RFC 2231 续行文件名仍然完整：内层附件用 filename*0*/
// filename*1* 断行，转发解-编-解后文件名拼回。
func TestForwardPreserves2231Continuation(t *testing.T) {
	innerRaw := "Content-Type: multipart/mixed; boundary=IN\r\n\r\n" +
		"--IN\r\nContent-Type: text/plain\r\n\r\nhi\r\n" +
		"--IN\r\nContent-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment;\r\n" +
		" filename*0*=utf-8''%E6%88%91%E7%9A%84;\r\n" +
		" filename*1*=%E6%96%87%E4%BB%B6.dat\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\niVBORwo=\r\n" +
		"--IN--\r\n"
	inner := mustParse(t, innerRaw)
	if got := inner.Parts[1].Filename(); got != "我的文件.dat" {
		t.Fatalf("inner filename = %q", got)
	}

	env := &Part{Header: Header{}}
	env.SetMediaType("multipart/mixed")
	env.Parts = []*Part{WrapMessage(inner)}
	envRaw, err := Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	p := mustParse(t, string(envRaw))
	att := p.Parts[0].Message.Parts[1]
	if att.Filename() != "我的文件.dat" {
		t.Fatalf("forwarded filename = %q", att.Filename())
	}
	if !bytes.Equal(att.Body, []byte{0x89, 'P', 'N', 'G', '\n'}) {
		t.Fatalf("forwarded attachment bytes = % x", att.Body)
	}
}
