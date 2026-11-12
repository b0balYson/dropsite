// Command control is the pushpage server: API, UI, accounts and deploys.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "control:", err)
		os.Exit(1)
	}
}

func run(_ []string) error {
	return nil
}
