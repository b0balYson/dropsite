// Command pushpage publishes a directory of static files to a pushpage server.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "pushpage:", err)
		os.Exit(1)
	}
}

func run(_ []string) error {
	return nil
}
