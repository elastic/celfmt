// Copyright 2019 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package celfmt

import (
	"strings"

	"cel.dev/cel-go/common"
)

// lineScan holds what a scan of the whole source tells about its lines, which
// cannot be told from a line alone: where the comment on a line starts, if it
// has one, and whether the line starts inside a string. A // in a string, in a
// raw string, or on a line of a string that spans lines does not start a
// comment.
type lineScan struct {
	src common.Source

	// comment and inString are indexed by line number, from 1.
	comment  []int  // Byte index in the line of the //, or -1.
	inString []bool // The line starts inside a string.
}

// scanLines returns the scan of src.
func scanLines(src common.Source) *lineScan {
	text := src.Content()
	// Lines are numbered from 1, and the last line may end with a newline.
	n := strings.Count(text, "\n") + 2
	s := &lineScan{
		src:      src,
		comment:  make([]int, n+1),
		inString: make([]bool, n+1),
	}
	for i := range s.comment {
		s.comment[i] = -1
	}

	line := 1
	start := 0 // Index in text of the start of the line.
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == '\n':
			line++
			start = i + 1
			i++
		case c == '/' && strings.HasPrefix(text[i:], "//"):
			s.comment[line] = i - start
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case c == '"' || c == '\'' || c == '`':
			// The string ends at the matching quote. Only a triple-quoted
			// string spans lines, and an unterminated one ends at the end
			// of its line.
			raw := c == '`' || rawPrefix(text, i)
			triple := c != '`' && tripled(text, i)
			i++
			if triple {
				i += 2
			}
		str:
			for i < len(text) {
				d := text[i]
				switch {
				case d == '\n':
					if !triple {
						break str
					}
					line++
					start = i + 1
					s.inString[line] = true
					i++
				case d == '\\' && !raw:
					i++
					if i < len(text) && text[i] != '\n' {
						i++
					}
				case d == c && !triple:
					i++
					break str
				case d == c && tripled(text, i):
					i += 3
					break str
				default:
					i++
				}
			}
		default:
			i++
		}
	}
	return s
}

// tripled returns whether text has three of the same character at i.
func tripled(text string, i int) bool {
	return i+2 < len(text) && text[i+1] == text[i] && text[i+2] == text[i]
}

// rawPrefix returns whether the quote at text[i] follows the prefix of a raw
// string, r or R, or of a raw bytes literal, br or bR. It also accepts the
// prefixes in the other order, which the parser rejects.
func rawPrefix(text string, i int) bool {
	j := i
	for j > 0 && i-j < 2 && strings.IndexByte("rRbB", text[j-1]) >= 0 {
		j--
	}
	if j > 0 && isIdentByte(text[j-1]) {
		return false
	}
	return strings.ContainsAny(text[j:i], "rR")
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// split returns the code and the comment of a line, each with white space
// trimmed. The comment starts with the //, or is empty. The code of a line
// that starts inside a string is not empty, even if the string is, since the
// line is not blank. The index of the // in the line is also returned, or -1.
func (s *lineScan) split(line int, text string) (code, comment string, idx int) {
	idx = s.commentIndex(line)
	if idx < 0 || idx > len(text) {
		return strings.TrimSpace(text), "", -1
	}
	return strings.TrimSpace(text[:idx]), strings.TrimSpace(text[idx:]), idx
}

// startsInString returns whether the line starts inside a string.
func (s *lineScan) startsInString(line int) bool {
	return line > 0 && line < len(s.inString) && s.inString[line]
}

// commentIndex returns the index in the line of the // that starts a
// comment, or -1 if the line has none.
func (s *lineScan) commentIndex(line int) int {
	if line > 0 && line < len(s.comment) {
		return s.comment[line]
	}
	return -1
}

// byteIndex returns the index in text of the character in the given column,
// which counts characters, not bytes.
func byteIndex(text string, col int) int {
	for i := range text {
		if col == 0 {
			return i
		}
		col--
	}
	return len(text)
}
