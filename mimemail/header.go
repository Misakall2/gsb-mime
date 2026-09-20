package mimemail

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	contentTypeHeader        = "Content-Type"
	contentTransferEncoding  = "Content-Transfer-Encoding"
	contentDispositionHeader = "Content-Disposition"
	contentIDHeader          = "Content-Id"
)

var contentHeaderKeys = map[string]struct{}{
	contentTypeHeader:        {},
	contentTransferEncoding:  {},
	contentDispositionHeader: {},
	contentIDHeader:          {},
}

func withoutContentHeaders(h textproto.MIMEHeader) textproto.MIMEHeader {
	out := make(textproto.MIMEHeader, len(h))
	for k, vs := range h {
		if _, ok := contentHeaderKeys[textproto.CanonicalMIMEHeaderKey(k)]; ok {
			continue
		}
		out[k] = append([]string(nil), vs...)
	}
	return out
}

type headerValue struct {
	value  string
	params map[string]string
}

func parseHeaderValue(line string) (headerValue, error) {
	parts := splitHeaderParameters(line)
	hv := headerValue{value: strings.TrimSpace(parts[0]), params: make(map[string]string)}
	for _, raw := range parts[1:] {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		key, value, ok := strings.Cut(raw, "=")
		if !ok {
			return headerValue{}, fmt.Errorf("malformed header parameter %q", raw)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			return headerValue{}, fmt.Errorf("malformed header parameter %q", raw)
		}
		value, err := unquoteParameterValue(strings.TrimSpace(value))
		if err != nil {
			return headerValue{}, err
		}
		hv.params[key] = value
	}
	return hv, nil
}

func splitHeaderParameters(line string) []string {
	var parts []string
	var start int
	inQuote := false
	escape := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if escape {
			escape = false
			continue
		}
		if c == '\\' {
			escape = true
			continue
		}
		if c == '"' {
			inQuote = !inQuote
			continue
		}
		if c == ';' && !inQuote {
			parts = append(parts, line[start:i])
			start = i + 1
		}
	}
	return append(parts, line[start:])
}

func unquoteParameterValue(v string) (string, error) {
	if v == "" {
		return "", nil
	}
	if v[0] != '"' {
		return strings.TrimRight(v, " \t"), nil
	}
	if len(v) < 2 || v[len(v)-1] != '"' {
		return "", fmt.Errorf("unterminated quoted parameter value %q", v)
	}
	var b strings.Builder
	escape := false
	for _, r := range v[1 : len(v)-1] {
		if escape {
			b.WriteRune(r)
			escape = false
			continue
		}
		if r == '\\' {
			escape = true
			continue
		}
		b.WriteRune(r)
	}
	if escape {
		return "", fmt.Errorf("dangling escape in parameter value %q", v)
	}
	return b.String(), nil
}

// decodeHeaderValue decodes RFC 2047 encoded-words, including multiple
// adjacent Q/B words split across folded lines. Whitespace that separates two
// encoded-words is dropped per RFC 2047 Section 6.2.
func decodeHeaderValue(value string) (string, error) {
	var b strings.Builder
	lastEncoded := false
	i := 0
	for i < len(value) {
		rest := value[i:]
		start := strings.Index(rest, "=?")
		if start < 0 {
			b.WriteString(rest)
			break
		}
		rawBefore := rest[:start]
		token := rest[start:]
		charset, encLetter, encText, tokenLen, ok := splitEncodedWordToken(token)
		if !ok {
			b.WriteString(rawBefore)
			b.WriteString("=?")
			i += start + 2
			lastEncoded = false
			break
		}
		decoded, valid, err := decodeEncodedWordParts(charset, encLetter, encText)
		if err != nil {
			return "", err
		}
		if !valid {
			b.WriteString(rawBefore)
			b.WriteString(token[:tokenLen])
			i += start + tokenLen
			lastEncoded = false
			continue
		}
		if !(lastEncoded && strings.TrimSpace(rawBefore) == "") {
			b.WriteString(rawBefore)
		}
		b.Write(decoded)
		lastEncoded = true
		i += start + tokenLen
	}
	return b.String(), nil
}

// splitEncodedWordToken expects token to start with "=?" and returns the text
// between "=?" and "?=" together with the full token length.
// splitEncodedWordToken parses the leading "=?charset?enc?text?=" prefix of
// token. It returns charset, encoding letter, encoded text and token length.
func splitEncodedWordToken(token string) (charset, enc, text string, tokenLen int, ok bool) {
	// token layout: =? charset ? enc ? text ?=
	if len(token) < 7 || token[0] != '=' || token[1] != '?' {
		return "", "", "", 0, false
	}
	relQ1 := strings.IndexByte(token[2:], '?')
	if relQ1 < 0 {
		return "", "", "", 0, false
	}
	q1 := 2 + relQ1
	if q1+3 >= len(token) {
		return "", "", "", 0, false
	}
	// Encoding is exactly one letter at q1+1; q2 follows at q1+2.
	if token[q1+2] != '?' {
		return "", "", "", 0, false
	}
	q2 := q1 + 2
	relQ3 := strings.Index(token[q2+1:], "?=")
	if relQ3 < 0 {
		return "", "", "", 0, false
	}
	q3 := q2 + 1 + relQ3
	return token[2:q1], token[q1+1 : q2], token[q2+1 : q3], q3 + 2, true
}

func decodeEncodedWordParts(charset, encodingLetter, text string) ([]byte, bool, error) {
	if charset == "" || text == "" || len(encodingLetter) != 1 {
		return nil, false, nil
	}

	var (
		raw []byte
		err error
	)
	switch strings.ToLower(encodingLetter) {
	case "b":
		raw, err = base64.StdEncoding.DecodeString(text)
		if err != nil {
			return nil, false, nil
		}
	case "q":
		raw = decodeQEncoding([]byte(text))
	default:
		return nil, false, nil
	}
	raw, err = decodeCharset(charset, raw)
	if err != nil {
		return nil, true, fmt.Errorf("RFC 2047 encoded-word charset %q: %w", charset, err)
	}
	return raw, true, nil
}

func decodeQEncoding(in []byte) []byte {
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); i++ {
		c := in[i]
		switch {
		case c == '_':
			out = append(out, ' ')
		case c == '=' && i+2 < len(in):
			v, err := strconv.ParseUint(string(in[i+1:i+3]), 16, 8)
			if err != nil {
				// RFC 2047 says to display malformed words literally; the
				// caller already consumes the "=" as ordinary text here.
				out = append(out, c)
			} else {
				out = append(out, byte(v))
				i += 2
			}
		default:
			out = append(out, c)
		}
	}
	return out
}

func decodeCharset(charset string, in []byte) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "", "us-ascii", "ascii":
		for _, c := range in {
			if c > 127 {
				return nil, fmt.Errorf("non-ASCII byte in us-ascii data")
			}
		}
		return append([]byte(nil), in...), nil
	case "utf-8":
		if !utf8.Valid(in) {
			return nil, errors.New("invalid UTF-8 data")
		}
		return append([]byte(nil), in...), nil
	default:
		return nil, fmt.Errorf("unsupported charset %q", charset)
	}
}

// filenameFromParams resolves a file name from a Content-Disposition or
// Content-Type parameter set. Priority:
//
//	filename* short form, then filename*0*... continuation,
//	then filename*0... plain continuation, then filename, then name.
func filenameFromParams(params map[string]string) (string, error) {
	plain := map[int]string{}
	extendedChunks := map[int]string{}
	for key, value := range params {
		switch {
		case key == "filename*":
			extendedChunks[-1] = value
		case strings.HasPrefix(key, "filename*"):
			index, star, ok := parseContinuationIndex(strings.TrimPrefix(key, "filename*"))
			if !ok {
				continue
			}
			if star {
				extendedChunks[index] = value
			} else {
				plain[index] = value
			}
		case key == "name*":
			extendedChunks[-1] = value
		case strings.HasPrefix(key, "name*"):
			index, star, ok := parseContinuationIndex(strings.TrimPrefix(key, "name*"))
			if !ok {
				continue
			}
			if star {
				extendedChunks[index] = value
			} else {
				plain[index] = value
			}
		}
	}
	if single, ok := extendedChunks[-1]; ok && len(extendedChunks) == 1 {
		return decodeSingleExtended(single)
	}
	delete(extendedChunks, -1)
	if len(extendedChunks) > 0 {
		return joinExtendedChunks(extendedChunks)
	}
	if len(plain) > 0 {
		return joinPlainChunks(plain)
	}
	for _, key := range []string{"filename", "name"} {
		if name, ok := params[key]; ok {
			return decodeHeaderValue(name)
		}
	}
	return "", nil
}

func parseContinuationIndex(rest string) (index int, star bool, ok bool) {
	if rest == "" {
		return 0, false, false
	}
	if strings.HasSuffix(rest, "*") {
		star = true
		rest = rest[:len(rest)-1]
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 {
		return 0, false, false
	}
	return n, star, true
}

func decodeSingleExtended(value string) (string, error) {
	charset, raw, err := splitExtendedValue(value, true)
	if err != nil {
		return "", err
	}
	return decodeRFC2231Bytes(charset, raw)
}

func joinExtendedChunks(chunks map[int]string) (string, error) {
	var charset string
	var raw []byte
	for i := 0; i < len(chunks); i++ {
		value, ok := chunks[i]
		if !ok {
			return "", fmt.Errorf("missing RFC 2231 parameter segment %d", i)
		}
		partCharset, partRaw, err := splitExtendedValue(value, i == 0)
		if err != nil {
			return "", err
		}
		if i == 0 {
			charset = partCharset
		}
		raw = append(raw, partRaw...)
	}
	return decodeRFC2231Bytes(charset, raw)
}

// splitExtendedValue splits charset'language'percent-encoded-payload. The
// charset'language' prefix is carried only by segment 0; later segments are
// bare percent-encoded continuations (RFC 2231 Section 7).
func splitExtendedValue(value string, withPrefix bool) (string, []byte, error) {
	if !withPrefix {
		raw, err := percentDecode(value)
		return "", raw, err
	}
	first := strings.IndexByte(value, '\'')
	if first < 0 {
		return "", nil, fmt.Errorf("malformed extended parameter %q", value)
	}
	rest := value[first+1:]
	second := strings.IndexByte(rest, '\'')
	if second < 0 {
		return "", nil, fmt.Errorf("malformed extended parameter %q", value)
	}
	charset := value[:first]
	payload := rest[second+1:]
	raw, err := percentDecode(payload)
	return charset, raw, err
}

func percentDecode(value string) ([]byte, error) {
	unescaped, err := url.PathUnescape(value)
	if err != nil {
		return nil, fmt.Errorf("invalid percent-encoding in %q: %w", value, err)
	}
	return []byte(unescaped), nil
}

func decodeRFC2231Bytes(charset string, raw []byte) (string, error) {
	decoded, err := decodeCharset(charset, raw)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func joinPlainChunks(chunks map[int]string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(chunks); i++ {
		value, ok := chunks[i]
		if !ok {
			return "", fmt.Errorf("missing RFC 2231 parameter segment %d", i)
		}
		decoded, err := decodeHeaderValue(value)
		if err != nil {
			return "", err
		}
		b.WriteString(decoded)
	}
	return b.String(), nil
}

func encodeExtendedFilename(name string) string {
	var b bytes.Buffer
	b.WriteString("UTF-8''")
	for _, r := range name {
		if r < 33 || r > 126 || strings.ContainsRune("\"'()<>@,;:\\/[]?={}", r) {
			for _, c := range []byte(string(r)) {
				fmt.Fprintf(&b, "%%%02X", c)
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
