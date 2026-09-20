package mimemsg

import (
	"encoding/base64"
	"fmt"
	"io"
	"mime/quotedprintable"
	"strings"
)

// decodeTransfer 按 Content-Transfer-Encoding 解码叶子段正文。
// 不识别的编码、损坏的 base64（含缺填充）、非法 QP 转义都返回错误，
// 不 panic。
func decodeTransfer(body []byte, cte string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "", "7bit", "8bit", "binary":
		return body, nil
	case "base64":
		return decodeBase64Body(body)
	case "quoted-printable":
		return decodeStrictQP(body)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedEncoding, cte)
	}
}

// decodeBase64Body 去掉软换行后用严格模式解码：缺填充、字母表外字符、
// 尾部多余字节全部报错。
func decodeBase64Body(body []byte) ([]byte, error) {
	cleaned := make([]byte, 0, len(body))
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimRight(line, "\r")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		cleaned = append(cleaned, line...)
	}
	out := make([]byte, base64.StdEncoding.DecodedLen(len(cleaned)))
	n, err := base64.StdEncoding.Strict().Decode(out, cleaned)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBase64, err)
	}
	return out[:n], nil
}

// decodeStrictQP 在标准库 QP 解码之外，额外钉住三件事：
//   - "=" 后必须紧跟两个十六进制数字（软换行 "=\r\n" 除外）；
//   - 输入不得包含裸 '\r'（必须 \r\n 成对）。
//   - 未编码的行尾空白按 RFC 2045 丢弃。
//
// 标准库对非法十六进制是放行的，网关场景需要明确报错。
func decodeStrictQP(body []byte) ([]byte, error) {
	var cleaned []byte
	lineStart := 0
	for i := 0; i < len(body); i++ {
		if body[i] == '\r' {
			if i+1 >= len(body) || body[i+1] != '\n' {
				return nil, fmt.Errorf("%w: bare CR", ErrInvalidQuotedPrintable)
			}
			cleaned = appendQPTrailing(cleaned, body[lineStart:i])
			cleaned = append(cleaned, '\r', '\n')
			i++
			lineStart = i + 1
			continue
		}
		if body[i] == '\n' {
			cleaned = appendQPTrailing(cleaned, body[lineStart:i])
			cleaned = append(cleaned, '\n')
			lineStart = i + 1
			continue
		}
		if body[i] != '=' {
			continue
		}
		// 软换行。
		if i+1 < len(body) && body[i+1] == '\n' {
			i++
			continue
		}
		if i+2 < len(body) && body[i+1] == '\r' && body[i+2] == '\n' {
			i += 2
			continue
		}
		// 必须是两个十六进制数字。
		if i+2 >= len(body) || !isHex(body[i+1]) || !isHex(body[i+2]) {
			return nil, fmt.Errorf("%w: bad hex escape at byte %d", ErrInvalidQuotedPrintable, i)
		}
		i += 2
	}
	cleaned = appendQPTrailing(cleaned, body[lineStart:])
	out, err := readAllQP(cleaned)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidQuotedPrintable, err)
	}
	return out, nil
}

// appendQPTrailing 追加一行时去掉未编码的尾部空格/制表符。
func appendQPTrailing(dst, line []byte) []byte {
	end := len(line)
	for end > 0 && (line[end-1] == ' ' || line[end-1] == '\t') {
		// 已编码的空白（=20 / =09）不是裸空白，必须保留。
		if end >= 3 && line[end-3] == '=' {
			break
		}
		end--
	}
	return append(dst, line[:end]...)
}

func readAllQP(body []byte) ([]byte, error) {
	r := quotedprintable.NewReader(strings.NewReader(string(body)))
	buf := make([]byte, 0, len(body))
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			if err == io.EOF {
				return buf, nil
			}
			return buf, err
		}
	}
}

// chooseCTE 为叶子段选择重新编码时使用的 Content-Transfer-Encoding。
// 纯 ASCII 短行用 7bit；文本类用 quoted-printable（保留可读性）；
// 其余一律 base64。
func chooseCTE(mediaType string, body []byte) string {
	if is7BitLineSafe(body) {
		return "7bit"
	}
	if strings.HasPrefix(mediaType, "text/") {
		return "quoted-printable"
	}
	return "base64"
}

func is7BitLineSafe(body []byte) bool {
	for _, b := range body {
		if b == 0 || b >= 0x80 {
			return false
		}
	}
	for _, line := range strings.Split(string(body), "\n") {
		if len(strings.TrimRight(line, "\r")) > 998 {
			return false
		}
	}
	return true
}

// encodeTransfer 按指定 CTE 编码叶子正文。
func encodeTransfer(body []byte, cte string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "", "7bit", "8bit", "binary":
		return body, nil
	case "base64":
		return []byte(base64.StdEncoding.EncodeToString(body)), nil
	case "quoted-printable":
		var buf strings.Builder
		w := quotedprintable.NewWriter(&buf)
		if _, err := w.Write(body); err != nil {
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		return []byte(buf.String()), nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedEncoding, cte)
	}
}
