// Package jsonc reads and edits JSON-with-comments files without destroying the
// user's comments or formatting. Edits are made by text insertion, never by
// re-serialising, so a hand-edited Zed config survives.
package jsonc

import (
	"encoding/json"
	"strings"
)

// Loads parses JSONC text: comments are blanked and trailing commas removed
// before encoding/json sees it.
func Loads(s string) (any, error) {
	var v any
	if err := json.Unmarshal([]byte(stripTrailingCommas(BlankComments(s))), &v); err != nil {
		return nil, err
	}
	return v, nil
}

// HasComments reports whether s contains a comment outside a string.
func HasComments(s string) bool {
	return BlankComments(s) != s
}

// BlankComments replaces // line comments and /* */ block comments with spaces.
// Byte offsets, newlines and string literals are preserved, so a position in
// the result maps to the same position in the input.
func BlankComments(s string) string {
	b := []byte(s)
	n := len(b)
	inStr := false
	for i := 0; i < n; i++ {
		c := b[i]
		if inStr {
			if c == '\\' && i+1 < n {
				i++
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch {
		case c == '"':
			inStr = true
		case c == '/' && i+1 < n && b[i+1] == '/':
			j := i
			for j < n && b[j] != '\n' {
				b[j] = ' '
				j++
			}
			i = j - 1
		case c == '/' && i+1 < n && b[i+1] == '*':
			j := i + 2
			b[i], b[i+1] = ' ', ' '
			for j < n {
				if b[j] == '*' && j+1 < n && b[j+1] == '/' {
					b[j], b[j+1] = ' ', ' '
					j += 2
					break
				}
				if b[j] != '\n' {
					b[j] = ' '
				}
				j++
			}
			i = j - 1
		}
	}
	return string(b)
}

// stripTrailingCommas blanks any comma that is followed only by whitespace and
// then a closing bracket. Run it on comment-blanked text.
func stripTrailingCommas(s string) string {
	b := []byte(s)
	n := len(b)
	inStr := false
	for i := 0; i < n; i++ {
		c := b[i]
		if inStr {
			if c == '\\' && i+1 < n {
				i++
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			continue
		}
		if c == ',' {
			j := i + 1
			for j < n && isSpaceByte(b[j]) {
				j++
			}
			if j < n && (b[j] == ']' || b[j] == '}') {
				b[i] = ' '
			}
		}
	}
	return string(b)
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// AppendManagedBlock inserts block between the begin and end markers inside the
// top-level array in text. Calling it twice with the same block is a no-op, so
// installing is idempotent. Comments and formatting around the insertion point
// are preserved.
func AppendManagedBlock(text, block, begin, end string) string {
	nl := "\n"
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
	}
	region := begin + nl + block + nl + end

	if i := strings.Index(text, begin); i >= 0 {
		if j := strings.Index(text[i+len(begin):], end); j >= 0 {
			after := i + len(begin) + j + len(end)
			return text[:i] + region + text[after:]
		}
	}

	blank := BlankComments(text)
	close := lastTopLevelClose(blank, ']')
	if close < 0 {
		return "[" + nl + region + nl + "]" + nl
	}
	// The comma belongs immediately after the last real element, never after a
	// trailing comment, so find it in the comment-blanked text. If the element
	// already ends in a comma — which is valid JSONC, and common — adding
	// another produces ",," and the file no longer parses at all.
	k := close - 1
	for k >= 0 && isSpaceByte(blank[k]) {
		k--
	}
	sep := ""
	if k >= 0 && blank[k] != '[' && blank[k] != ',' {
		sep = ","
	}
	gap := text[k+1 : close]
	if !HasComments(gap) {
		gap = nl
	}
	return text[:k+1] + sep + gap + region + nl + text[close:]
}

// RemoveManagedBlock deletes the managed region and any comma that only
// separated it from the previous element.
func RemoveManagedBlock(text, begin, end string) string {
	i := strings.Index(text, begin)
	if i < 0 {
		return text
	}
	j := strings.Index(text[i+len(begin):], end)
	if j < 0 {
		return text
	}
	after := i + len(begin) + j + len(end)
	head := dropDanglingComma(text[:i])
	tail := strings.TrimLeft(text[after:], "\r\n")
	return head + tail
}

// dropDanglingComma removes the last comma in head when everything after it is
// only whitespace and comments — that comma was ours.
func dropDanglingComma(head string) string {
	idx := strings.LastIndex(head, ",")
	if idx < 0 {
		return head
	}
	if strings.TrimSpace(BlankComments(head[idx+1:])) != "" {
		return head
	}
	return head[:idx] + head[idx+1:]
}

// lastTopLevelClose returns the index of the closing byte that brings nesting
// back to zero, or -1. Run it on comment-blanked text.
func lastTopLevelClose(blank string, ch byte) int {
	depth := 0
	last := -1
	inStr := false
	for i := 0; i < len(blank); i++ {
		c := blank[i]
		if inStr {
			if c == '\\' {
				i++
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth == 0 && c == ch {
				last = i
			}
		}
	}
	return last
}
