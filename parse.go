package mimemsg

import (
	"bytes"
	"fmt"
	"net/textproto"
	"strings"
	"unicode/utf8"
)

func utf8Valid(b []byte) bool { return utf8.Valid(b) }

// Parse 把一封 MIME 消息解析成树。multipart 节点按原始顺序保留子段
// （mixed / alternative / related 等不做重排），叶子节点正文已经过
// Content-Transfer-Encoding 解码。
func Parse(data []byte) (*Part, error) {
	return parseEntity(data)
}

func parseEntity(data []byte) (*Part, error) {
	headerBytes, body, hasHeader := splitHeaderBody(data)
	p := &Part{Header: Header{}}

	if !hasHeader {
		// 没有头字段：按 RFC 2045 默认当 text/plain 整段处理。
		p.mediaType = "text/plain"
		p.Body = append([]byte(nil), data...)
		return p, validateBodyCharset(p)
	}

	if err := parseHeaderBlock(p, headerBytes); err != nil {
		return nil, err
	}

	if p.isMultipart() {
		boundary := p.ctParams["boundary"]
		if boundary == "" {
			if _, present := p.ctParams["boundary"]; !present {
				return nil, fmt.Errorf("%w: multipart entity without boundary parameter", ErrMalformedParameter)
			}
			return nil, ErrEmptyBoundary
		}
		chunks, err := splitParts(body, boundary)
		if err != nil {
			return nil, err
		}
		for _, chunk := range chunks {
			child, err := parseEntity(chunk)
			if err != nil {
				return nil, err
			}
			p.Parts = append(p.Parts, child)
		}
		return p, nil
	}

	decoded, err := decodeTransfer(body, p.CTE)
	if err != nil {
		return nil, err
	}
	p.Body = decoded
	return p, validateBodyCharset(p)
}

// validateBodyCharset 钉住 charset 契约：charset 参数只接受
// us-ascii 与 utf-8（未知值在 parseContentType 阶段已拒绝）。
// 这里再校验正文与声明一致：us-ascii 不得有高位字节；非文本类型
// （image/application/...）的默认 charset 不适用，直接放行二进制；
// text/* 缺省 charset 按 UTF-8 校验。
func validateBodyCharset(p *Part) error {
	explicit := p.ctParams["charset"] != ""
	switch p.Charset() {
	case "us-ascii", "ascii":
		for _, b := range p.Body {
			if b >= 0x80 {
				return fmt.Errorf("%w: non-ascii byte in us-ascii body", ErrUnsupportedCharset)
			}
		}
	case "utf-8", "utf8", "":
		if explicit || strings.HasPrefix(p.mediaType, "text/") {
			if !utf8Valid(p.Body) {
				return errInvalidUTF8
			}
		}
	}
	return nil
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
	if cs := strings.ToLower(resolved["charset"]); cs != "" {
		switch cs {
		case "us-ascii", "ascii", "utf-8", "utf8":
		default:
			return "", nil, fmt.Errorf("%w: charset %q", ErrUnsupportedCharset, cs)
		}
	}
	return main, resolved, nil
}

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

// splitParts 按 boundary 切 multipart 正文。规则钉死如下：
//   - boundary 区分大小写，定界行必须独占一行（CRLF 或裸 LF）；
//   - 第一段之前的 preamble、收尾之后的 epilogue 一律忽略；
//   - 每个 part 去掉定界行自带的那一个行尾；
//   - 输入结束仍没有 "--boundary--" 收尾时返回 ErrMissingClosingBoundary。
func splitParts(body []byte, boundary string) ([][]byte, error) {
	delim := []byte("--" + boundary)
	var parts [][]byte

	// 第一个定界行：可以在输入起点，也可以在 preamble 之后。
	_, firstClose, pos, err := scanDelimiter(body, 0, delim)
	if err != nil {
		return nil, err
	}

	for {
		if firstClose {
			return parts, nil
		}
		start, closing, end, err := scanDelimiter(body, pos, delim)
		if err != nil {
			return nil, err
		}
		chunk := body[pos:start]
		chunk = dropFramingLineEnd(chunk)
		parts = append(parts, append([]byte(nil), chunk...))
		pos = end
		if closing {
			return parts, nil
		}
	}
}

// scanDelimiter 从 from 开始找下一个定界行。定界行必须位于行首
// （输入起点或换行之后），区分大小写。
func scanDelimiter(body []byte, from int, delim []byte) (start int, closing bool, end int, err error) {
	idx := from
	for {
		j := bytes.Index(body[idx:], delim)
		if j < 0 {
			return 0, false, 0, ErrMissingClosingBoundary
		}
		j += idx
		if j > 0 && body[j-1] != '\n' {
			idx = j + len(delim)
			continue
		}
		k := j + len(delim)
		isClose := false
		if k < len(body) && body[k] == '-' && k+1 < len(body) && body[k+1] == '-' {
			isClose = true
			k += 2
		}
		// 定界符后到行尾只允许空白（transport padding）。
		for k < len(body) && (body[k] == ' ' || body[k] == '\t') {
			k++
		}
		switch {
		case k == len(body):
			if !isClose {
				// 输入在非收尾定界行处结束，后面缺一个段且没有收尾。
				return 0, false, 0, ErrMissingClosingBoundary
			}
			return j, true, k, nil
		case body[k] == '\n':
			return j, isClose, k + 1, nil
		case body[k] == '\r' && k+1 < len(body) && body[k+1] == '\n':
			return j, isClose, k + 2, nil
		default:
			idx = j + len(delim)
			continue
		}
	}
}

// dropFramingLineEnd 去掉 part 正文末尾属于定界帧的 CRLF（或裸 LF）。
func dropFramingLineEnd(chunk []byte) []byte {
	if len(chunk) >= 2 && chunk[len(chunk)-2] == '\r' && chunk[len(chunk)-1] == '\n' {
		return chunk[:len(chunk)-2]
	}
	if len(chunk) >= 1 && chunk[len(chunk)-1] == '\n' {
		return chunk[:len(chunk)-1]
	}
	return chunk
}
