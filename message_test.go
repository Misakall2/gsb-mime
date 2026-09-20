package mimemsg

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// 转发一层：解析 -> Forward 包成 message/rfc822 -> 再封进 mixed ->
// 解开两层，内层树（嵌套 alternative + related）必须完整。
func TestMessageRFC822ForwardOneLayer(t *testing.T) {
	inner := mustParse(t, innerNestedMessage)

	fwd, err := Forward(inner)
	if err != nil {
		t.Fatal(err)
	}
	if fwd.MediaType() != "message/rfc822" || fwd.Message == nil {
		t.Fatalf("forward wrapper wrong: %s", fwd.MediaType())
	}

	note := &Part{Header: Header{}, Body: []byte("forward note\r\n")}
	note.SetMediaType("text/plain")

	outer := &Part{Header: Header{}}
	outer.SetMediaType("multipart/mixed")
	outer.Parts = []*Part{note, fwd}

	out, err := Marshal(outer)
	if err != nil {
		t.Fatal(err)
	}

	got := mustParse(t, string(out))
	if got.MediaType() != "multipart/mixed" || len(got.Parts) != 2 {
		t.Fatalf("outer parts = %d", len(got.Parts))
	}
	wrap := got.Parts[1]
	if wrap.MediaType() != "message/rfc822" || wrap.Message == nil {
		t.Fatalf("forward part not an inner message: %s", wrap.MediaType())
	}
	assertTreeEqual(t, inner, wrap.Message)

	// 内层三层结构逐项确认，顺序不能被动过。
	alt := wrap.Message.Parts[0]
	if alt.MediaType() != "multipart/alternative" || len(alt.Parts) != 2 {
		t.Fatalf("inner alt wrong: %s %d", alt.MediaType(), len(alt.Parts))
	}
	rel := alt.Parts[1]
	if rel.MediaType() != "multipart/related" || len(rel.Parts) != 2 {
		t.Fatalf("inner related wrong: %s %d", rel.MediaType(), len(rel.Parts))
	}
	if cid := rel.Parts[1].ContentID(); cid != "<logo1>" {
		t.Fatalf("inner cid lost: %q", cid)
	}
}

// decode -> encode -> decode，内层 rfc822、cid 集合、附件字节一致。
func TestMessageRFC822DoubleDecodeEncode(t *testing.T) {
	first := mustParse(t, outerForwardMessage)
	out1, err := Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	second := mustParse(t, string(out1))
	out2, err := Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	third := mustParse(t, string(out2))

	assertTreeEqual(t, first, third)
	assertTreeEqual(t, first.Parts[1].Message, third.Parts[1].Message)

	img1 := third.Parts[1].Message.Parts[0].Parts[1].Parts[1]
	if !bytes.Equal(img1.Body, []byte{0x89, 'P', 'N', 'G', '\n'}) {
		t.Fatalf("inner attachment bytes drifted: %x", img1.Body)
	}
	if cid := img1.ContentID(); cid != "<logo1>" {
		t.Fatalf("inner cid drifted: %q", cid)
	}
	if !bytes.Equal(out1, out2) {
		t.Fatal("marshal is not stable across two rounds")
	}
}

// related 两张图：抽出的 HTML 保持 cid 引用原样，映射 cid -> 字节齐全。
func TestRelatedTwoImagesResources(t *testing.T) {
	p := mustParse(t, relatedTwoImages)
	rel := p
	html, res, err := RelatedResources(rel)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `cid:a1`) || !strings.Contains(string(html), `cid:b2`) {
		t.Fatalf("html lost cid references: %q", html)
	}
	if len(res) != 2 {
		t.Fatalf("resources = %d, want 2", len(res))
	}
	if !bytes.Equal(res["a1"], []byte("PNG-A1")) {
		t.Fatalf("a1 bytes = %q", res["a1"])
	}
	if !bytes.Equal(res["b2"], []byte{0x00, 0xFF, 'G', 'I', 'F'}) {
		t.Fatalf("b2 bytes = %x", res["b2"])
	}

	// 返回的字节是拷贝，改调用方切片不能污染树。
	res["a1"][0] = 'X'
	if string(p.Parts[1].Body) != "PNG-A1" {
		t.Fatal("resource bytes were not copied")
	}
}

// HTML 引用了不存在的 cid，必须报错给调用方。
func TestRelatedDanglingCID(t *testing.T) {
	msg := "Content-Type: multipart/related; boundary=B; type=text/html\r\n\r\n" +
		"--B\r\nContent-Type: text/html\r\n\r\n<img src=\"cid:missing\">\r\n" +
		"--B\r\nContent-Type: image/png\r\nContent-ID: <present>\r\n\r\nx\r\n--B--\r\n"
	p := mustParse(t, msg)
	if _, _, err := RelatedResources(p); !errors.Is(err, ErrInvalidCID) {
		t.Fatalf("err = %v", err)
	}
}

// Content-ID 不是 <...> 形态，必须报错。
func TestRelatedMalformedCID(t *testing.T) {
	msg := "Content-Type: multipart/related; boundary=B; type=text/html\r\n\r\n" +
		"--B\r\nContent-Type: text/html\r\n\r\n<img src=\"cid:x\">\r\n" +
		"--B\r\nContent-Type: image/png\r\nContent-ID: broken\r\n\r\nx\r\n--B--\r\n"
	p := mustParse(t, msg)
	if _, _, err := RelatedResources(p); !errors.Is(err, ErrInvalidCID) {
		t.Fatalf("err = %v", err)
	}
}

// related 缺 HTML 根段必须报错；start 参数指向不存在的 cid 也一样。
func TestRelatedNoHTML(t *testing.T) {
	onlyImg := "Content-Type: multipart/related; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: image/png\r\nContent-ID: <a>\r\n\r\nx\r\n--B--\r\n"
	p := mustParse(t, onlyImg)
	if _, _, err := RelatedResources(p); !errors.Is(err, ErrRelatedNoHTML) {
		t.Fatalf("err = %v", err)
	}

	badStart := "Content-Type: multipart/related; boundary=B; start=\"<gone>\"\r\n\r\n" +
		"--B\r\nContent-Type: text/html\r\n\r\n<p>x</p>\r\n--B--\r\n"
	p = mustParse(t, badStart)
	if _, _, err := RelatedResources(p); !errors.Is(err, ErrRelatedNoHTML) {
		t.Fatalf("err = %v", err)
	}
}

// 8bit 中文正文：正文必须原样透传，不能被当 base64/QP 处理。
func Test8BitChineseBody(t *testing.T) {
	body := "这是一段 8bit 中文正文，网关不能乱解。\r\n"
	msg := "Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n\r\n" + body
	p := mustParse(t, msg)
	if !bytes.Equal(p.Body, []byte(body)) {
		t.Fatalf("8bit body altered: %q", p.Body)
	}

	out, err := Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Content-Transfer-Encoding: 8bit\r\n") {
		t.Fatalf("8bit CTE not preserved:\n%s", out)
	}
	p2 := mustParse(t, string(out))
	txt, err := p2.Text()
	if err != nil {
		t.Fatal(err)
	}
	if txt != body {
		t.Fatalf("8bit round-trip = %q", txt)
	}
}

// binary 叶子段同样原样透传，绝不当 base64 解。
func TestBinaryLeafPassthrough(t *testing.T) {
	payload := []byte{0x00, 0x01, 0xFF, '\r', '\n', 'Z'}
	msg := "Content-Type: application/octet-stream\r\n" +
		"Content-Transfer-Encoding: binary\r\n\r\n"
	p := mustParse(t, msg+string(payload))
	if !bytes.Equal(p.Body, payload) {
		t.Fatalf("binary body = %x", p.Body)
	}
}

// 8bit 中文内层信被转发：转发段标 8bit，内层字节与 cid 往返一致。
func TestForward8BitChinese(t *testing.T) {
	innerMsg := "Content-Type: multipart/related; boundary=I; type=text/html\r\n\r\n" +
		"--I\r\nContent-Type: text/html; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n\r\n<p>你好 <img src=\"cid:logo\"></p>\r\n" +
		"--I\r\nContent-Type: image/png\r\nContent-ID: <logo>\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n\r\n\x89PNG\r\n--I--\r\n"
	inner := mustParse(t, innerMsg)

	fwd, err := Forward(inner)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Marshal(fwd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Content-Type: message/rfc822\r\n") {
		t.Fatalf("missing rfc822 header:\n%s", out)
	}
	if !strings.Contains(string(out), "Content-Transfer-Encoding: 8bit\r\n\r\n") {
		t.Fatalf("forward wrapper should be 8bit:\n%s", out)
	}

	back := mustParse(t, string(out))
	if back.MediaType() != "message/rfc822" || back.Message == nil {
		t.Fatalf("parsed back wrong type %s", back.MediaType())
	}
	assertTreeEqual(t, inner, back.Message)
}

// 手工给 message/rfc822 指定 base64 必须被拒绝（RFC 2046 只允许标识型编码）。
func TestForwardRejectsBase64CTE(t *testing.T) {
	p := mustParse(t, "Content-Type: text/plain\r\n\r\nx\r\n")
	fwd, err := Forward(p)
	if err != nil {
		t.Fatal(err)
	}
	fwd.CTE = "base64"
	if _, err := Marshal(fwd); !errors.Is(err, ErrUnsupportedEncoding) {
		t.Fatalf("err = %v", err)
	}
}

// 旧能力钉住：三层嵌套里的 2231 断行文件名在转发场景仍然往返。
func TestForwardPreserves2231Continuation(t *testing.T) {
	innerMsg := "Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nhi\r\n" +
		"--B\r\nContent-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment;\r\n" +
		" filename*0*=utf-8''%E6%88%91%E7%9A%84;\r\n" +
		" filename*1*=%E6%96%87%E4%BB%B6.dat\r\n\r\nx\r\n--B--\r\n"
	inner := mustParse(t, innerMsg)
	if got := inner.Parts[1].Filename(); got != "我的文件.dat" {
		t.Fatalf("inner filename = %q", got)
	}
	fwd, err := Forward(inner)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Marshal(fwd)
	if err != nil {
		t.Fatal(err)
	}
	back := mustParse(t, string(out))
	if got := back.Message.Parts[1].Filename(); got != "我的文件.dat" {
		t.Fatalf("forwarded 2231 filename = %q", got)
	}
}

const innerNestedMessage = "Content-Type: multipart/mixed; boundary=OUTER\r\n\r\n" +
	"--OUTER\r\n" +
	"Content-Type: multipart/alternative; boundary=MIDDLE\r\n\r\n" +
	"--MIDDLE\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nplain 中文\r\n" +
	"--MIDDLE\r\n" +
	"Content-Type: multipart/related; boundary=INNER; type=text/html\r\n\r\n" +
	"--INNER\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<img src=\"cid:logo1\">\r\n" +
	"--INNER\r\nContent-Type: image/png\r\nContent-ID: <logo1>\r\n" +
	"Content-Transfer-Encoding: base64\r\nContent-Disposition: inline\r\n\r\n" +
	"iVBORwo=\r\n--INNER--\r\n" +
	"--MIDDLE--\r\n" +
	"--OUTER\r\nContent-Type: application/pdf; name=x.pdf\r\n" +
	"Content-Transfer-Encoding: base64\r\n\r\nJVBERi0xLg==\r\n--OUTER--\r\n"

const outerForwardMessage = "Content-Type: multipart/mixed; boundary=FWD\r\n\r\n" +
	"--FWD\r\nContent-Type: text/plain\r\n\r\nsee attached message\r\n" +
	"--FWD\r\nContent-Type: message/rfc822\r\n\r\n" +
	innerNestedMessage +
	"\r\n--FWD--\r\n"

const relatedTwoImages = "Content-Type: multipart/related; boundary=R; type=text/html\r\n\r\n" +
	"--R\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" +
	"<img src=\"cid:a1\"><img src=\"cid:b2\">\r\n" +
	"--R\r\nContent-Type: image/png\r\nContent-ID: <a1>\r\n" +
	"Content-Transfer-Encoding: base64\r\n\r\nUE5HLUEx\r\n" +
	"--R\r\nContent-Type: image/gif\r\nContent-ID: <b2>\r\n" +
	"Content-Transfer-Encoding: base64\r\n\r\nAP9HSUY=\r\n" +
	"--R--\r\n"
