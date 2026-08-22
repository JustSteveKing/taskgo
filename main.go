package main

import "github.com/JustSteveKing/taskgo/cmd"

// version is overridden at build time:
//
//	go build -ldflags "-X main.version=$(git describe --tags --always)"
var version = "dev"

func main() {
	cmd.Execute(version)
}
