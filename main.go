package main

import (
	"context"
	"os"

	"github.com/lulaide/minirunc/internal/command"
)

func main() {
	os.Exit(command.Execute(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
