package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Napageneral/comms/internal/config"
	"github.com/Napageneral/comms/internal/db"
	"github.com/Napageneral/comms/internal/me"
	"github.com/spf13/cobra"
)

var (
	version    = "dev"
	commit     = "none"
	buildDate  = "unknown"
	jsonOutput bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "comms",
		Short: "Unified communications cartographer",
		Long: `Comms aggregates your communications across all channels 
(iMessage, Gmail, Slack, AI sessions, etc.) into a single 
queryable event store with identity resolution.`,
	}

	rootCmd.PersistentFlags().BoolVarP(&jsonOutput, "json", "j", false, "Output as JSON")

	// version command
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version info",
		Run: func(cmd *cobra.Command, args []string) {
			if jsonOutput {
				printJSON(map[string]string{
					"version": version,
					"commit":  commit,
					"date":    buildDate,
				})
			} else {
				fmt.Printf("comms %s (%s, %s)\n", version, commit, buildDate)
			}
		},
	})

	// init command
	rootCmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Initialize comms config and database",
		Run: func(cmd *cobra.Command, args []string) {
			type Result struct {
				OK         bool   `json:"ok"`
				Message    string `json:"message,omitempty"`
				ConfigDir  string `json:"config_dir,omitempty"`
				DataDir    string `json:"data_dir,omitempty"`
				DBPath     string `json:"db_path,omitempty"`
			}

			result := Result{OK: true}

			// Get directories
			configDir, err := config.GetConfigDir()
			if err != nil {
				result.OK = false
				result.Message = fmt.Sprintf("Failed to get config directory: %v", err)
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}
			result.ConfigDir = configDir

			dataDir, err := config.GetDataDir()
			if err != nil {
				result.OK = false
				result.Message = fmt.Sprintf("Failed to get data directory: %v", err)
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}
			result.DataDir = dataDir

			// Create config directory
			if err := os.MkdirAll(configDir, 0755); err != nil {
				result.OK = false
				result.Message = fmt.Sprintf("Failed to create config directory: %v", err)
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			// Create data directory
			if err := os.MkdirAll(dataDir, 0755); err != nil {
				result.OK = false
				result.Message = fmt.Sprintf("Failed to create data directory: %v", err)
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			// Initialize database
			if err := db.Init(); err != nil {
				result.OK = false
				result.Message = fmt.Sprintf("Failed to initialize database: %v", err)
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			dbPath, err := db.GetPath()
			if err != nil {
				result.OK = false
				result.Message = fmt.Sprintf("Failed to get database path: %v", err)
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}
			result.DBPath = dbPath

			result.Message = "Comms initialized successfully"

			if jsonOutput {
				printJSON(result)
			} else {
				fmt.Printf("✓ Config directory: %s\n", result.ConfigDir)
				fmt.Printf("✓ Data directory: %s\n", result.DataDir)
				fmt.Printf("✓ Database: %s\n", result.DBPath)
				fmt.Println("\nComms initialized successfully!")
			}
		},
	})

	// me command
	meCmd := &cobra.Command{
		Use:   "me",
		Short: "Configure user identity",
		Long:  "Manage your identity configuration (name, phone, email, etc.)",
	}

	// me set command
	meSetCmd := &cobra.Command{
		Use:   "set",
		Short: "Set your identity information",
		Run: func(cmd *cobra.Command, args []string) {
			type Result struct {
				OK      bool   `json:"ok"`
				Message string `json:"message,omitempty"`
			}

			name, _ := cmd.Flags().GetString("name")
			phone, _ := cmd.Flags().GetString("phone")
			email, _ := cmd.Flags().GetString("email")

			if name == "" && phone == "" && email == "" {
				result := Result{
					OK:      false,
					Message: "At least one of --name, --phone, or --email must be provided",
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			database, err := db.Open()
			if err != nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Failed to open database: %v", err),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}
			defer database.Close()

			// Set name if provided
			if name != "" {
				if err := me.SetMeName(database, name); err != nil {
					result := Result{
						OK:      false,
						Message: fmt.Sprintf("Failed to set name: %v", err),
					}
					if jsonOutput {
						printJSON(result)
					} else {
						fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
					}
					os.Exit(1)
				}
			}

			// Add phone identity if provided
			if phone != "" {
				if err := me.AddIdentity(database, "phone", phone); err != nil {
					result := Result{
						OK:      false,
						Message: fmt.Sprintf("Failed to add phone: %v", err),
					}
					if jsonOutput {
						printJSON(result)
					} else {
						fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
					}
					os.Exit(1)
				}
			}

			// Add email identity if provided
			if email != "" {
				if err := me.AddIdentity(database, "email", email); err != nil {
					result := Result{
						OK:      false,
						Message: fmt.Sprintf("Failed to add email: %v", err),
					}
					if jsonOutput {
						printJSON(result)
					} else {
						fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
					}
					os.Exit(1)
				}
			}

			result := Result{
				OK:      true,
				Message: "Identity updated successfully",
			}

			if jsonOutput {
				printJSON(result)
			} else {
				fmt.Println("✓ Identity updated successfully")
				if name != "" {
					fmt.Printf("  Name: %s\n", name)
				}
				if phone != "" {
					fmt.Printf("  Phone: %s\n", phone)
				}
				if email != "" {
					fmt.Printf("  Email: %s\n", email)
				}
			}
		},
	}

	meSetCmd.Flags().String("name", "", "Your full name")
	meSetCmd.Flags().String("phone", "", "Your phone number")
	meSetCmd.Flags().String("email", "", "Your email address")

	// me show command
	meShowCmd := &cobra.Command{
		Use:   "show",
		Short: "Show your current identity configuration",
		Run: func(cmd *cobra.Command, args []string) {
			type IdentityInfo struct {
				Channel    string `json:"channel"`
				Identifier string `json:"identifier"`
			}

			type Result struct {
				OK         bool           `json:"ok"`
				Message    string         `json:"message,omitempty"`
				Name       string         `json:"name,omitempty"`
				Identities []IdentityInfo `json:"identities,omitempty"`
			}

			database, err := db.Open()
			if err != nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Failed to open database: %v", err),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}
			defer database.Close()

			person, err := me.GetMePerson(database)
			if err != nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Failed to get identity: %v", err),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			if person == nil {
				result := Result{
					OK:      false,
					Message: "Identity not configured. Run 'comms me set --name \"Your Name\"' to configure.",
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "%s\n", result.Message)
				}
				os.Exit(1)
			}

			identities, err := me.GetIdentities(database, person.ID)
			if err != nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Failed to get identities: %v", err),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			result := Result{
				OK:   true,
				Name: person.CanonicalName,
			}

			for _, id := range identities {
				result.Identities = append(result.Identities, IdentityInfo{
					Channel:    id.Channel,
					Identifier: id.Identifier,
				})
			}

			if jsonOutput {
				printJSON(result)
			} else {
				fmt.Printf("Name: %s\n", person.CanonicalName)
				if len(identities) > 0 {
					fmt.Println("\nIdentities:")
					for _, id := range identities {
						fmt.Printf("  %s: %s\n", id.Channel, id.Identifier)
					}
				} else {
					fmt.Println("\nNo identities configured")
				}
			}
		},
	}

	meCmd.AddCommand(meSetCmd)
	meCmd.AddCommand(meShowCmd)
	rootCmd.AddCommand(meCmd)

	// TODO: Add more commands as per PRD
	// - connect
	// - adapters
	// - sync
	// - events
	// - people
	// - timeline
	// - identify
	// - tag
	// - db

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func printJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}
