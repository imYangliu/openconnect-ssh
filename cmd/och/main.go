package main

import (
	"fmt"
	"os"

	"github.com/imyangliu/openconnect-ssh/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "och:", err)
		os.Exit(1)
	}
}
