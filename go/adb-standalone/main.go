package main

//go:generate go run ./tools/genfixtures --out ../../docs/adb-standalone/fixtures

import "github.com/Genentech/exohub/go/adb-standalone/cmd"

func main() {
	cmd.Execute()
}
