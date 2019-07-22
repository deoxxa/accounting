package main

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/robertkrimen/otto"
	"github.com/shopspring/decimal"
)

func getComment(s string) (string, string) {
	if a := strings.SplitN(s, ";", 2); len(a) == 2 {
		return strings.TrimSpace(a[0]), strings.TrimSpace(a[1])
	}

	return s, ""
}

func parseFile(rd *bufio.Reader) ([]*Trigger, Transactions, error) {
	var triggers []*Trigger
	var transactions Transactions

	for {
		b, err := rd.Peek(1)
		if err == io.EOF {
			break
		}

		switch string(b) {
		case "\n":
			_, _ = rd.Discard(1)
		case "#":
			_, _ = rd.ReadString('\n')
		case "=":
			t, err := parseTrigger(rd)
			if err != nil {
				return nil, nil, errors.Wrap(err, "parseFile")
			}
			t.ID = len(triggers) + 1
			triggers = append(triggers, t)
		default:
			t, err := parseTransaction(rd)
			if err != nil {
				return nil, nil, errors.Wrap(err, "parseFile")
			}
			transactions = append(transactions, t)
		}
	}

	return triggers, transactions, nil
}

func parseTrigger(rd *bufio.Reader) (*Trigger, error) {
	var tr Trigger

headerLoop:
	for {
		b, err := rd.Peek(1)
		if err != nil {
			if err == io.EOF {
				break
			}

			return nil, errors.Errorf("parseTrigger")
		}

		switch string(b) {
		case "#":
			_, _ = rd.ReadString('\n')
		case "=":
			l, err := rd.ReadString('\n')
			if err != nil {
				return nil, errors.Errorf("parseTrigger")
			}

			l = strings.TrimSpace(strings.TrimPrefix(l, "="))

			switch {
			case strings.HasPrefix(l, "/") && strings.HasSuffix(l, "/"):
				re, err := regexp.Compile(strings.Trim(l, "/"))
				if err != nil {
					return nil, errors.Wrap(err, "parseTrigger")
				}
				tr.Matchers = append(tr.Matchers, &RegexpMatcher{r: re})
			case strings.HasPrefix(l, "JS"):
				js := strings.TrimSpace(strings.TrimPrefix(l, "JS"))

				vm := otto.New()

				vm.Set("fy", func(t time.Time) string {
					switch t.Month() {
					case time.January, time.February, time.March, time.April, time.May, time.June:
						return fmt.Sprintf("FY%d%d", (t.Year()-1)%100, t.Year()%100)
					default:
						return fmt.Sprintf("FY%d%d", t.Year()%100, (t.Year()+1)%100)
					}
				})

				fn, err := vm.Eval("(function(tx, p) { return (" + js + ") })")
				if err != nil {
					return nil, errors.Wrap(err, "parseTrigger")
				}

				tr.Matchers = append(tr.Matchers, &JSMatcher{js: js, fn: fn})
			default:
				return nil, errors.Errorf("parseTrigger: couldn't parse header %q", l)
			}
		default:
			break headerLoop
		}
	}

bodyLoop:
	for {
		l, err := rd.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}

			return nil, errors.Errorf("parseTrigger")
		}

		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			break bodyLoop
		}

		a, err := parseAction(l)
		if err != nil {
			return nil, errors.Wrap(err, "parseTrigger")
		}

		tr.Actions = append(tr.Actions, a)
	}

	return &tr, nil
}

func parseAction(l string) (*Action, error) {
	l, c := getComment(l)

	a := strings.Split(l, "\t")

	typ, account := parsePostingTypeAndAccount(a[0])

	switch len(a) {
	case 1:
		return &Action{Type: typ, Account: account, Comment: c}, nil
	case 2:
		n, err := decimal.NewFromString(strings.Replace(a[1], "$", "", 1))
		if err != nil {
			return nil, errors.Wrap(err, "parseAction")
		}
		return &Action{Type: typ, Account: account, Amount: n, Comment: c}, nil
	default:
		return nil, errors.Errorf("parseAction: wrong number of segments")
	}
}

func parseTransaction(rd *bufio.Reader) (*Transaction, error) {
	hdr, err := rd.ReadString('\n')
	if err != nil {
		return nil, errors.Wrap(err, "parseTransaction")
	}
	hdr = strings.TrimSpace(hdr)

	a := strings.SplitN(hdr, " ", 2)
	if len(a) < 2 {
		a = append(a, "")
	}

	m := regexp.MustCompile("^(?:<(.+?)> *)?(.+?)$").FindStringSubmatch(a[1])

	tr := Transaction{ID: m[1], Description: m[2]}

	bits := strings.SplitN(a[0], "=", 2)

	date, err := time.Parse("2006-01-02", bits[0])
	if err != nil {
		return nil, errors.Wrap(err, "parseTransaction")
	}
	tr.Date = date

	if len(bits) > 1 {
		t, err := time.Parse("2006-01-02", bits[1])
		if err != nil {
			return nil, errors.Wrap(err, "parseTransaction")
		}
		tr.SecondaryDate = &t
	}

	for {
		l, err := rd.ReadString('\n')
		if err == io.EOF {
			break
		}

		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			break
		}

		p, err := parsePosting(l)
		if err != nil {
			return nil, errors.Wrap(err, "parseTransaction")
		}

		tr.Postings = append(tr.Postings, p)
	}

	return &tr, nil
}

func parsePosting(l string) (*Posting, error) {
	l, c := getComment(l)

	a := strings.Split(l, "\t")

	typ, account := parsePostingTypeAndAccount(a[0])

	switch len(a) {
	case 1:
		return &Posting{Type: typ, Account: account, Comment: c}, nil
	case 2:
		n, err := decimal.NewFromString(strings.Replace(a[1], "$", "", 1))
		if err != nil {
			return nil, errors.Wrap(err, "parseAction")
		}
		return &Posting{Type: typ, Account: account, Amount: &n, Comment: c}, nil
	default:
		return nil, errors.Errorf("parseAction: wrong number of segments")
	}
}

func parsePostingTypeAndAccount(s string) (PostingType, string) {
	switch {
	case strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")"):
		return VirtualPosting, strings.Trim(s, "()")
	case strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]"):
		return BalancedVirtualPosting, strings.Trim(s, "[]")
	default:
		return RealPosting, s
	}
}
