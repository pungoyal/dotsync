// Command genman writes the dotsync(1) man page to stdout. Releases run it (see
// .goreleaser.yaml) to ship the page in the archives, the .deb and the Homebrew formula.
//
//	go run ./internal/cmd/genman -version 0.6.0 -date 2026-09-21 > dotsync.1
package main

import (
	"flag"
	"fmt"

	"github.com/pungoyal/dotsync/internal/dotsync"
)

func main() {
	version := flag.String("version", "dev", "version shown in the footer")
	date := flag.String("date", "", "date shown in the footer (YYYY-MM-DD)")
	flag.Parse()
	fmt.Print(dotsync.ManPage(*version, *date))
}
