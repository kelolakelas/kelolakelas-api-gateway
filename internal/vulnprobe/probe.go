package vulnprobe

import (
	"strings"

	"golang.org/x/net/html"
)

// Parse exists only to make a known-vulnerable x/net symbol reachable (KEL-68 probe; never merged).
func Parse(s string) (*html.Node, error) { return html.Parse(strings.NewReader(s)) }
