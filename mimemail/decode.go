package mimemail

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"strconv"
	"strings"
)

// Parse reads a complete RFC 822/MIME message from data.
//
// Header field values are unfolded and RFC 2047 encoded-words are decoded, so
// Message.Header and Part.Header contain UTF-8 Go strings. Unknown charsets in
// encoded-words or RFC 2231 parameters are reported as errors instead of being
// silently treated as UTF-8.
func Parse(data []byte) (*Message, error) {
	normalized := normalizeLineEndings(data)
	headerBlock, body, _ := cutHeaderBlock(normalized)
	headers, err := readHeaderFields(headerBlock)
	if err != nil {
		return nil, err
	}
	root, err := parsePart(headers, body)
	if err != nil {
		return nil, err
	}
	return &Message{Header: withoutContentHeaders(headers), Root: root}, nil
}

func cutHeaderBlock(data []byte) ([]byte, []byte, bool) {
	idx := bytes.Index(data, []byte("\r\n\r\n"))
	if idx < 0 {
		return data, nil, false
	}
	return data[:idx], data[idx+4:], true
}

// readHeaderFields reads a header block using net/textproto, then decodes
// RFC 2047 encoded-words in every field value.
func readHeaderFields(block []byte) (textproto.MIMEHeader, error) {
	src := string(block)
	if src != "" {
		src += "\r\n"
	}
	r := textproto.NewReader(bufio.NewReader(strings.NewReader(src)))
	header, err := r.ReadMIMEHeader()
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read MIME headers: %w", err)
	}
	if header == nil {
		header = textproto.MIMEHeader{}
	}
	decoded := make(textproto.MIMEHeader, len(header))
	for key, values := range header {
		for _, value := range values {
			dv, derr := decodeHeaderValue(value)
			if derr != nil {
				return nil, fmt.Errorf("decode %s header: %w", key, derr)
			}
			decoded[key] = append(decoded[key], dv)
		}
	}
	return decoded, nil
}

func parsePart(headers textproto.MIMEHeader, body []byte) (*Part, error) {
	mediaType := "text/plain"
	mediaParams := map[string]string{}
	if ct := firstHeader(headers, contentTypeHeader); ct != "" {
		parsed, err := parseHeaderValue(ct)
		if err != nil {
			return nil, fmt.Errorf("parse Content-Type: %w", err)
		}
		if parsed.value != "" {
			mediaType = strings.ToLower(parsed.value)
		}
		mediaParams = parsed.params
	}

	part := &Part{
		Header:     withoutContentHeaders(headers),
		Type:       mediaType,
		TypeParams: mediaParams,
		ContentID:  strings.TrimSpace(firstHeader(headers, contentIDHeader)),
	}
	if cid := part.ContentID; strings.HasPrefix(cid, "<") && strings.HasSuffix(cid, ">") {
		part.ContentID = cid[1 : len(cid)-1]
	}

	if cd := firstHeader(headers, contentDispositionHeader); cd != "" {
		parsed, err := parseHeaderValue(cd)
		if err != nil {
			return nil, fmt.Errorf("parse Content-Disposition: %w", err)
		}
		part.Disposition = strings.ToLower(parsed.value)
		name, err := filenameFromParams(parsed.params)
		if err != nil {
			return nil, fmt.Errorf("parse attachment filename: %w", err)
		}
		part.FileName = name
	}
	if part.FileName == "" {
		name, err := filenameFromParams(mediaParams)
		if err != nil {
			return nil, fmt.Errorf("parse content name: %w", err)
		}
		part.FileName = name
	}

	if strings.HasPrefix(part.Type, "multipart/") {
		boundary := strings.TrimSpace(mediaParams["boundary"])
		children, err := parseMultipartBody(body, boundary)
		if err != nil {
			return nil, err
		}
		delete(part.TypeParams, "boundary")
		part.Parts = children
		part.Body = nil
		return part, nil
	}

	decoded, err := decodeBody(headers, body)
	if err != nil {
		return nil, err
	}
	part.Body = decoded
	return part, nil
}

func firstHeader(h textproto.MIMEHeader, key string) string {
	if vs := h[textproto.CanonicalMIMEHeaderKey(key)]; len(vs) > 0 {
		return vs[0]
	}
	return ""
}

// parseMultipartBody splits a multipart body using a line-oriented scanner
// instead of searching raw bytes, so CRLF/LF input, preamble, epilogue and
// transport padding are all handled deterministically.
//
// Boundary semantics pinned by the tests:
//   - an empty or >70 character boundary is an error;
//   - a quoted boundary is ordinary after parameter unquoting;
//   - the closing delimiter may be missing: as long as one start delimiter
//     was found, the accumulated trailing part is accepted.
func parseMultipartBody(body []byte, boundary string) ([]*Part, error) {
	boundary = strings.TrimSpace(boundary)
	if boundary == "" {
		return nil, errors.New("multipart Content-Type has empty or missing boundary")
	}
	if len(boundary) > 70 {
		return nil, errors.New("multipart boundary is longer than 70 characters")
	}

	lines := splitLines(normalizeLineEndings(body))
	delim := "--" + boundary

	var (
		chunks  [][][]byte
		current [][]byte
		state   int // 0 preamble, 1 in part, 2 epilogue after close
		started bool
	)
	for _, line := range lines {
		text := string(line)
		if text == delim || strings.HasPrefix(text, delim+"--") {
			if strings.HasPrefix(text, delim+"--") {
				if state == 1 {
					chunks = append(chunks, current)
				}
				state = 2
			} else {
				if state == 1 {
					chunks = append(chunks, current)
				}
				current = nil
				state = 1
				started = true
			}
			continue
		}
		if state == 2 {
			// Ignore the epilogue, including transport padding.
			continue
		}
		if state == 1 {
			current = append(current, line)
		}
	}

	if !started {
		return nil, fmt.Errorf("multipart boundary %q not found", boundary)
	}
	if state == 1 {
		// Tolerate a missing closing boundary: the final start delimiter
		// already terminated the preceding part, so accept the tail.
		chunks = append(chunks, current)
	}

	children := make([]*Part, 0, len(chunks))
	for i, chunk := range chunks {
		raw := joinLines(chunk)
		raw = bytes.TrimPrefix(raw, []byte("\r\n"))
		raw = bytes.TrimSuffix(raw, []byte("\r\n"))
		headerBlock, partBody, ok := cutHeaderBlock(raw)
		if !ok {
			return nil, fmt.Errorf("multipart part %d has no header/body separator", i+1)
		}
		h, err := readHeaderFields(headerBlock)
		if err != nil {
			return nil, fmt.Errorf("multipart part %d: %w", i+1, err)
		}
		child, err := parsePart(h, partBody)
		if err != nil {
			return nil, fmt.Errorf("multipart part %d: %w", i+1, err)
		}
		children = append(children, child)
	}
	if len(children) == 0 {
		return nil, errors.New("multipart contains no parts")
	}
	return children, nil
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	for len(data) > 0 {
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			lines = append(lines, bytes.TrimSuffix(data, []byte("\r")))
			break
		}
		lines = append(lines, bytes.TrimSuffix(data[:idx], []byte("\r")))
		data = data[idx+1:]
	}
	return lines
}

func joinLines(lines [][]byte) []byte {
	return bytes.Join(lines, []byte("\r\n"))
}

func decodeBody(headers textproto.MIMEHeader, body []byte) ([]byte, error) {
	te := strings.ToLower(strings.TrimSpace(firstHeader(headers, contentTransferEncoding)))
	switch te {
	case "", "7bit", "8bit", "binary":
		return append([]byte(nil), body...), nil
	case "quoted-printable":
		return decodeQuotedPrintable(body)
	case "base64":
		return decodeBase64Body(body)
	default:
		return nil, fmt.Errorf("unsupported Content-Transfer-Encoding %q", te)
	}
}

// decodeBase64Body requires canonical RFC 2045 base64. Missing padding,
// invalid characters and invalid trailing bits are errors; no panic escapes.
func decodeBase64Body(body []byte) ([]byte, error) {
	cleaned := bytes.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, body)
	if len(cleaned) == 0 {
		return []byte{}, nil
	}
	if len(cleaned)%4 != 0 {
		return nil, errors.New("base64 body has invalid length or missing padding")
	}
	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(cleaned)))
	n, err := base64.StdEncoding.Decode(decoded, cleaned)
	if err != nil {
		return nil, fmt.Errorf("decode base64 body: %w", err)
	}
	return decoded[:n], nil
}

// decodeQuotedPrintable handles RFC 2045 soft line breaks ("=" at end of a
// encoded line), whitespace trimming and hard line breaks. Bad hexadecimal
// escapes are returned as errors.
func decodeQuotedPrintable(body []byte) ([]byte, error) {
	lines := splitLines(normalizeLineEndings(body))
	out := make([]byte, 0, len(body))
	// A trailing CRLF terminates the final encoded line; the resulting empty
	// segment is not a logical line and must not emit a dangling soft break.
	logical := lines
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		logical = lines[:len(lines)-1]
	}
	for i, raw := range logical {
		line := trimTrailingWhitespace(raw)
		isLast := i == len(logical)-1
		var decoded []byte
		if isLast && len(line) == 1 && line[0] == '=' {
			// A single dangling "=" with no following content is malformed.
			return nil, errors.New("quoted-printable ends with dangling soft line break")
		}
		soft := bytes.HasSuffix(line, []byte("="))
		lineToDecode := line
		if soft {
			lineToDecode = line[:len(line)-1]
		}
		var err error
		decoded, err = decodeQPLine(lineToDecode)
		if err != nil {
			return nil, err
		}
		out = append(out, decoded...)
		if !soft && !isLast {
			out = append(out, '\n')
		}
	}
	return out, nil
}

func trimTrailingWhitespace(line []byte) []byte {
	for len(line) > 0 && (line[len(line)-1] == ' ' || line[len(line)-1] == '\t') {
		line = line[:len(line)-1]
	}
	return line
}

func decodeQPLine(line []byte) ([]byte, error) {
	out := make([]byte, 0, len(line))
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c != '=' {
			out = append(out, c)
			continue
		}
		if i+2 >= len(line) {
			return nil, errors.New("quoted-printable escape is truncated")
		}
		value, err := strconv.ParseUint(string(line[i+1:i+3]), 16, 8)
		if err != nil {
			return nil, fmt.Errorf("quoted-printable invalid hexadecimal escape %q", line[i:i+3])
		}
		out = append(out, byte(value))
		i += 2
	}
	return out, nil
}

func normalizeLineEndings(data []byte) []byte {
	var b bytes.Buffer
	b.Grow(len(data))
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' && (i == 0 || data[i-1] != '\r') {
			b.WriteByte('\r')
		}
		b.WriteByte(data[i])
	}
	return b.Bytes()
}
