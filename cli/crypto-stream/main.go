package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/uwezukwechibuzor/termino/cli/crypto-stream/ui"
)

const usage = `crypto-stream - Real-time crypto price streaming terminal

Usage:
  crypto-stream prices [SYMBOLS...]    Stream live prices (e.g., crypto-stream prices BTC ETH SOL)
  crypto-stream watch SYMBOL           Watch a single asset with detailed view
  crypto-stream alerts                 Stream live price alerts
  crypto-stream history SYMBOL         Show price history for a symbol
  crypto-stream version                Show version

Environment:
  STREAM_URL    Streaming API URL (default: http://localhost:8080)

Examples:
  crypto-stream prices BTC ETH SOL
  crypto-stream watch BTC
  crypto-stream alerts
`

var version = "1.0.0"

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(1)
	}

	serverURL := os.Getenv("STREAM_URL")
	if serverURL == "" {
		serverURL = "http://localhost:8080"
	}

	cmd := os.Args[1]

	switch cmd {
	case "prices":
		symbols := os.Args[2:]
		if len(symbols) == 0 {
			symbols = []string{"BTC", "ETH", "SOL", "ADA", "DOT", "AVAX", "MATIC", "LINK"}
		}
		for i := range symbols {
			symbols[i] = strings.ToUpper(symbols[i])
		}
		ui.RunPriceStream(serverURL, symbols)

	case "watch":
		if len(os.Args) < 3 {
			fmt.Println("Usage: crypto-stream watch SYMBOL")
			os.Exit(1)
		}
		symbol := strings.ToUpper(os.Args[2])
		ui.RunWatchMode(serverURL, symbol)

	case "alerts":
		ui.RunAlertStream(serverURL)

	case "history":
		if len(os.Args) < 3 {
			fmt.Println("Usage: crypto-stream history SYMBOL")
			os.Exit(1)
		}
		symbol := strings.ToUpper(os.Args[2])
		ui.RunHistory(serverURL, symbol)

	case "version":
		fmt.Printf("crypto-stream %s\n", version)

	default:
		fmt.Printf("Unknown command: %s\n\n", cmd)
		fmt.Print(usage)
		os.Exit(1)
	}
}
