package mimemsg

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

// 对照样例信：一封同时覆盖头折叠、2047、2231 续行、QP/base64/8bit
// 传输编码、三层嵌套与 message/rfc822 转发的邮件。重构前后这封
// 信解析出的树（媒体类型、文件名、正文哈希、子段数量与顺序）
// 必须逐节点一致。
const goldenSampleMessage = "Subject: =?utf-8?B?5L2g5aW9?=\r\n" +
	" =?utf-8?Q?=E4=B8=96=E7=95=8C?=\r\n" +
	"From: =?utf-8?B?5rWL6K+V?= <dev@example.com>\r\n" +
	"X-Long-Header: first part of a very long header value\r\n" +
	"\tsecond folded part\r\n" +
	"Content-Type: multipart/mixed; boundary=GOLD\r\n\r\n" +
	"--GOLD\r\n" +
	"Content-Type: multipart/alternative; boundary=ALT\r\n\r\n" +
	"--ALT\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
	"caf=C3=A9 =\r\nau lait\r\n" +
	"--ALT\r\n" +
	"Content-Type: text/html; charset=utf-8\r\n" +
	"Content-Transfer-Encoding: base64\r\n\r\n" +
	"PHA+5L2g5aW9PC9wPg==\r\n" +
	"--ALT--\r\n" +
	"--GOLD\r\n" +
	"Content-Type: application/octet-stream\r\n" +
	"Content-Disposition: attachment;\r\n" +
	" filename*0*=utf-8''%E6%88%91%E7%9A%84;\r\n" +
	" filename*1*=%E6%96%87%E4%BB%B6.dat\r\n" +
	"Content-Transfer-Encoding: base64\r\n\r\n" +
	"AAH+/mEKYg==\r\n" +
	"--GOLD\r\n" +
	"Content-Type: message/rfc822\r\n\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"Content-Transfer-Encoding: 8bit\r\n" +
	"Content-Disposition: attachment; filename=\"=?utf-8?B?5rWL6K+VLnR4dA==?=\"\r\n\r\n" +
	"内层 8bit 正文\r\n" +
	"--GOLD--\r\n"

// goldenFingerprint 是 goldenSampleMessage 在重构前解析出的树指纹，
// 每行一个节点（先序）：深度、媒体类型、文件名、正文 SHA-256。
// 重构后必须逐字节一致。
const goldenFingerprint = `0|multipart/mixed||-
1|multipart/alternative||-
2|text/plain||7c413039fbb2248e2b18b98e7a8d4d85bdcac7cd79b9477a0923f97e3a1f2b50
2|text/html||cd2bdd085cff1018f1c76e41e9b6f2659c0341d3e8c86fa44f7b544c187224ca
1|application/octet-stream|我的文件.dat|05c98c2feb0327712e7bee29b33bfc34edd874b6dcf898b49a4437a852dd19c4
1|message/rfc822||-
2|text/plain|测试.txt|b4687d266b6acf97d0b4faf9b17c833709a85fd3d3f4fe60c1c2a321561087ae
`

// treeFingerprint 先序遍历 Part 树，逐节点记录深度、媒体类型、
// 文件名与正文 SHA-256（无正文的节点记 "-"）。
func treeFingerprint(p *Part) string {
	var b strings.Builder
	var walk func(n *Part, depth int)
	walk = func(n *Part, depth int) {
		bodyHash := "-"
		if n.Body != nil {
			sum := sha256.Sum256(n.Body)
			bodyHash = hex.EncodeToString(sum[:])
		}
		fmt.Fprintf(&b, "%d|%s|%s|%s\n", depth, n.MediaType(), n.Filename(), bodyHash)
		if n.Message != nil {
			walk(n.Message, depth+1)
		}
		for _, child := range n.Parts {
			walk(child, depth+1)
		}
	}
	walk(p, 0)
	return b.String()
}

// 同一封样例信，重构前后解析出的树指纹必须一致；
// 并且 decode -> encode -> decode 后指纹仍一致（圆回）。
func TestGoldenSampleTreeFingerprint(t *testing.T) {
	p := mustParse(t, goldenSampleMessage)
	if got := treeFingerprint(p); got != goldenFingerprint {
		t.Fatalf("tree fingerprint drifted:\n%s", got)
	}

	out, err := Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	reparsed := mustParse(t, string(out))
	if got := treeFingerprint(reparsed); got != goldenFingerprint {
		t.Fatalf("tree fingerprint after round-trip drifted:\n%s", got)
	}
}
