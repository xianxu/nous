// Gmail CLI — search and read Gmail threads via Charon proxy.
//
// Usage:
//
//	charon run -- gmail search "query" [--account user@gmail.com] [--max-results 10]
//	charon run -- gmail thread <thread_id> --account user@gmail.com
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/xianxu/nous/lib/gmail"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "search":
		cmdSearch(os.Args[2:])
	case "thread":
		cmdThread(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
}

func cmdSearch(args []string) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	account := fs.String("account", "", "search specific account only")
	maxResults := fs.Int("max-results", 10, "max threads to return")
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: gmail search <query> [--account email] [--max-results N]")
		os.Exit(1)
	}
	query := fs.Arg(0)

	if *account == "" {
		fmt.Fprintln(os.Stderr, "error: --account is required (multi-account search: use charon accounts to list, then search each)")
		os.Exit(1)
	}

	threads, err := gmail.SearchThreads(*account, query, *maxResults)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(threads)
}

func cmdThread(args []string) {
	fs := flag.NewFlagSet("thread", flag.ExitOnError)
	account := fs.String("account", "", "account the thread belongs to (required)")
	fs.Parse(args)

	if fs.NArg() < 1 || *account == "" {
		fmt.Fprintln(os.Stderr, "usage: gmail thread <thread_id> --account email")
		os.Exit(1)
	}
	threadID := fs.Arg(0)

	thread, err := gmail.GetThread(*account, threadID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(thread)
}

func usage() {
	fmt.Fprintln(os.Stderr, `gmail — search and read Gmail threads via Charon proxy

Usage:
  gmail search <query> --account user@gmail.com [--max-results 10]
  gmail thread <thread_id> --account user@gmail.com

Requires: charon run -- gmail ...`)
}
