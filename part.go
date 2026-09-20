package mimemsg

import (
	"errors"
	"fmt"
	"net/textproto"
	"strings"
	"unicode/utf8"
)

var errInvalidUTF8 = errors.New("mimemsg: body is not valid utf-8")

// Part 是 MIME 树的一个节点。叶子节点的 Body 是 Content-Transfer-Encoding
// 解码后的原始字节；multipart 节点的 Parts 是有序的子段，Body 为 nil。
type Part struct {
	// mediaType 是主类型，如 "multipart/mixed"、"text/plain"，全部小写。
	mediaType string
	// ctParams 是解析后的 Content-Type 参数（boundary、charset、name 等）。
	ctParams map[string]string
	// disposition 是 Content-Disposition 主值（attachment / inline）。
	disposition string
	// dispParams 是 Content-Disposition 参数（filename* 等）。
	dispParams map[string]string

	// Header 保留其它头字段，键已走 CanonicalMIMEHeaderKey。
	// Content-Type / Content-Disposition / Content-Transfer-Encoding
	// 不放在这里。
	Header Header

	// CTE 是解析时见到的 Content-Transfer-Encoding。Marshal 时若为空，
	// 会按正文内容自动选择 7bit / quoted-printable / base64。
	CTE string

	Body  []byte
	Parts []*Part

	// Message 仅在 MediaType() == "message/rfc822" 时有意义，持有解开后
	// 的内层整棵树。这类节点的 Body 为 nil，Parts 也为空。
	Message *Part
}

// MediaType 返回小写媒体类型，例如 "multipart/alternative"。
func (p *Part) MediaType() string {
	if p.mediaType == "" {
		return "text/plain"
	}
	return p.mediaType
}

// SetMediaType 设置媒体类型，例如 "multipart/mixed"。
func (p *Part) SetMediaType(mt string) {
	p.mediaType = strings.ToLower(strings.TrimSpace(mt))
}

func (p *Part) isMultipart() bool {
	return strings.HasPrefix(p.mediaType, "multipart/")
}

// ContentTypeParams 返回 Content-Type 参数的副本。
func (p *Part) ContentTypeParams() map[string]string {
	out := make(map[string]string, len(p.ctParams))
	for k, v := range p.ctParams {
		out[k] = v
	}
	return out
}

// SetContentTypeParam 设置一个 Content-Type 参数（例如 charset）。
func (p *Part) SetContentTypeParam(name, value string) {
	if p.ctParams == nil {
		p.ctParams = map[string]string{}
	}
	p.ctParams[strings.ToLower(name)] = value
}

// Charset 返回 Content-Type 中的 charset 参数（小写），缺省为 utf-8。
func (p *Part) Charset() string {
	if cs := strings.ToLower(p.ctParams["charset"]); cs != "" {
		return cs
	}
	return "utf-8"
}

// Disposition 返回 Content-Disposition 主值（小写），没有则为空串。
func (p *Part) Disposition() string {
	return p.disposition
}

// SetDisposition 设置 Content-Disposition 主值，如 "attachment"。
func (p *Part) SetDisposition(d string) {
	p.disposition = strings.ToLower(strings.TrimSpace(d))
}

// Filename 返回附件文件名：优先 Content-Disposition 的 filename/filename*，
// 其次 Content-Type 的 name。所有 RFC 2231 续行已在此之前拼好。
func (p *Part) Filename() string {
	if name := p.dispParams["filename"]; name != "" {
		return name
	}
	return p.ctParams["name"]
}

// SetFileName 设置附件文件名（UTF-8）。序列化时 ASCII 文件名走普通
// filename 参数，非 ASCII走 filename*=utf-8”... 扩展参数。
func (p *Part) SetFileName(name string) {
	if p.dispParams == nil {
		p.dispParams = map[string]string{}
	}
	if p.disposition == "" {
		p.disposition = "attachment"
	}
	p.dispParams["filename"] = name
}

// ContentID 返回 Content-ID 原始值（通常形如 "<cid@host>"）。
func (p *Part) ContentID() string {
	return strings.TrimSpace(p.Header.GetRaw("Content-Id"))
}

// SetContentID 设置 Content-ID（related 段的 cid 目标）。
func (p *Part) SetContentID(cid string) {
	if p.Header == nil {
		p.Header = Header{}
	}
	p.Header.Set("Content-Id", cid)
}

// Text 返回按 charset 解释后的正文。us-ascii 与 utf-8 直接返回；
// UTF-8 字节无效时返回错误，未知 charset 由 Parse 阶段拒绝。
func (p *Part) Text() (string, error) {
	cs := p.Charset()
	switch cs {
	case "", "us-ascii", "ascii", "utf-8", "utf8":
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedCharset, cs)
	}
	if (cs == "utf-8" || cs == "utf8") && !utf8.Valid(p.Body) {
		return "", errInvalidUTF8
	}
	return string(p.Body), nil
}

// Header 是 RFC 2045 头字段的多重映射。
type Header map[string][]string

// Get 取头值并做 RFC 2047 encoded-word 解码（支持同一头字段中
// 相邻多段 =?charset?Q?..?= =?charset?B?..?=，含 Q/B 两种编码和中文）。
func (h Header) Get(key string) string {
	raw := h.GetRaw(key)
	if raw == "" {
		return ""
	}
	decoded, err := decodeEncodedWords(raw)
	if err != nil {
		return raw
	}
	return decoded
}

// GetRaw 取未做 encoded-word 解码的原始头值。
func (h Header) GetRaw(key string) string {
	if h == nil {
		return ""
	}
	v := h[textproto.CanonicalMIMEHeaderKey(key)]
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// Set 用未编码的原始值设置头字段（非 ASCII 值在 Marshal 时编码）。
func (h Header) Set(key, value string) {
	h[textproto.CanonicalMIMEHeaderKey(key)] = []string{value}
}

// Values / Add 支持同名字段多次出现。
func (h Header) Add(key, value string) {
	ck := textproto.CanonicalMIMEHeaderKey(key)
	h[ck] = append(h[ck], value)
}

func (h Header) Values(key string) []string {
	return h[textproto.CanonicalMIMEHeaderKey(key)]
}
