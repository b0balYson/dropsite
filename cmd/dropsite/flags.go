package main

import (
	"errors"
	"flag"
	"fmt"

	"github.com/b0balYson/dropsite/internal/brand"
)

// newFlagSet builds a flag set that reports errors instead of exiting.
func (a *app) newFlagSet(name, args, desc string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintf(out, "Usage: %s %s %s\n\n%s\n", brand.Name, name, args, desc)
		hasFlags := false
		fs.VisitAll(func(*flag.Flag) { hasFlags = true })
		if hasFlags {
			fmt.Fprintln(out, "\nFlags:")
			fs.PrintDefaults()
		}
	}
	return fs
}

// parseArgs parses flags placed before, between or after positional
// arguments (the flag package alone stops at the first positional one).
// After a literal "--" everything is positional.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		before := len(args)
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, &usageError{msg: err.Error(), shown: true}
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		if consumedTerminator(args[:before-len(rest)]) {
			return append(pos, rest...), nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

func consumedTerminator(parsed []string) bool {
	return len(parsed) > 0 && parsed[len(parsed)-1] == "--"
}

// wantArgs checks the positional argument count.
func wantArgs(fs *flag.FlagSet, pos []string, lo, hi int) error {
	if len(pos) >= lo && len(pos) <= hi {
		return nil
	}
	fs.Usage()
	return &usageError{msg: fmt.Sprintf("%s: wrong number of arguments", fs.Name()), shown: true}
}
