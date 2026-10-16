package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

// runLogin imitates the login and status commands of the CLIs:
//
//	auth login            link, then waits for a pasted code ("good" succeeds)
//	auth status --json    {"loggedIn": FAKE_LOGGED_IN == "1"}
//	login status          exit 0 when FAKE_LOGGED_IN == "1"
//	login [--device-auth] link and code, then waits FAKE_SLEEP_MS (default 5s)
//	provider list         a provider when FAKE_LOGGED_IN == "1"
//
// It reports whether it handled args.
func runLogin(args []string) bool {
	loggedIn := os.Getenv("FAKE_LOGGED_IN") == "1"
	cmd := strings.Join(args, " ")
	switch {
	case cmd == "auth status --json":
		fmt.Printf("{\"loggedIn\": %v, \"authMethod\": \"fake\"}\n", loggedIn)
	case cmd == "auth login":
		fmt.Println("\x1b[1mOpening browser to sign in…\x1b[0m")
		fmt.Println("If the browser didn't open, visit: \x1b]8;;https://example.test/oauth?x=1\x07https://example.test/oauth?x=1\x1b]8;;\x07")
		fmt.Print("Paste code here if prompted > ")
		code, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(code) != "good" {
			fmt.Println("Login failed: invalid code")
			os.Exit(1)
		}
		fmt.Println("Login successful.")
	case cmd == "login status":
		if !loggedIn {
			fmt.Fprintln(os.Stderr, "WARNING: something unrelated")
			fmt.Println("Not logged in")
			os.Exit(1)
		}
		fmt.Println("Logged in using ChatGPT")
	case args[0] == "login":
		fmt.Println("1. Open this link: https://example.test/device")
		fmt.Println("2. Enter this one-time code: ABCD-1234")
		time.Sleep(sleepDuration())
	case cmd == "provider list":
		if loggedIn {
			fmt.Println("managed:kimi-code  3 models")
		} else {
			fmt.Println("No providers configured.")
		}
	default:
		return false
	}
	return true
}
