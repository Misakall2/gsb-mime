package mimemsg

import (
	"bytes"
	"fmt"
	"net/textproto"
	"strings"
)

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

// splitHeaderBody 找头与正文之间的空行，支持 CRLF 与裸 LF。
func splitHeaderBody(data []byte) (header, body []byte, ok bool) {
	if idx := bytes.Index(data, []byte("\r\n\r\n")); idx >= 0 {
		return data[:idx], data[idx+4:], true
	}
	if idx := bytes.Index(data, []byte("\n\n")); idx >= 0 {
		return data[:idx], data[idx+2:], true
	}
	return nil, nil, false
}

// unfoldHeaderLines 把 CRLF/LF 切开、以空白续行的折行拼回单行，
// 返回的行不再带行尾。
func unfoldHeaderLines(raw []byte) []string {
	s := strings.ReplaceAll(string(raw), "\r\n", "\n")
	rawLines := strings.Split(s, "\n")
	var lines []string
	for _, ln := range rawLines {
		if ln == "" {
			continue
		}
		if (ln[0] == ' ' || ln[0] == '\t') && len(lines) > 0 {
			lines[len(lines)-1] += " " + strings.TrimSpace(ln)
			continue
		}
		lines = append(lines, ln)
	}
	return lines
}

// parseHeaderBlock 解析头：展开折叠、按第一个冒号切分，抽出
// Content-Type / Content-Disposition / Content-Transfer-Encoding。
func parseHeaderBlock(p *Part, raw []byte) error {
	lines := unfoldHeaderLines(raw)
	for _, line := range lines {
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			return fmt.Errorf("%w: %q", ErrMalformedHeader, trimShort(line))
		}
		key := strings.TrimSpace(line[:colon])
		value := strings.TrimSpace(line[colon+1:])
		if key == "" || strings.ContainsAny(key, " \t") {
			return fmt.Errorf("%w: bad field name %q", ErrMalformedHeader, key)
		}
		ck := textproto.CanonicalMIMEHeaderKey(key)
		switch ck {
		case "Content-Type":
			mt, params, err := parseContentType(value)
			if err != nil {
				return err
			}
			p.mediaType = mt
			p.ctParams = params
		case "Content-Disposition":
			disp, params, err := parseDisposition(value)
			if err != nil {
				return err
			}
			p.disposition = disp
			p.dispParams = params
		case "Content-Transfer-Encoding":
			p.CTE = strings.ToLower(value)
		default:
			if strings.Contains(value, "=?") {
				if _, derr := decodeEncodedWords(value); derr != nil {
					return fmt.Errorf("header %q: %w", key, derr)
				}
			}
			p.Header.Add(ck, value)
		}
	}
	return nil
}

func trimShort(s string) string {
	if len(s) > 40 {
		return s[:40] + "..."
	}
	return s
}

// parseContentType 解析 Content-Type 头值：小写主类型 + 参数表。
// charset 参数的合法性在这里用统一的 charset 政策把关。
func parseContentType(value string) (string, map[string]string, error) {
	main := value
	rest := ""
	if semi := strings.IndexByte(value, ';'); semi >= 0 {
		main = value[:semi]
		rest = value[semi+1:]
	}
	main = strings.ToLower(strings.TrimSpace(main))
	if main == "" || !strings.Contains(main, "/") {
		return "", nil, fmt.Errorf("%w: bad media type %q", ErrMalformedParameter, main)
	}
	if strings.ContainsAny(main, " \t") {
		return "", nil, fmt.Errorf("%w: whitespace in media type %q", ErrMalformedParameter, main)
	}
	params, _, err := parseParameters(rest)
	if err != nil {
		return "", nil, err
	}
	resolved, err := resolveParams(params)
	if err != nil {
		return "", nil, err
	}
	if cs := resolved["charset"]; cs != "" {
		if err := checkCharset(cs); err != nil {
			return "", nil, fmt.Errorf("charset parameter: %w", err)
		}
	}
	return main, resolved, nil
}

// parseDisposition 解析 Content-Disposition 头值：小写主值 + 参数表。
func parseDisposition(value string) (string, map[string]string, error) {
	main := value
	rest := ""
	if semi := strings.IndexByte(value, ';'); semi >= 0 {
		main = value[:semi]
		rest = value[semi+1:]
	}
	disp := strings.ToLower(strings.TrimSpace(main))
	params, _, err := parseParameters(rest)
	if err != nil {
		return "", nil, err
	}
	resolved, err := resolveParams(params)
	if err != nil {
		return "", nil, err
	}
	return disp, resolved, nil
}
