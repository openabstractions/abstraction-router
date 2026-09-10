package main

import (
	"fmt"
	"os"

	router "github.com/openabstractions/abstraction-router/go"
)

func main() {
	if err := router.Serve(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "routerd:", err)
		os.Exit(3)
	}
}
