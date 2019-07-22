package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
)

var (
	file        string
	mode        string
	account     string
	transaction string
	showZero    bool
	onlyReal    bool
	noBalance   bool
	noTriggers  bool
	noSort      bool
)

func init() {
	flag.StringVar(&file, "file", "log.txt", "Ledger file to process.")
	flag.StringVar(&mode, "mode", "balance", "Mode to run in (balance, print, or register).")
	flag.StringVar(&account, "account", "", "Show only accounts matching this regex filter.")
	flag.StringVar(&transaction, "transaction", "", "Show only transactions matching this regex filter for their description or ID.")
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

	var accountRegexp *regexp.Regexp
	if account != "" {
		accountRegexp = regexp.MustCompile(account)
	}

	var transactionRegexp *regexp.Regexp
	if transaction != "" {
		transactionRegexp = regexp.MustCompile(transaction)
	}

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

				if len(tx.Postings) > 100 {
					fmt.Printf("posting cycle detected\n\n%s\n", tx.String())

					os.Exit(1)
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
			if transactionRegexp != nil && !transactionRegexp.MatchString(tx.Description) && !transactionRegexp.MatchString(tx.ID) {
				continue
			}

			fmt.Printf("%s\n", tx.String())
		}
	case "register":
		accounts := NewAccounts()

		for _, tx := range transactions {
			first := true

			for _, p := range tx.Postings {
				if p.Amount.IsZero() && !showZero {
					continue
				}
				if p.Type != RealPosting && onlyReal {
					continue
				}

				a := accounts.Get(p.Account)
				a.Add(*p.Amount)

				if accountRegexp != nil && !accountRegexp.MatchString(a.Name) {
					continue
				}
				if transactionRegexp != nil && !transactionRegexp.MatchString(tx.Description) && !transactionRegexp.MatchString(tx.ID) {
					continue
				}

				prefix := ""
				if first {
					prefix = fmt.Sprintf("%s %-30s", tx.Date.Format("06-Jan-02"), tx.Description)
					first = false
				}

				fmt.Printf("%-42s %-40s %14s %14s\n", prefix, a.Name, "$"+p.Amount.StringFixedBank(2), "$"+a.Balance.StringFixedBank(2))
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
			a := accounts.Get(name)

			if a.Balance.IsZero() && !showZero {
				continue
			}

			if accountRegexp == nil || accountRegexp.MatchString(a.Name) {
				fmt.Printf("%16s %-40s\n", "$"+a.Balance.StringFixedBank(2), a.Name)
			}
		}

		fmt.Printf("---------------- Total\n")
		fmt.Printf("%16s\n", "$"+accounts.Balance().StringFixedBank(2))
	}
}
