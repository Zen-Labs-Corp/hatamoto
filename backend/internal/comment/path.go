package comment

import (
	"fmt"
	"strings"
)

// segWidth is the number of hex digits per path segment (2^48 comments).
const segWidth = 12

// Segment encodes a comment's seq as a fixed-width lowercase hex path segment.
// Segments are joined with ".". The path column is COLLATE "C", where "."
// (0x2E) < "/" (0x2F) < "0" (0x30): a parent sorts before its children and a
// whole subtree sits in the range [root, root+"/").
func Segment(seq int64) string { return fmt.Sprintf("%0*x", segWidth, seq) }

// Depth is the number of segments in a path (0 for "").
func Depth(path string) int {
	if path == "" {
		return 0
	}
	return strings.Count(path, ".") + 1
}

// AncestorPath returns the prefix of path at depth d (1-based), or "" when d < 1.
// A d beyond the path's depth returns the whole path.
func AncestorPath(path string, d int) string {
	if d < 1 {
		return ""
	}
	parts := strings.SplitN(path, ".", d+1)
	if len(parts) <= d {
		return path
	}
	return strings.Join(parts[:d], ".")
}

// ReplyParentPath decides where a reply attaches given the parent's path and
// the max depth: the parent itself while it is shallower than maxDepth, else
// its ancestor at maxDepth-1. "" means the reply becomes a root (maxDepth 1 is flat).
func ReplyParentPath(parentPath string, maxDepth int) string {
	if Depth(parentPath) < maxDepth {
		return parentPath
	}
	return AncestorPath(parentPath, maxDepth-1)
}

// subtreeEnd is the exclusive upper bound of a root's subtree in "C" order.
func subtreeEnd(root string) string { return root + "/" }
