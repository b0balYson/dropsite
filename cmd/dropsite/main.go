// Command dropsite publishes a directory of static files to a dropsite server.
package main

import (
	"context"
	"os"
	"os/signal"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := newApp().run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}
