package main

import (
	"fmt"
	"os"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	path, err := cfg.LogFilePath()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Printf("%s %s; log path: %s\n", cfg.App.Name, cfg.App.Version, path)
}
