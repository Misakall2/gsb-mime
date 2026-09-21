package mimemsg

import (
	"bytes"
)

// splitParts 按 boundary 切 multipart 正文。规则钉死如下：
//   - boundary 区分大小写，定界行必须独占一行（CRLF 或裸 LF）；
//   - 第一段之前的 preamble、收尾之后的 epilogue 一律忽略；
//   - 每个 part 去掉定界行自带的那一个行尾；
//   - 完全没有后续定界行时返回 ErrMissingClosingBoundary；输入恰好结束
//     在最后一个定界行（少收尾 "--"）时宽容视为正常收尾，避免丢最后一段。
func splitParts(body []byte, boundary string) ([][]byte, error) {
	delim := []byte("--" + boundary)
	var parts [][]byte

	// 第一个定界行：可以在输入起点，也可以在 preamble 之后。
	_, firstClose, pos, err := scanDelimiter(body, 0, delim)
	if err != nil {
		return nil, err
	}

	for {
		if firstClose {
			return parts, nil
		}
		start, closing, end, err := scanDelimiter(body, pos, delim)
		if err != nil {
			return nil, err
		}
		chunk := body[pos:start]
		chunk = dropFramingLineEnd(chunk)
		parts = append(parts, append([]byte(nil), chunk...))
		pos = end
		if closing {
			return parts, nil
		}
	}
}

// scanDelimiter 从 from 开始找下一个定界行。定界行必须位于行首
// （输入起点或换行之后），区分大小写。
func scanDelimiter(body []byte, from int, delim []byte) (start int, closing bool, end int, err error) {
	idx := from
	for {
		j := bytes.Index(body[idx:], delim)
		if j < 0 {
			return 0, false, 0, ErrMissingClosingBoundary
		}
		j += idx
		if j > 0 && body[j-1] != '\n' {
			idx = j + len(delim)
			continue
		}
		k := j + len(delim)
		isClose := false
		if k < len(body) && body[k] == '-' && k+1 < len(body) && body[k+1] == '-' {
			isClose = true
			k += 2
		}
		// 定界符后到行尾只允许空白（transport padding）。
		for k < len(body) && (body[k] == ' ' || body[k] == '\t') {
			k++
		}
		switch {
		case k == len(body):
			// 与 mime/multipart 对齐：EOF 上的最后一个 boundary 可缺 "--"。
			return j, true, k, nil
		case body[k] == '\n':
			return j, isClose, k + 1, nil
		case body[k] == '\r' && k+1 < len(body) && body[k+1] == '\n':
			return j, isClose, k + 2, nil
		default:
			idx = j + len(delim)
			continue
		}
	}
}

// dropFramingLineEnd 去掉 part 正文末尾属于定界帧的 CRLF（或裸 LF）。
func dropFramingLineEnd(chunk []byte) []byte {
	if len(chunk) >= 2 && chunk[len(chunk)-2] == '\r' && chunk[len(chunk)-1] == '\n' {
		return chunk[:len(chunk)-2]
	}
	if len(chunk) >= 1 && chunk[len(chunk)-1] == '\n' {
		return chunk[:len(chunk)-1]
	}
	return chunk
}
