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

// Package celfmt provides formatting for CEL programs.
package celfmt

import (
	"testing"

	"cel.dev/cel-go/common"
)

func TestScanLines(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		comment  []int  // For each line, from 1.
		inString []bool // For each line, from 1.
	}{
		{name: "none", in: "a + b\n", comment: []int{-1, -1}, inString: []bool{false, false}},
		{name: "comment", in: "a // c\nb\n", comment: []int{2, -1, -1}, inString: []bool{false, false, false}},
		{name: "in_string", in: `"a//b" + c // d`, comment: []int{11}, inString: []bool{false}},
		{name: "single_quote", in: `'a//b' + c // d`, comment: []int{11}, inString: []bool{false}},
		{name: "escaped_quote", in: `"a\"//b" // c`, comment: []int{9}, inString: []bool{false}},
		{name: "raw_no_escape", in: `r"\" // c`, comment: []int{5}, inString: []bool{false}},
		{name: "raw_upper", in: `R"\" // c`, comment: []int{5}, inString: []bool{false}},
		{name: "bytes_raw", in: `br"\" // c`, comment: []int{6}, inString: []bool{false}},
		{name: "bytes_escape", in: `b"\"//" // c`, comment: []int{8}, inString: []bool{false}},
		{name: "bytes_raw_upper", in: `Br"\" // c`, comment: []int{6}, inString: []bool{false}},
		{name: "quad_quote", in: `''''a''' // c`, comment: []int{9}, inString: []bool{false}},
		{name: "ident_ends_in_r", in: `bar"\" // c`, comment: []int{-1}, inString: []bool{false}},
		{name: "backtick", in: "a.`b//c` // d", comment: []int{9}, inString: []bool{false}},
		{name: "triple", in: "\"\"\"a\n// b\n\"\"\" // c\n", comment: []int{-1, -1, 4, -1}, inString: []bool{false, true, true, false}},
		{name: "unterminated", in: "\"a // b\nc // d\n", comment: []int{-1, 2, -1}, inString: []bool{false, false, false}},
		{name: "crlf", in: "a // b\r\n\"c\" // d\r\n", comment: []int{2, 4, -1}, inString: []bool{false, false, false}},
		{name: "non_ascii", in: "\"é\" // c", comment: []int{5}, inString: []bool{false}},
	}
	for _, test := range tests {
		got := scanLines(common.NewTextSource(test.in))
		for i, want := range test.comment {
			if got.commentIndex(i+1) != want {
				t.Errorf("%s: scanLines(%q).commentIndex(%d) = %d; want %d", test.name, test.in, i+1, got.commentIndex(i+1), want)
			}
		}
		for i, want := range test.inString {
			if got.startsInString(i+1) != want {
				t.Errorf("%s: scanLines(%q).startsInString(%d) = %t; want %t", test.name, test.in, i+1, got.startsInString(i+1), want)
			}
		}
	}
}

func TestByteIndex(t *testing.T) {
	tests := []struct {
		text string
		col  int
		want int
	}{
		{text: "abc", col: 0, want: 0},
		{text: "abc", col: 2, want: 2},
		{text: "abc", col: 3, want: 3},
		{text: "abc", col: 9, want: 3},
		{text: "éa", col: 1, want: 2},
		{text: "éa", col: 2, want: 3},
	}
	for _, test := range tests {
		got := byteIndex(test.text, test.col)
		if got != test.want {
			t.Errorf("byteIndex(%q, %d) = %d; want %d", test.text, test.col, got, test.want)
		}
	}
}
