package main

import (
	"fmt"
	"os"

	"github.com/ezke5/pi/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "pi:", err)
		os.Exit(1)
	}
}
