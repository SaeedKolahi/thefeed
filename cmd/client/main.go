package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/sartoopjj/thefeed/internal/version"
	"github.com/sartoopjj/thefeed/internal/web"
)

func main() {
	dataDir := flag.String("data-dir", "./thefeeddata", "Data directory for config, cache, and sessions")
	port := flag.Int("port", 8080, "Web UI port")
	showVersion := flag.Bool("version", false, "Show version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("thefeed-client %s (commit: %s, built: %s)\n", version.Version, version.Commit, version.Date)
		os.Exit(0)
	}

	srv, err := web.New(*dataDir, *port)
	if err != nil {
		log.Fatalf("Failed to start: %v", err)
	}

	if err := srv.Run(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
