package mimemsg

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"strings"
)

// limitedCharsetReader 只接受 us-ascii 与 utf-8。遇到别的 charset 返回
// ErrUnsupportedCharset，调用方可以明确感知，不会静默当 UTF-8。
func limitedCharsetReader(charset string, input io.Reader) (io.Reader, error) {
	raw, err := io.ReadAll(input)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(charset) {
	case "us-ascii", "ascii":
		for _, b := range raw {
			if b >= 0x80 {
				return nil, fmt.Errorf("%w: non-ascii byte in us-ascii text", ErrUnsupportedCharset)
			}
		}
		return bytes.NewReader(raw), nil
	case "utf-8", "utf8":
		return bytes.NewReader(raw), nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedCharset, charset)
	}
}

var rfc2047Decoder = &mime.WordDecoder{CharsetReader: limitedCharsetReader}

// decodeEncodedWords 解 RFC 2047 encoded-word。mime.WordDecoder 天然支持
// 同一头值里相邻多段 encoded-word（中间只隔折叠空白时拼接），Q、B 两种
// 编码都走这里。注意标准库内置了 iso-8859-1，在 CharsetReader 之前就放行，
// 所以先用 validateEncodedWordCharsets 把 us-ascii/utf-8 之外的 charset
// 挡掉，未知 charset 一律报错。
func decodeEncodedWords(s string) (string, error) {
	if err := validateEncodedWordCharsets(s); err != nil {
		return "", err
	}
	out, err := rfc2047Decoder.DecodeHeader(s)
	if err != nil {
		return "", err
	}
	return out, nil
}

// validateEncodedWordCharsets 扫描 "=?charset?[qQbB]?..." 形态，
// 确认每个 encoded-word 声明的 charset 都受支持。
func validateEncodedWordCharsets(s string) error {
	for {
		i := strings.Index(s, "=?")
		if i < 0 {
			return nil
		}
		s = s[i+2:]
		q := strings.IndexByte(s, '?')
		if q <= 0 {
			return nil // 不是合法起始，交给标准库按原文处理
		}
		charset := strings.ToLower(s[:q])
		rest := s[q+1:]
		if len(rest) < 2 || (rest[1] != '?') || (rest[0] != 'q' && rest[0] != 'Q' && rest[0] != 'b' && rest[0] != 'B') {
			s = rest
			continue
		}
		switch charset {
		case "us-ascii", "ascii", "utf-8", "utf8":
		default:
			return fmt.Errorf("%w: encoded-word charset %q", ErrUnsupportedCharset, charset)
		}
		s = rest[2:]
	}
}

// encodeEncodedWords 把非 ASCII 头值编码成 RFC 2047 B-encoded-word，
// 按 75 字节一行折叠，段间用 CRLF SPACE。纯 ASCII 原样返回。
func encodeEncodedWords(s string) string {
	if isASCII(s) {
		return s
	}
	var b strings.Builder
	for len(s) > 0 {
		chunk := takeRunes(s, 40)
		s = s[len(chunk):]
		if b.Len() > 0 {
			b.WriteString("\r\n ")
		}
		b.WriteString(mime.BEncoding.Encode("utf-8", chunk))
	}
	return b.String()
}

func takeRunes(s string, maxRunes int) string {
	n := 0
	for i := range s {
		if n == maxRunes {
			return s[:i]
		}
		n++
	}
	return s
}
