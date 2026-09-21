package mimemsg

import (
	"fmt"
)

// Parse 把一封 MIME 消息解析成树。multipart 节点按原始顺序保留子段
// （mixed / alternative / related 等不做重排），叶子节点正文已经过
// Content-Transfer-Encoding 解码。
//
// 各阶段的分工：头折叠与头块解析在 header.go，2047 encoded-word 在
// encodedword.go，2231 参数续行在 parameter.go，正文传输解码在
// encoding.go，multipart 定界切分在 multipart.go，charset 政策统一
// 在 charset.go；这里只负责把它们串成实体递归与树组装。
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
