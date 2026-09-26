package main

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"learn101/helper/logger"
	"learn101/internal/coreuser"
)

func printUserStatus(coreUserService *coreuser.CoreUserService) {
	listUser, err := coreUserService.GetAll()
	if err != nil {
		slog.Error("failed fetching all users", slog.String("err", err.Error()))
		return
	}

	fmt.Println("\n [List Of All Users]")
	for _, user := range listUser {
		fmt.Printf(" - Name: %s | Email: %s | Status: %v -\n", user.Name, user.Email, user.IsActive())
	}
}

func main() {
	logger.Init()

	coreUserService := coreuser.NewUserService()
	printUserStatus(coreUserService)

	// Interactive CLI Loop
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Println("\n--- MENU ---")
		fmt.Println("1. List Users")
		fmt.Println("2. Activate User")
		fmt.Println("3. Deactivate User")
		fmt.Println("4. Exit")
		fmt.Print("Choose an option: ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		switch input {
		case "1":
			printUserStatus(coreUserService)
		case "2", "3":
			fmt.Print("Enter User ID: ")
			idInput, _ := reader.ReadString('\n')
			id, err := strconv.ParseInt(strings.TrimSpace(idInput), 10, 64)
			if err != nil {
				slog.Error("Invalid ID format", slog.String("input", idInput))
				continue
			}

			if input == "2" {
				user, err := coreUserService.SetActive(id)
				if err != nil {
					slog.Error("Failed to activate", slog.String("err", err.Error()))
				} else {
					slog.Info("User activated successfully", slog.Any("user", user))
				}
			} else {
				user, err := coreUserService.SetInactive(id)
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
