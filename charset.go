package mimemsg

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func normalizeCharset(charset string) string {
	return strings.ToLower(charset)
}

func isSupportedCharset(charset string) bool {
	switch normalizeCharset(charset) {
	case "", "us-ascii", "ascii", "utf-8", "utf8":
		return true
	default:
		return false
	}
}

func validateCharsetBytes(charset string, body []byte) error {
	switch normalizeCharset(charset) {
	case "us-ascii", "ascii":
		for _, b := range body {
			if b >= 0x80 {
				return fmt.Errorf("%w: non-ascii byte in us-ascii body", ErrUnsupportedCharset)
			}
		}
	case "utf-8", "utf8", "":
		if !utf8.Valid(body) {
			return errInvalidUTF8
		}
	}
	return nil
}

// validateBodyCharset 钉住 charset 契约：charset 参数只接受
// us-ascii 与 utf-8（未知值在 parseContentType 阶段已拒绝）。
// text/* 缺省 charset 按 UTF-8 校验；非文本类型的默认 charset
// 不适用，直接放行二进制。
func validateBodyCharset(p *Part) error {
	charset := p.ctParams["charset"]
	if !isSupportedCharset(charset) {
		return fmt.Errorf("%w: charset %q", ErrUnsupportedCharset, normalizeCharset(charset))
	}
	if charset != "" || strings.HasPrefix(p.mediaType, "text/") {
		return validateCharsetBytes(charset, p.Body)
	}
	return nil
}
