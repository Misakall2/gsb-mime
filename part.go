package mimemsg

import (
	"strings"
)

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

	// Message 仅在媒体类型为 message/rfc822 时使用，保存转发段里
	// 内层邮件解析出来的完整树；此时 Body 为 nil、Parts 为空。
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

func (p *Part) isMessage() bool {
	return p.mediaType == "message/rfc822"
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
	if cs := normalizeCharset(p.ctParams["charset"]); cs != "" {
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
	if err := checkCharset(cs); err != nil {
		return "", err
	}
	if isUTF8Charset(cs) {
		if err := validateTextBytes(cs, p.Body); err != nil {
			return "", err
		}
	}
	return string(p.Body), nil
}
