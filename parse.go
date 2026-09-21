package mimemsg

import "bytes"

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
		return parseMultipartEntity(p, body)
	}

	decoded, err := decodeTransfer(body, p.CTE)
	if err != nil {
		return nil, err
	}

	if p.isMessage() {
		// message/rfc822 段体本身就是一封完整邮件（含头字段），
		// 递归解析成内层树；message/* 不套 charset 校验。
		inner, err := parseEntity(decoded)
		if err != nil {
			return nil, err
		}
		p.Message = inner
		return p, nil
	}

	p.Body = decoded
	return p, validateBodyCharset(p)
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
