package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Miku0139oao/bili-scroll-agent/internal/app"
	"github.com/Miku0139oao/bili-scroll-agent/internal/config"
	"github.com/Miku0139oao/bili-scroll-agent/internal/runner"
)

func main() {
	command := "serve"
	if len(os.Args) > 1 && !startsWithDash(os.Args[1]) {
		command = os.Args[1]
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}
	settings, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	switch command {
	case "serve":
		serve(&settings)
	case "create-admin":
		createAdmin(&settings)
	case "reset-totp":
		resetTOTP(&settings)
	case "doctor":
		doctor(&settings)
	case "backup":
		backup(&settings)
	default:
		log.Fatalf("unknown command: %s", command)
	}
}

func serve(settings *config.Settings) {
	serveFlags := flag.NewFlagSet("serve", flag.ExitOnError)
	host := serveFlags.String("host", settings.Host, "bind host")
	port := serveFlags.Int("port", settings.Port, "bind port")
	_ = serveFlags.Parse(os.Args[1:])
	settings.Host = *host
	settings.Port = *port
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	application, err := app.NewCLI(ctx, settings)
	if err != nil {
		log.Fatal(err)
	}
	defer application.Close()
	application.Start(ctx)
	server := &http.Server{
		Addr:              settings.Addr(),
		Handler:           application.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		log.Printf("Bili Scroll Go listening on http://%s", settings.Addr())
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
}

func createAdmin(settings *config.Settings) {
	flags := flag.NewFlagSet("create-admin", flag.ExitOnError)
	username := flags.String("username", "", "admin username")
	displayName := flags.String("display-name", "", "display name")
	password := flags.String("password", "", "password")
	_ = flags.Parse(os.Args[1:])
	ctx := context.Background()
	application, err := app.NewCLI(ctx, settings)
	if err != nil {
		log.Fatal(err)
	}
	defer application.Close()
	if *username == "" {
		*username, err = app.PromptLine("Admin username: ")
		if err != nil {
			log.Fatal(err)
		}
	}
	if *displayName == "" {
		*displayName, _ = app.PromptLine("Display name: ")
	}
	secret, uri, err := app.CreateAdmin(ctx, application, *username, *displayName, *password)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Created admin:", *username)
	app.PrintTOTPSetup(*username, secret, uri)
}

func resetTOTP(settings *config.Settings) {
	flags := flag.NewFlagSet("reset-totp", flag.ExitOnError)
	username := flags.String("username", "", "username")
	_ = flags.Parse(os.Args[1:])
	if *username == "" {
		var err error
		*username, err = app.PromptLine("Username: ")
		if err != nil {
			log.Fatal(err)
		}
	}
	ctx := context.Background()
	application, err := app.New(ctx, settings)
	if err != nil {
		log.Fatal(err)
	}
	defer application.Close()
	secret, uri, err := app.ResetTOTP(ctx, application, *username)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Reset TOTP for:", *username)
	app.PrintTOTPSetup(*username, secret, uri)
}

func doctor(settings *config.Settings) {
	fmt.Println("data_dir=" + settings.DataDir)
	fmt.Println("database=" + settings.DatabasePath)
	for name, info := range runner.Status(settings) {
		state := "missing"
		if info.Available {
			state = "available"
		}
		fmt.Printf("%s: %s (%s; home=%s)\n", name, state, info.Command, info.Home)
	}
	ctx := context.Background()
	application, err := app.NewCLI(ctx, settings)
	if err != nil {
		log.Fatal(err)
	}
	defer application.Close()
	health, err := app.SecretHealth(ctx, application)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("secrets: TOTP %d checked / %d failed; Bilibili %d checked / %d failed\n",
		health["totp_checked"], health["totp_failed"], health["bili_checked"], health["bili_failed"])
}

func backup(settings *config.Settings) {
	ctx := context.Background()
	application, err := app.NewCLI(ctx, settings)
	if err != nil {
		log.Fatal(err)
	}
	defer application.Close()
	target, err := app.Backup(ctx, application)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(target)
}

func startsWithDash(value string) bool {
	return len(value) > 0 && value[0] == '-'
}
