# gsb-mime

Go standard library only. `go test ./...`

## API

`mimemail.Parse(data []byte) (*mimemail.Message, error)` parses a complete
RFC 822/MIME message into a `Part` tree. `(*Message).Encode() ([]byte, error)`
serializes the same tree back to wire format.

## Behavior

- Nested `multipart/mixed`, `multipart/alternative` and `multipart/related`
  parts are recursed into; child order is preserved.
- Multipart boundaries may be quoted. Empty/missing boundaries and bodies
  that never show a boundary delimiter are errors. A missing final closing
  delimiter is tolerated when at least one start delimiter was present.
- Body transfer encodings `7bit`, `8bit`, `binary`, `quoted-printable` and
  `base64` are decoded. QP soft line breaks are joined; bad QP hexadecimal,
  base64 missing padding and corrupt base64 characters return errors.
- Header fields are unfolded and RFC 2047 `Q`/`B` encoded-words are decoded,
  including several encoded-words that made up one folded field.
- File names are resolved from RFC 2231 `filename*`, `filename*0*`/
  `filename*1*` continuations, plain continuations, `filename` and the
  Content-Type `name` parameter.
- Only `us-ascii` and `utf-8` charsets are decoded; unknown charsets in
  encoded-words and extended parameters are reported as errors.
- Round-trip preserves multipart segment count, child order, media types,
  Content-ID, decoded attachment bytes and UTF-8 file names. Header folding
  whitespace may differ from the input.
