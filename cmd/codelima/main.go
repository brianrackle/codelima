package main

import (
	"context"
	"os"

	"github.com/brianrackle/test_lima/internal/codelima"
)

func main() {
	os.Exit(codelima.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
