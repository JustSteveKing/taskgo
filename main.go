package main

import (
	"github.com/JustSteveKing/taskgo/cmd"

	// Before anything imports Bubble Tea: see the package doc.
	_ "github.com/JustSteveKing/taskgo/internal/termquiet"
)

// version is overridden at build time:
//
//	go build -ldflags "-X main.version=$(git describe --tags --always)"
var version = "dev"

func main() {
	cmd.Execute(version)
}
