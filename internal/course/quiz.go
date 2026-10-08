// Package course reads a knowledge base as a course: its lessons, in the
// order the wiki's Course box lists them, and the quiz blocks a lesson (or
// any Markdown document) may hold. It only parses; the web package draws.
package course

import (
	"errors"
	"fmt"
	"strings"
)

const (
	// MaxAnswers is the most answers one quiz may offer.
	MaxAnswers = 8
	// MaxQuiz is the most bytes of one quiz fence.
	MaxQuiz = 16 << 10
)

// Quiz is one question of a quiz fence. Built only by ParseQuiz, never
// changed after.
type Quiz struct {
	// Question is Markdown.
	Question string
	// Answers are 2 to MaxAnswers, at least one of them right.
	Answers []Answer
}

// Answer is one choice: its text (one line of Markdown), whether it is
// right, and why (Markdown, may be empty).
type Answer struct {
	Text  string
	Right bool
	Why   string
}

// ParseQuiz reads the text of a quiz fence:
//
//	The question, any Markdown, up to the first answer.
//
//	- [ ] a wrong answer
//	  Why it is wrong, indented under it.
//	- [x] a right answer
//	  Why it is right.
//
// Its errors name the line, for the note a page shows above a quiz it
// could not read.
func ParseQuiz(src string) (Quiz, error) {
	if len(src) > MaxQuiz {
		return Quiz{}, fmt.Errorf("the quiz has %d bytes and the limit is %d KiB; split it into several", len(src), MaxQuiz>>10)
	}
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var (
		q        Quiz
		question []string
		why      [][]string
		first    int // line of the first answer, from 1
	)
	for i, line := range lines {
		n := i + 1
		if text, right, ok := answerLine(line); ok {
			if text == "" {
				return Quiz{}, fmt.Errorf("line %d: write the answer after the box", n)
			}
			if len(q.Answers) == MaxAnswers {
				return Quiz{}, fmt.Errorf("line %d: a quiz has at most %d answers", n, MaxAnswers)
			}
			if first == 0 {
				first = n
			}
			q.Answers = append(q.Answers, Answer{Text: text, Right: right})
			why = append(why, nil)
			continue
		}
		switch {
		case len(q.Answers) == 0:
			question = append(question, line)
		case strings.TrimSpace(line) == "" || strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t"):
			why[len(why)-1] = append(why[len(why)-1], line)
		default:
			return Quiz{}, fmt.Errorf("line %d: indent the explanation under its answer, or start a new answer with - [ ] or - [x]", n)
		}
	}
	q.Question = strings.TrimSpace(strings.Join(question, "\n"))
	if err := q.check(first); err != nil {
		return Quiz{}, err
	}
	for i, w := range why {
		q.Answers[i].Why = dedent(w)
	}
	return q, nil
}

// check holds the rules on a whole quiz; first is the line of its first
// answer, 0 for none.
func (q Quiz) check(first int) error {
	switch {
	case q.Question == "":
		return errors.New("line 1: write the question before the first answer")
	case len(q.Answers) < 2:
		return fmt.Errorf("line %d: a quiz needs at least 2 answers, each on a line that starts with - [ ] or - [x]", max(first, 1))
	}
	for _, a := range q.Answers {
		if a.Right {
			return nil
		}
	}
	return fmt.Errorf("line %d: no answer is marked right; mark it with - [x]", first)
}

// answerLine reads a line that starts an answer: "- [ ] text" (wrong) or
// "- [x] text" (right), at the first column, with "*" for "-" and "X"
// for "x" accepted.
func answerLine(line string) (text string, right, ok bool) {
	if len(line) < 5 || (line[0] != '-' && line[0] != '*') || line[1] != ' ' || line[2] != '[' || line[4] != ']' {
		return "", false, false
	}
	switch line[3] {
	case ' ':
	case 'x', 'X':
		right = true
	default:
		return "", false, false
	}
	rest := line[5:]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return "", false, false
	}
	return strings.TrimSpace(rest), right, true
}

// dedent joins lines with their common indentation removed, and blank
// lines at either end dropped.
func dedent(lines []string) string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	cut := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if n := len(l) - len(strings.TrimLeft(l, " \t")); cut < 0 || n < cut {
			cut = n
		}
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		out[i] = l[cut:]
	}
	return strings.Join(out, "\n")
}
