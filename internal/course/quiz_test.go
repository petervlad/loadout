package course

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseQuiz(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want Quiz
	}{
		{"one right", "Which?\n\n- [ ] a\n  Not a.\n- [x] b\n  Yes, b.\n", Quiz{
			Question: "Which?",
			Answers:  []Answer{{Text: "a", Why: "Not a."}, {Text: "b", Right: true, Why: "Yes, b."}},
		}},
		{"several right", "Pick two\n- [x] a\n- [x] b\n- [ ] c", Quiz{
			Question: "Pick two",
			Answers:  []Answer{{Text: "a", Right: true}, {Text: "b", Right: true}, {Text: "c"}},
		}},
		{"star and capital X", "Q\n* [X] a\n* [ ] b", Quiz{
			Question: "Q",
			Answers:  []Answer{{Text: "a", Right: true}, {Text: "b"}},
		}},
		{"explanation over lines and a blank one", "Q\n- [x] a\n    First line.\n    Second line.\n\n      - indented more\n- [ ] b\n", Quiz{
			Question: "Q",
			Answers:  []Answer{{Text: "a", Right: true, Why: "First line.\nSecond line.\n\n  - indented more"}, {Text: "b"}},
		}},
		{"tab indent", "Q\n- [x] a\n\tWhy.\n- [ ] b", Quiz{
			Question: "Q",
			Answers:  []Answer{{Text: "a", Right: true, Why: "Why."}, {Text: "b"}},
		}},
		{"markdown in the question kept", "Run `loadout rebase`?\n\n- a list item\n\n```sh\nx\n```\n- [x] **yes**\n- [ ] no", Quiz{
			Question: "Run `loadout rebase`?\n\n- a list item\n\n```sh\nx\n```",
			Answers:  []Answer{{Text: "**yes**", Right: true}, {Text: "no"}},
		}},
		{"CRLF read as LF", "Q\r\n- [x] a\r\n  Why.\r\n- [ ] b\r\n", Quiz{
			Question: "Q",
			Answers:  []Answer{{Text: "a", Right: true, Why: "Why."}, {Text: "b"}},
		}},
		{"eight answers", "Q\n- [x] 1\n- [ ] 2\n- [ ] 3\n- [ ] 4\n- [ ] 5\n- [ ] 6\n- [ ] 7\n- [ ] 8", Quiz{
			Question: "Q",
			Answers:  []Answer{{Text: "1", Right: true}, {Text: "2"}, {Text: "3"}, {Text: "4"}, {Text: "5"}, {Text: "6"}, {Text: "7"}, {Text: "8"}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseQuiz(tt.src)
			if err != nil {
				t.Fatalf("ParseQuiz() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseQuiz() =\n%#v\nwant\n%#v", got, tt.want)
			}
		})
	}
}

func TestParseQuizRefused(t *testing.T) {
	nine := "Q\n- [x] 1\n" + strings.Repeat("- [ ] n\n", 8)
	tests := []struct {
		name, src, want string
	}{
		{"no question", "- [x] a\n- [ ] b", "line 1: write the question"},
		{"blank question", "\n  \n- [x] a\n- [ ] b", "line 1: write the question"},
		{"no answers", "Q only", "line 1: a quiz needs at least 2 answers"},
		{"one answer", "Q\n\n- [x] a\n  Why.", "line 3: a quiz needs at least 2 answers"},
		{"nine answers", nine, "line 10: a quiz has at most 8 answers"},
		{"no right answer", "Q\n- [ ] a\n- [ ] b", "line 2: no answer is marked right"},
		{"empty answer", "Q\n- [x]\n- [ ] b", "line 2: write the answer"},
		{"unindented line after answers", "Q\n- [x] a\n- [ ] b\nloose text", "line 4: indent the explanation"},
		{"one space is not an indent", "Q\n- [x] a\n x\n- [ ] b", "line 3: indent the explanation"},
		{"too large", "Q\n- [x] a\n- [ ] b\n" + strings.Repeat("  x\n", MaxQuiz/4), "the limit is 16 KiB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseQuiz(tt.src)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ParseQuiz() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}
