package worklog

import (
	"crypto/sha1"
	"encoding/hex"
	"sort"
	"strings"
)

func Signature(closeReason, commitSHA string, files []string) string {
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	h := sha1.New()
	h.Write([]byte(closeReason))
	h.Write([]byte{0x1f})
	h.Write([]byte(commitSHA))
	h.Write([]byte{0x1f})
	h.Write([]byte(strings.Join(sorted, "\x1f")))
	return hex.EncodeToString(h.Sum(nil))
}
