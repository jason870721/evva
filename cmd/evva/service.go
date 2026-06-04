package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/johnny1110/evva/internal/swarm/service"
)

// runService dispatches `evva service <start|stop|status>`. At M0 (SPRD-1-1)
// only `start` is live — it boots the 127.0.0.1:8888 host in the foreground
// and serves /healthz + the embedded SPA placeholder until Ctrl-C. stop/status
// (daemonization, pidfile under ~/.evva/service/) land in SPRD-1-9.
func runService(args []string) {
	sub := "start"
	if len(args) > 0 {
		sub = args[0]
	}

	switch sub {
	case "start":
		serviceStart()
	case "stop", "status":
		fmt.Printf("evva service %s: not implemented yet (SPRD-1-9: daemon + pidfile)\n", sub)
	default:
		exitf(2, "evva service: unknown subcommand %q (want start|stop|status)", sub)
	}
}

func serviceStart() {
	svc := service.New(service.DefaultAddr)
	if err := svc.Listen(); err != nil {
		exitf(1, "evva service: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Printf("evva service listening on http://%s  (Ctrl-C to stop)\n", svc.Addr())
	fmt.Printf("session token: %s\n", svc.Token())
	if err := svc.Serve(ctx); err != nil {
		exitf(1, "evva service: %v", err)
	}
	fmt.Println("\nevva service stopped.")
}
