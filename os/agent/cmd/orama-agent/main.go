// orama-agent is the sole root process on OramaOS.
// It handles enrollment, LUKS key management, service supervision,
// over-the-air updates, and command reception.
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/DeBrosOfficial/orama-os/agent/internal/boot"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.Println("orama-agent starting")

	agent, err := boot.NewAgent()
	if err != nil {
		log.Fatalf("failed to initialize agent: %v", err)
	}

	if err := agent.Run(); err != nil {
		log.Fatalf("agent failed: %v", err)
	}

	// Wait for termination signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh
	log.Printf("received %s, shutting down", sig)

	agent.Shutdown()
}
