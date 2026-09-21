// Command dotsync synchronizes dotfiles across macOS and Linux machines via a shared git repository.
package main

import (
	"os"

	"github.com/pungoyal/dotsync/internal/dotsync"
)

func main() {
	os.Exit(dotsync.Run(os.Args[1:], os.Stdout, os.Stderr))
}
