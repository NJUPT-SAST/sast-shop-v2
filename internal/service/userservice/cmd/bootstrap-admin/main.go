package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/service"
)

func main() {
	userID := flag.Int64("user-id", 0, "already verified, existing active user ID")
	reason := flag.String("reason", "", "reason for initial superadministrator assignment")
	operator := flag.String("operator", "", "name of the infrastructure operator")
	flag.Parse()
	if *userID <= 0 || *reason == "" || *operator == "" {
		fmt.Fprintln(os.Stderr, "--user-id, --reason and --operator are required")
		os.Exit(2)
	}
	config.Init()
	postgres.Init()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := service.BootstrapAdmin(ctx, postgres.DB, *userID, *reason, *operator); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := postgres.DB.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Superadministrator initialized for user %d. Existing sessions were revoked; sign in again.\n", *userID)
}
