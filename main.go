package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/pkg/errors"
)

var (
	file       string
	mode       string
	showZero   bool
	onlyReal   bool
	noBalance  bool
	noTriggers bool
	noSort     bool
)

func init() {
	flag.StringVar(&file, "file", "log.txt", "Ledger file to process.")
	flag.StringVar(&mode, "mode", "balance", "Mode to run in (balance, print, or register).")
	flag.BoolVar(&showZero, "show_zero", false, "Show entries where the balance or amount is zero.")
	flag.BoolVar(&onlyReal, "only_real", false, "Only use real postings, not virtual.")
	flag.BoolVar(&noBalance, "no_balance", false, "Don't perform or check balancing (only really useful with print).")
	flag.BoolVar(&noTriggers, "no_triggers", false, "Don't run any triggers (only really useful with print).")
	flag.BoolVar(&noSort, "no_sort", false, "Don't re-order transactions by date.")
}

func main() {
	flag.Parse()

	fd, err := os.Open(file)
	if err != nil {
		panic(err)
	}
	defer fd.Close()

	triggers, transactions, err := parseFile(bufio.NewReader(fd))
	if err != nil {
		panic(err)
	}

	if !noSort {
		sort.Sort(transactions)
	}

	if noTriggers == false {
		for _, tx := range transactions {
			for i := 0; i < len(tx.Postings); i++ {
				p := tx.Postings[i]

				for _, tr := range triggers {
					if p.GeneratedBy == tr.ID {
						continue
					}

					b, m := tr.Match(tx, p)
					if !b {
						continue
					}

					for _, a := range tr.Actions {
						pp := a.Execute(p, m)
						pp.GeneratedBy = tr.ID
						pp.From = i + 1
						tx.Postings = append(tx.Postings, pp)
					}
				}

				if len(tx.Postings) > 1000 {
					panic(errors.Errorf("posting cycle detected"))
				}
			}
		}
	}

	if noBalance == false {
		failed := false

		for _, tx := range transactions {
			if err := tx.AutoBalance(); err != nil {
				fmt.Printf("%s\n\n%s\n", err.Error(), tx.String())

				failed = true
			}

			if err := tx.Balance(); err != nil {
				fmt.Printf("%s\n\n%s\n", err.Error(), tx.String())

				failed = true
			}
		}

		if failed {
			os.Exit(1)
		}
	}

	switch mode {
	case "print":
		for _, tr := range triggers {
			fmt.Printf("%s\n", tr.String())
		}
		for _, tx := range transactions {
			fmt.Printf("%s\n", tx.String())
		}
	case "register":
		accounts := NewAccounts()

		for _, tx := range transactions {
			fmt.Printf("%s %s\n", tx.Date.Format("2006-01-02"), tx.Description)

			for _, p := range tx.Postings {
				if p.Amount.IsZero() && !showZero {
					continue
				}
				if p.Type != RealPosting && onlyReal {
					continue
				}

				a := accounts.Get(p.Account)
				a.Add(*p.Amount)
				fmt.Printf("  %-40s $%-8s $%-8s\n", a.Name, p.Amount.String(), a.Balance.String())
			}
		}
	case "balance":
		accounts := NewAccounts()

		for _, tx := range transactions {
			for _, p := range tx.Postings {
				if p.Amount.IsZero() && !showZero {
					continue
				}
				if p.Type != RealPosting && onlyReal {
					continue
				}

				accounts.Get(p.Account).Add(*p.Amount)
			}
		}

		names := accounts.Names()
		sort.Strings(names)

		for _, name := range names {
			if accounts.Get(name).Balance.IsZero() && !showZero {
				continue
			}

			fmt.Printf("%16s %-40s\n", accounts.Get(name).Balance.StringFixedBank(2), name)
		}

		fmt.Printf("---------------- Total\n")
		fmt.Printf("%16s\n", accounts.Balance())
	}
}
