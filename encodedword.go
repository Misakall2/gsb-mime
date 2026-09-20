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

// decodeEncodedWords 解 RFC 2047 encoded-word。mime.WordDecoder 支持
// 同一头值里相邻多段 encoded-word（中间只隔折叠空白时拼接），Q、B 两种
// 编码都走这里。注意标准库内置了 iso-8859-1，在 CharsetReader 之前就放行，
// 所以先用 validateEncodedWordCharsets 把 us-ascii/utf-8 之外的 charset
// 挡掉，未知 charset 一律报错。
//
// 事故修复：现网有些 MTA 在一个 encoded-word 内部折行（词的 token 被
// CRLF+WSP 切成两段）。标准库的 DecodeHeader 不接受这种形态，会把整段
// 原样吐出来，表现就是"缺字"。折叠空白在 encoded-word 内无语义，先做
// mendFoldedEncodedWords 把词内 CRLF+WSP 剔除再交给标准库。
func decodeEncodedWords(s string) (string, error) {
	s = mendFoldedEncodedWords(s)
	if err := validateEncodedWordCharsets(s); err != nil {
		return "", err
	}
	out, err := rfc2047Decoder.DecodeHeader(s)
	if err != nil {
		return "", err
	}
	return out, nil
}

// mendFoldedEncodedWords 把 encoded-word 内部的折叠空白
// （CRLF/LF + 空格/制表符）剔除。encoded-word 的 token 内部不允许出现
// 折叠空白，因此词内出现的折行一定是传输过程切开的，删掉即可拼回。
// 词与词之间（或词与普通文本之间）的空白原样保留，由 DecodeHeader
// 按 RFC 2047 规则决定是否吞并（相邻 encoded-word 之间的空白吞掉，
// 普通文本与词之间的空白保留一个）。
func mendFoldedEncodedWords(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if i+1 >= len(s) || s[i] != '=' || s[i+1] != '?' {
			b.WriteByte(s[i])
			i++
			continue
		}
		// 找到 encoded-word 的起始，定位结束标记 "?="；中间的
		// CRLF/LF 后紧跟 WSP 一律删掉。找不到结束标记就不是完整词，
		// 原样输出剩余部分（交给标准库按文本处理）。
		start := i
		// 结构是 =?charset?enc?...payload...?=，payload 内部允许
		// 出现 '?'（B 编码不会，Q 编码文本可能带），所以先跳过
		// charset / enc 两个问号分隔，再从 payload 末尾找 "?="。
		j := i + 2
		for q := 0; q < 2; q++ {
			for j < len(s) && s[j] != '?' {
				j++
			}
			if j >= len(s) {
				break
			}
			j++ // 跳过 '?'
		}
		end := -1
		for j+1 < len(s) {
			if s[j] == '?' && j+1 < len(s) && s[j+1] == '=' {
				end = j + 2
				break
			}
			j++
		}
		if end < 0 {
			b.WriteString(s[start:])
			break
		}
		// 对词内折叠，先复制一份去掉折叠空白的 token。Q 编码里
		// 软换行位置的 '=' 是转义的一部分，不能随折行一起丢，所以
		// 只删 CRLF/LF 及其后续行的 WSP。
		token := make([]byte, 0, end-start)
		for k := start; k < end; {
			// 折叠 = CRLF 或裸 LF 后紧跟至少一个 WSP。
			nl := 0
			switch {
			case s[k] == '\r' && k+1 < end && s[k+1] == '\n':
				nl = 2
			case s[k] == '\n' || s[k] == '\r':
				nl = 1
			}
			if nl > 0 && k+nl < end && (s[k+nl] == ' ' || s[k+nl] == '\t') {
				k += nl
				for k < end && (s[k] == ' ' || s[k] == '\t') {
					k++
				}
				continue
			}
			token = append(token, s[k])
			k++
		}
		b.Write(token)
		i = end
	}
	return b.String()
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
