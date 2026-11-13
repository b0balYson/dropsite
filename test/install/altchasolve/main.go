// Command altchasolve fetches the Altcha challenge of the admin sign-in
// form and prints the solved widget payload, so test/install/e2e.sh can sign
// in with curl:
//
//	go run ./test/install/altchasolve http://127.0.0.1:8082/login/challenge
//
// It prints nothing when the server has no challenge endpoint (a release
// from before the sign-in check), so the same script tests both.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go/v2"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "altchasolve:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: altchasolve <challenge URL>")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, os.Args[1], nil) //nolint:gosec // test tool: the URL is the server under test
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // test tool: the URL is the server under test
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("challenge: HTTP %d", resp.StatusCode)
	}
	var ch altcha.Challenge
	if err := json.NewDecoder(resp.Body).Decode(&ch); err != nil {
		return fmt.Errorf("decode challenge: %w", err)
	}
	sol, err := altcha.SolveChallenge(altcha.SolveChallengeOptions{Challenge: ch, DeriveKey: altcha.DeriveKeyPBKDF2(), Timeout: 4 * time.Minute})
	if err != nil {
		return fmt.Errorf("solve: %w", err)
	}
	if sol == nil {
		return fmt.Errorf("solve: no solution")
	}
	b, err := json.Marshal(altcha.Payload{Challenge: ch, Solution: *sol})
	if err != nil {
		return err
	}
	fmt.Println(base64.StdEncoding.EncodeToString(b))
	return nil
}
