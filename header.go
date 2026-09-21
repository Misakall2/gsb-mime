package mimemsg

import (
	"fmt"
	"net/textproto"
	"strings"
)

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
				if _, derr := decodeHeaderValue(value); derr != nil {
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
	main, rest := splitMainValue(value)
	main = strings.ToLower(strings.TrimSpace(main))
	if main == "" || !strings.Contains(main, "/") {
		return "", nil, fmt.Errorf("%w: bad media type %q", ErrMalformedParameter, main)
	}
	if strings.ContainsAny(main, " \t") {
		return "", nil, fmt.Errorf("%w: whitespace in media type %q", ErrMalformedParameter, main)
	}
	params, err := parseParameterString(rest)
	if err != nil {
		return "", nil, err
	}
	if cs := normalizeCharset(params["charset"]); cs != "" {
		if !isSupportedCharset(cs) {
			return "", nil, fmt.Errorf("%w: charset %q", ErrUnsupportedCharset, cs)
		}
	}
	return main, params, nil
}

func parseDisposition(value string) (string, map[string]string, error) {
	main, rest := splitMainValue(value)
	params, err := parseParameterString(rest)
	if err != nil {
		return "", nil, err
	}
	return strings.ToLower(strings.TrimSpace(main)), params, nil
}

func splitMainValue(value string) (main, rest string) {
	if semi := strings.IndexByte(value, ';'); semi >= 0 {
		return value[:semi], value[semi+1:]
	}
	return value, ""
}
