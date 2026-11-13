package main

import (
	"os"

	"github.com/AntonioIliev19/conmesh/internal/cli"
)

var version = "dev"

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr, version))
}
