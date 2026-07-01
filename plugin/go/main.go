package main

import (
	"context"
	"github.com/canopy-network/go-plugin/contract"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// debug: print ContractConfig URLs
	for i, url := range contract.ContractConfig.TransactionTypeUrls {
		log.Printf("URL[%d]: %s", i, url)
	}
	// start the plugin and capture the running instance
	plugin := contract.StartPlugin(contract.DefaultConfig())
	// start the plugin's own HTTP server exposing custom, chain-specific RPC endpoints
	go plugin.StartRPCServer()
	// create a cancellable context that listens for kill signals
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
}
// debug - remove later
