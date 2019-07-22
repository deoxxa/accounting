package main

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/robertkrimen/otto"
	"github.com/shopspring/decimal"
)

type Accounts struct {
	l sync.Mutex
	m map[string]*Account
}

func NewAccounts() *Accounts { return &Accounts{m: make(map[string]*Account)} }

func (a *Accounts) Get(name string) *Account {
	a.l.Lock()
	defer a.l.Unlock()

	if _, ok := a.m[name]; !ok {
		a.m[name] = &Account{
			Name:    name,
			Balance: decimal.NewFromFloat(0),
		}
	}

	return a.m[name]
}

func (a *Accounts) Balance() decimal.Decimal {
	var d decimal.Decimal

	for _, e := range a.m {
		d = d.Add(e.Balance)
	}

	return d
}

func (a *Accounts) Names() []string {
	var l []string
	for k := range a.m {
		l = append(l, k)
	}

	return l
}

func (a *Accounts) Filter(prefix string) *Accounts {
	r := NewAccounts()

	for k, v := range a.m {
		if strings.HasPrefix(k, prefix) && k != prefix {
			r.m[k] = v
		}
	}

	return r
}

type Account struct {
	Name    string
	Balance decimal.Decimal
}

func (a *Account) Add(d decimal.Decimal) {
	a.Balance = a.Balance.Add(d)
}

type Transactions []*Transaction

func (l Transactions) Len() int           { return len(l) }
func (l Transactions) Less(a, b int) bool { return l[a].Date.Before(l[b].Date) }
func (l Transactions) Swap(a, b int)      { l[a], l[b] = l[b], l[a] }

type Transaction struct {
	Date        time.Time
	Description string
	Postings    []*Posting
}

func (t Transaction) String() string {
	s := fmt.Sprintf("%s %s\n", t.Date.Format("2006-01-02"), t.Description)
	for _, e := range t.Postings {
		s += "\t" + e.String() + "\n"
	}

	return s
}

func (t *Transaction) AutoBalance() error {
	var toFill *Posting

	var total decimal.Decimal

	for _, e := range t.Postings {
		if e.Amount == nil {
			if toFill != nil {
				return errors.Errorf("AutoBalance: a transaction may only have one elided value")
			}

			toFill = e
		} else {
			total = total.Add(*e.Amount)
		}
	}

	if toFill != nil {
		total = total.Neg()
		toFill.Amount = &total
	}

	return nil
}

func (t *Transaction) Balance() error {
	var total decimal.Decimal

	for _, e := range t.Postings {
		if e.Type == VirtualPosting {
			continue
		}

		total = total.Add(*e.Amount)
	}

	if !total.IsZero() {
		return errors.Errorf("Balance: transactions must balance to zero; instead got %s", total.String())
	}

	return nil
}

type PostingType int

const (
	RealPosting PostingType = iota
	VirtualPosting
	BalancedVirtualPosting
)

func (p PostingType) Format(name string) string {
	switch p {
	case VirtualPosting:
		return "(" + name + ")"
	case BalancedVirtualPosting:
		return "[" + name + "]"
	default:
		return name
	}
}

type Posting struct {
	Type        PostingType
	Account     string
	Amount      *decimal.Decimal
	Comment     string
	GeneratedBy int
	From        int
}

func (p Posting) String() string {
	s := p.Type.Format(p.Account)
	if p.Amount != nil {
		s += "\t" + p.Amount.String()
	}
	if p.Comment != "" {
		s += "; " + p.Comment
	}
	if p.GeneratedBy != 0 {
		s += fmt.Sprintf("; GeneratedBy=%d From=%d", p.GeneratedBy, p.From)
	}
	return s
}

type Trigger struct {
	ID       int
	Matchers []Matcher
	Actions  []*Action
}

func (t Trigger) String() string {
	var s string
	for _, e := range t.Matchers {
		s += "= " + e.String() + "\n"
	}
	for _, e := range t.Actions {
		s += "\t" + e.String() + "\n"
	}

	return s
}

func (t Trigger) Match(tx *Transaction, p *Posting) (bool, map[string]string) {
	m := make(map[string]string)

	for _, e := range t.Matchers {
		b, mm := e.Match(tx, p)
		if !b {
			return false, nil
		}

		for k, v := range mm {
			m[k] = v
		}
	}

	return true, m
}

type Action struct {
	Type    PostingType
	Account string
	Amount  decimal.Decimal
	Comment string
}

func (a Action) String() string {
	s := a.Type.Format(a.Account) + "\t" + a.Amount.String()
	if a.Comment != "" {
		s += "; " + a.Comment
	}

	return s
}

func (a *Action) Execute(p *Posting, m map[string]string) *Posting {
	n := p.Amount.Mul(a.Amount)

	return &Posting{
		Type:   a.Type,
		Amount: &n,
		Account: regexp.MustCompile(`\$\{[A-Za-z0-9_]+\}`).ReplaceAllStringFunc(a.Account, func(s string) string {
			s = strings.Trim(s, "${}")

			if s == "account" {
				return p.Account
			}

			return m[s]
		}),
	}
}

type Matcher interface {
	String() string
	Match(t *Transaction, p *Posting) (bool, map[string]string)
}

type RegexpMatcher struct{ r *regexp.Regexp }

func (m *RegexpMatcher) String() string {
	return "/" + m.r.String() + "/"
}

func (m *RegexpMatcher) Match(tx *Transaction, p *Posting) (bool, map[string]string) {
	e := make(map[string]string)

	for i, v := range m.r.FindStringSubmatch(p.Account) {
		e[fmt.Sprintf("%d", i)] = v
	}

	return m.r.MatchString(p.Account), e
}

type JSMatcher struct {
	js string
	fn otto.Value
}

func (m *JSMatcher) String() string {
	return "JS " + m.js
}

func (m *JSMatcher) Match(tx *Transaction, p *Posting) (bool, map[string]string) {
	res, err := m.fn.Call(otto.UndefinedValue(), tx, p)
	if err != nil {
		panic(err)
	}

	if res.IsNull() || res.IsUndefined() {
		return false, nil
	}

	if res.IsBoolean() {
		b, _ := res.ToBoolean()
		return b, map[string]string{}
	}

	if res.IsObject() {
		e := make(map[string]string)

		obj := res.Object()

		for _, key := range obj.Keys() {
			val, err := obj.Get(key)
			if err != nil {
				panic(err)
			}

			e[key] = val.String()
		}

		return true, e
	}

	panic(errors.Errorf("can't interpret return type %s", res.Class()))
}
