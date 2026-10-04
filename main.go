package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"learn101/internal/config"
	"learn101/internal/dbpool"
	"learn101/internal/logger"
	"learn101/internal/repository"
)

func getUserList(coreUserRepository *repository.CoreUserRepository, ctx context.Context) {
	// List takes (limit, offset) — offset is "how many rows to skip",
	// not a page number. With offset 10 and only 2 rows, everything is skipped.
	const (
		limit  = 10
		offset = 0
	)

	listUser, err := coreUserRepository.List(ctx, limit, offset)
	if err != nil {
		slog.Error("failed fetching all users", slog.String("err", err.Error()))
		return
	}

	fmt.Println("\n [List Of All Users]")
	for _, user := range listUser {
		fmt.Printf(" - Name: %s | Email: %s | Status: %v -\n", user.Name, user.Email, user.Status)
	}
}

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("could not load .env", "err", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Build the logger, then make it the default so that anything which does not
	// receive one explicitly still emits in the configured format.
	log := logger.New(cfg)
	slog.SetDefault(log)

	log.Info("starting",
		"env", cfg.AppEnv,
		"addr", cfg.HTTPAddr,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := dbpool.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	log.Info("Connected To database", "addr", cfg.HTTPAddr)

	if !cfg.IsProduction() {
		interactiveCli(pool, ctx)
	}
	return nil
}

func interactiveCli(pool *pgxpool.Pool, ctx context.Context) {

	coreUserRepository := repository.NewCoreUserRepository(pool)
	getUserList(coreUserRepository, ctx)

	// Interactive CLI Loop
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Println("\n--- MENU ---")
		fmt.Println("1. List Users")
		fmt.Println("2. Activate User")
		fmt.Println("3. Deactivate User")
		fmt.Println("4. Exit")
		fmt.Print("Choose an option: ")

		input, err := reader.ReadString('\n')
		if err != nil {
			// stdin is closed or unreadable. Without this the loop spins forever:
			// ReadString returns "" plus an error on every single iteration,
			// which is exactly what happens when stdin is /dev/null in a container.
			return
		}
		input = strings.TrimSpace(input)

		switch input {
		case "1":
			getUserList(coreUserRepository, ctx)
		case "2", "3":
			fmt.Print("Enter User ID: ")
			idInput, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			id, err := strconv.ParseInt(strings.TrimSpace(idInput), 10, 64)
			if err != nil {
				slog.Error("Invalid ID format", slog.String("input", idInput))
				continue
			}

			if input == "2" {
				user, err := coreUserRepository.SetActive(ctx, id)
				if err != nil {
					slog.Error("Failed to activate", slog.String("err", err.Error()))
				} else {
					slog.Info("User activated successfully", slog.Any("user", user))
				}
			} else {
				user, err := coreUserRepository.SetInactive(ctx, id)
				if err != nil {
					slog.Error("Failed to deactivate", slog.String("err", err.Error()))
				} else {
					slog.Info("User deactivated successfully", slog.Any("user", user))
				}
			}
		case "4":
			slog.Info("Exiting program. Goodbye!")
			return
		default:
			fmt.Println("Unknown option, please choose 1-4.")
		}
	}
}
