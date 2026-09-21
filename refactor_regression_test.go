package mimemsg

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

type treeSnapshot struct {
	mediaTypes []string
	filenames  []string
	bodyHashes []string
	childCount []int
}

func snapshotTree(p *Part) treeSnapshot {
	var snap treeSnapshot
	var walk func(*Part)
	walk = func(p *Part) {
		snap.mediaTypes = append(snap.mediaTypes, p.MediaType())
		snap.filenames = append(snap.filenames, p.Filename())
		if len(p.Body) == 0 {
			snap.bodyHashes = append(snap.bodyHashes, "")
		} else {
			sum := sha256.Sum256(p.Body)
			snap.bodyHashes = append(snap.bodyHashes, hex.EncodeToString(sum[:]))
		}
		snap.childCount = append(snap.childCount, len(p.Parts))

		for _, child := range p.Parts {
			walk(child)
		}
		if p.Message != nil {
			walk(p.Message)
		}
	}
	walk(p)
	return snap
}

func assertSnapshotsEqual(t *testing.T, got, want treeSnapshot) {
	t.Helper()

	if len(got.mediaTypes) != len(want.mediaTypes) {
		t.Fatalf("node count = %d, want %d", len(got.mediaTypes), len(want.mediaTypes))
	}
	for i := range want.mediaTypes {
		if got.mediaTypes[i] != want.mediaTypes[i] {
			t.Fatalf("node %d media type = %q, want %q", i, got.mediaTypes[i], want.mediaTypes[i])
		}
		if got.filenames[i] != want.filenames[i] {
			t.Fatalf("node %d filename = %q, want %q", i, got.filenames[i], want.filenames[i])
		}
		if got.bodyHashes[i] != want.bodyHashes[i] {
			t.Fatalf("node %d body hash = %q, want %q", i, got.bodyHashes[i], want.bodyHashes[i])
		}
		if got.childCount[i] != want.childCount[i] {
			t.Fatalf("node %d child count = %d, want %d", i, got.childCount[i], want.childCount[i])
		}
	}
}

// TestRefactorTreeSnapshot 用同一封样例信钉住重构前后语义树：
// 每层嵌套段数、媒体类型、文件名和解码后正文哈希均不得变化。
func TestRefactorTreeSnapshot(t *testing.T) {
	inner := "Content-Type: multipart/mixed; boundary=INNER\r\n\r\n" +
		"--INNER\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n内层说明\r\n" +
		"--INNER\r\nContent-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment; filename=REPORT-1\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\nAAH//mUK\r\n--INNER--\r\n"

	msg := "Content-Type: multipart/mixed; boundary=OUTER\r\n" +
		"Subject: =?utf-8?B?5rWL?=\r\n" +
		" =?utf-8?Q?=E8=AF=95?=\r\n\r\n" +
		"--OUTER\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n说明\r\n" +
		"--OUTER\r\nContent-Type: multipart/alternative; boundary=ALT\r\n\r\n" +
		"--ALT\r\nContent-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
		"=E7=BA=AF=E6=96=87=E6=9C=AC=E7=89=88=E6=9C=AC\r\n" +
		"--ALT\r\nContent-Type: multipart/related; boundary=REL; type=text/html\r\n\r\n" +
		"--REL\r\nContent-Type: text/html; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		"PHA+5L2g5aW9IDxpbWcgc3JjPSJjaWQ6bG9nbyI+PC9wPgo=\r\n" +
		"--REL\r\nContent-Type: image/png\r\nContent-ID: <logo>\r\n" +
		"Content-Disposition: inline;\r\n filename=\"=?utf-8?B?5rWL6K+VLnBuZw==?=\"\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n\r\n\x89PNG\r\n" +
		"--REL--\r\n--ALT--\r\n" +
		"--OUTER\r\nContent-Type: message/rfc822\r\n\r\n" + inner +
		"\r\n--OUTER\r\nContent-Type: application/octet-stream; name=fallback.dat\r\n" +
		"Content-Disposition: attachment;\r\n" +
		" filename*0*=utf-8''%E6%88%91%E7%9A%84;\r\n" +
		" filename*1*=%E6%96%87%E4%BB%B6.bin\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		b64Std([]byte{0x00, 0x01, 0xFF, 0xFE, 'a', '\n', 'b'}) +
		"\r\n--OUTER--\r\n"

	before := mustParse(t, msg)
	encoded, err := Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	after := mustParse(t, string(encoded))
	if got := before.Header.Get("Subject"); got != "测试" {
		t.Fatalf("subject before round-trip = %q", got)
	}
	if got := after.Header.Get("Subject"); got != "测试" {
		t.Fatalf("subject after round-trip = %q", got)
	}

	want := treeSnapshot{
		mediaTypes: []string{
			"multipart/mixed",
			"text/plain",
			"multipart/alternative",
			"text/plain",
			"multipart/related",
			"text/html",
			"image/png",
			"message/rfc822",
			"multipart/mixed",
			"text/plain",
			"application/octet-stream",
			"application/octet-stream",
		},
		filenames: []string{
			"", "", "", "", "", "", "测试.png", "", "", "", "REPORT-1", "我的文件.bin",
		},
		bodyHashes: []string{
			"",
			"4262c45dc797bb0cd5008331ef25b684413859f55b73c71fb4e2c01b645dbbb7",
			"",
			"22b07f53677e7edbd57101f0661a012c3315e045e23292e53a9a3564d134f128",
			"",
			"25956654df3fc2d2d93241b2bc0f66a5bf4e40077ac580cc52977caf0aacd6c4",
			"0f4636c78f65d3639ece5a064b5ae753e3408614a14fb18ab4d7540d2c248543",
			"",
			"",
			"db25ca76cb27d125b1493250ae58e8e0474c8c0d72853b68aa480814ae8be4ad",
			"590fe948a10f6e3e0dc4892c01d06e656f8e1b2f464a43ca8333a45502f41866",
			"477b91afa6b2ae37c377c2125b62dbcb0a297d1a694e06de3c8d56fc28f9165b",
		},
		childCount: []int{4, 0, 2, 0, 2, 0, 0, 0, 2, 0, 0, 0},
	}

	assertSnapshotsEqual(t, snapshotTree(before), want)
	assertSnapshotsEqual(t, snapshotTree(after), want)
}
