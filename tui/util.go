package tui

import (
	"fmt"
	"strconv"
)

func fmtSscan(s string, n *int) (int, error) { return fmt.Sscan(s, n) }

func fmtInt(n int64) string { return strconv.FormatInt(n, 10) }

func parseInt64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}
