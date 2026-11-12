package main

import (
	"context"
	"fmt"

	"github.com/b0balYson/dropsite/internal/brand"
)

func cmdVersion(_ context.Context, a *app, args []string) error {
	fs := a.newFlagSet("version", "", "Print the CLI version.")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := wantArgs(fs, pos, 0, 0); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%s %s\n", brand.Name, brand.Version)
	return nil
}
