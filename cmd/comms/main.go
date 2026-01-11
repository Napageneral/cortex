package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Napageneral/comms/internal/config"
	"github.com/Napageneral/comms/internal/db"
	"github.com/Napageneral/comms/internal/identify"
	"github.com/Napageneral/comms/internal/me"
	"github.com/Napageneral/comms/internal/query"
	"github.com/Napageneral/comms/internal/sync"
	"github.com/Napageneral/comms/internal/tag"
	"github.com/Napageneral/comms/internal/timeline"
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

	// adapters command
	adaptersCmd := &cobra.Command{
		Use:   "adapters",
		Short: "List configured adapters",
		Run: func(cmd *cobra.Command, args []string) {
			type AdapterInfo struct {
				Name    string `json:"name"`
				Type    string `json:"type"`
				Enabled bool   `json:"enabled"`
				Status  string `json:"status"`
			}

			type Result struct {
				OK       bool          `json:"ok"`
				Message  string        `json:"message,omitempty"`
				Adapters []AdapterInfo `json:"adapters,omitempty"`
			}

			cfg, err := config.Load()
			if err != nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Failed to load config: %v", err),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			result := Result{OK: true}

			if len(cfg.Adapters) == 0 {
				result.Message = "No adapters configured. Run 'comms connect <adapter>' to configure one."
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Println(result.Message)
				}
				return
			}

			// Determine adapter status based on enabled flag and prerequisites
			for name, adapter := range cfg.Adapters {
				status := "disabled"
				if adapter.Enabled {
					status = checkAdapterStatus(name, adapter)
				}

				result.Adapters = append(result.Adapters, AdapterInfo{
					Name:    name,
					Type:    adapter.Type,
					Enabled: adapter.Enabled,
					Status:  status,
				})
			}

			if jsonOutput {
				printJSON(result)
			} else {
				fmt.Println("Configured adapters:")
				for _, a := range result.Adapters {
					statusSymbol := "✗"
					if a.Status == "ready" {
						statusSymbol = "✓"
					}
					enabledStr := "disabled"
					if a.Enabled {
						enabledStr = "enabled"
					}
					fmt.Printf("  %s %s (%s) - %s - %s\n", statusSymbol, a.Name, a.Type, enabledStr, a.Status)
				}
			}
		},
	}
	rootCmd.AddCommand(adaptersCmd)

	// connect command
	connectCmd := &cobra.Command{
		Use:   "connect",
		Short: "Configure an adapter",
		Long:  "Configure and enable an adapter for syncing communications",
	}

	// connect imessage
	connectImessageCmd := &cobra.Command{
		Use:   "imessage",
		Short: "Configure iMessage adapter (via Eve)",
		Run: func(cmd *cobra.Command, args []string) {
			type Result struct {
				OK      bool   `json:"ok"`
				Message string `json:"message,omitempty"`
			}

			// Check if Eve database exists
			home, err := os.UserHomeDir()
			if err != nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Failed to get home directory: %v", err),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			eveDBPath := filepath.Join(home, "Library", "Application Support", "Eve", "eve.db")
			if _, err := os.Stat(eveDBPath); os.IsNotExist(err) {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Eve database not found at %s. Install and run Eve first: brew install Napageneral/tap/eve && eve init && eve sync", eveDBPath),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n\n", result.Message)
					fmt.Fprintf(os.Stderr, "To fix this:\n")
					fmt.Fprintf(os.Stderr, "  1. Install Eve: brew install Napageneral/tap/eve\n")
					fmt.Fprintf(os.Stderr, "  2. Initialize Eve: eve init\n")
					fmt.Fprintf(os.Stderr, "  3. Sync Eve: eve sync\n")
				}
				os.Exit(1)
			}

			cfg, err := config.Load()
			if err != nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Failed to load config: %v", err),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			cfg.Adapters["imessage"] = config.AdapterConfig{
				Type:    "eve",
				Enabled: true,
			}

			if err := cfg.Save(); err != nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Failed to save config: %v", err),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			result := Result{
				OK:      true,
				Message: "iMessage adapter configured successfully",
			}

			if jsonOutput {
				printJSON(result)
			} else {
				fmt.Println("✓ iMessage adapter configured")
				fmt.Printf("  Eve database: %s\n", eveDBPath)
				fmt.Println("\nRun 'comms sync' to sync iMessage events")
			}
		},
	}

	// connect gmail
	connectGmailCmd := &cobra.Command{
		Use:   "gmail",
		Short: "Configure Gmail adapter (via gogcli)",
		Run: func(cmd *cobra.Command, args []string) {
			type Result struct {
				OK      bool   `json:"ok"`
				Message string `json:"message,omitempty"`
			}

			account, _ := cmd.Flags().GetString("account")
			if account == "" {
				result := Result{
					OK:      false,
					Message: "The --account flag is required (e.g., --account user@gmail.com)",
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			// TODO: Check if gogcli is installed and authenticated
			// For now, just add the config

			cfg, err := config.Load()
			if err != nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Failed to load config: %v", err),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			cfg.Adapters["gmail"] = config.AdapterConfig{
				Type:    "gogcli",
				Enabled: true,
				Options: map[string]interface{}{
					"account": account,
				},
			}

			if err := cfg.Save(); err != nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Failed to save config: %v", err),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			result := Result{
				OK:      true,
				Message: "Gmail adapter configured successfully",
			}

			if jsonOutput {
				printJSON(result)
			} else {
				fmt.Println("✓ Gmail adapter configured")
				fmt.Printf("  Account: %s\n", account)
				fmt.Println("\nNote: Ensure gogcli is installed and authenticated:")
				fmt.Println("  brew install steipete/tap/gogcli")
				fmt.Printf("  gog auth add %s\n", account)
				fmt.Println("\nRun 'comms sync' to sync Gmail events")
			}
		},
	}
	connectGmailCmd.Flags().String("account", "", "Gmail account email address")

	connectCmd.AddCommand(connectImessageCmd)
	connectCmd.AddCommand(connectGmailCmd)
	rootCmd.AddCommand(connectCmd)

	// sync command
	syncCmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync communications from adapters",
		Long:  "Synchronize communications from all enabled adapters or a specific adapter",
		Run: func(cmd *cobra.Command, args []string) {
			type Result struct {
				OK       bool                  `json:"ok"`
				Message  string                `json:"message,omitempty"`
				Adapters []sync.AdapterResult  `json:"adapters,omitempty"`
			}

			adapterName, _ := cmd.Flags().GetString("adapter")
			full, _ := cmd.Flags().GetBool("full")

			// Load config
			cfg, err := config.Load()
			if err != nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Failed to load config: %v", err),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			// Open database
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

			// Create context
			ctx := context.Background()

			// Sync adapters
			var syncResult sync.SyncResult
			if adapterName != "" {
				syncResult = sync.SyncOne(ctx, database, cfg, adapterName, full)
			} else {
				syncResult = sync.SyncAll(ctx, database, cfg, full)
			}

			result := Result{
				OK:       syncResult.OK,
				Message:  syncResult.Message,
				Adapters: syncResult.Adapters,
			}

			if jsonOutput {
				printJSON(result)
			} else {
				if !syncResult.OK && syncResult.Message != "" {
					fmt.Fprintf(os.Stderr, "Error: %s\n", syncResult.Message)
					os.Exit(1)
				}

				if len(syncResult.Adapters) == 0 {
					fmt.Println(syncResult.Message)
					return
				}

				// Print results for each adapter
				fmt.Println("Sync results:")
				for _, adapterResult := range syncResult.Adapters {
					if adapterResult.Success {
						fmt.Printf("\n✓ %s\n", adapterResult.AdapterName)
						fmt.Printf("  Events created: %d\n", adapterResult.EventsCreated)
						fmt.Printf("  Events updated: %d\n", adapterResult.EventsUpdated)
						fmt.Printf("  Persons created: %d\n", adapterResult.PersonsCreated)
						fmt.Printf("  Duration: %s\n", adapterResult.Duration)
					} else {
						fmt.Printf("\n✗ %s\n", adapterResult.AdapterName)
						fmt.Printf("  Error: %s\n", adapterResult.Error)
					}
				}

				// If any adapter failed, exit with error code
				if !syncResult.OK {
					os.Exit(1)
				}
			}
		},
	}
	syncCmd.Flags().String("adapter", "", "Sync specific adapter (e.g., imessage, gmail)")
	syncCmd.Flags().Bool("full", false, "Force full re-sync instead of incremental")
	rootCmd.AddCommand(syncCmd)

	// identify command
	identifyCmd := &cobra.Command{
		Use:   "identify",
		Short: "Manage identity resolution and person merging",
		Long:  "List, search, merge, and manage person identities across channels",
		Run: func(cmd *cobra.Command, args []string) {
			type IdentityInfo struct {
				Channel    string `json:"channel"`
				Identifier string `json:"identifier"`
			}

			type PersonInfo struct {
				ID            string         `json:"id"`
				Name          string         `json:"name"`
				DisplayName   string         `json:"display_name,omitempty"`
				IsMe          bool           `json:"is_me"`
				Identities    []IdentityInfo `json:"identities"`
				EventCount    int            `json:"event_count"`
			}

			type Result struct {
				OK      bool         `json:"ok"`
				Message string       `json:"message,omitempty"`
				Persons []PersonInfo `json:"persons,omitempty"`
			}

			// Open database
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

			searchTerm, _ := cmd.Flags().GetString("search")

			var persons []identify.PersonWithIdentities
			if searchTerm != "" {
				persons, err = identify.Search(database, searchTerm)
				if err != nil {
					result := Result{
						OK:      false,
						Message: fmt.Sprintf("Failed to search persons: %v", err),
					}
					if jsonOutput {
						printJSON(result)
					} else {
						fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
					}
					os.Exit(1)
				}
			} else {
				persons, err = identify.ListAll(database)
				if err != nil {
					result := Result{
						OK:      false,
						Message: fmt.Sprintf("Failed to list persons: %v", err),
					}
					if jsonOutput {
						printJSON(result)
					} else {
						fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
					}
					os.Exit(1)
				}
			}

			result := Result{OK: true}

			if len(persons) == 0 {
				result.Message = "No persons found"
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Println(result.Message)
				}
				return
			}

			// Convert to result format
			for _, p := range persons {
				personInfo := PersonInfo{
					ID:         p.ID,
					Name:       p.CanonicalName,
					IsMe:       p.IsMe,
					EventCount: p.EventCount,
				}
				if p.DisplayName != nil {
					personInfo.DisplayName = *p.DisplayName
				}
				for _, id := range p.Identities {
					personInfo.Identities = append(personInfo.Identities, IdentityInfo{
						Channel:    id.Channel,
						Identifier: id.Identifier,
					})
				}
				result.Persons = append(result.Persons, personInfo)
			}

			if jsonOutput {
				printJSON(result)
			} else {
				if searchTerm != "" {
					fmt.Printf("Search results for '%s':\n\n", searchTerm)
				} else {
					fmt.Printf("All persons (%d total):\n\n", len(persons))
				}

				for _, p := range persons {
					nameStr := p.CanonicalName
					if p.IsMe {
						nameStr += " (me)"
					}
					fmt.Printf("• %s\n", nameStr)
					fmt.Printf("  ID: %s\n", p.ID)
					if p.DisplayName != nil {
						fmt.Printf("  Display name: %s\n", *p.DisplayName)
					}
					if p.EventCount > 0 {
						fmt.Printf("  Events: %d\n", p.EventCount)
					}
					if len(p.Identities) > 0 {
						fmt.Printf("  Identities:\n")
						for _, id := range p.Identities {
							fmt.Printf("    - %s: %s\n", id.Channel, id.Identifier)
						}
					}
					fmt.Println()
				}
			}
		},
	}

	identifyCmd.Flags().String("search", "", "Search for persons by name or identifier")

	// identify --merge command
	identifyMergeCmd := &cobra.Command{
		Use:   "merge <person1> <person2>",
		Short: "Merge two persons (union-find operation)",
		Long:  "Merge person2 into person1. All identities and events from person2 will be transferred to person1.",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			type Result struct {
				OK      bool   `json:"ok"`
				Message string `json:"message,omitempty"`
			}

			person1Name := args[0]
			person2Name := args[1]

			// Open database
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

			// Find person1 by name
			person1, err := identify.GetPersonByName(database, person1Name)
			if err != nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Failed to find person1: %v", err),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}
			if person1 == nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Person '%s' not found", person1Name),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			// Find person2 by name
			person2, err := identify.GetPersonByName(database, person2Name)
			if err != nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Failed to find person2: %v", err),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}
			if person2 == nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Person '%s' not found", person2Name),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			// Merge person2 into person1
			err = identify.Merge(database, person1.ID, person2.ID)
			if err != nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Failed to merge persons: %v", err),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			result := Result{
				OK:      true,
				Message: fmt.Sprintf("Successfully merged '%s' into '%s'", person2Name, person1Name),
			}

			if jsonOutput {
				printJSON(result)
			} else {
				fmt.Printf("✓ Merged '%s' into '%s'\n", person2Name, person1Name)
				fmt.Printf("  All identities and events from '%s' are now associated with '%s'\n", person2Name, person1Name)
			}
		},
	}

	// identify --add command
	identifyAddCmd := &cobra.Command{
		Use:   "add <person> --email <email> | --phone <phone> | --identifier <channel>:<identifier>",
		Short: "Add an identity to a person",
		Long:  "Add a new identity (email, phone, or custom identifier) to an existing person",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			type Result struct {
				OK      bool   `json:"ok"`
				Message string `json:"message,omitempty"`
			}

			personName := args[0]
			email, _ := cmd.Flags().GetString("email")
			phone, _ := cmd.Flags().GetString("phone")
			identifier, _ := cmd.Flags().GetString("identifier")

			if email == "" && phone == "" && identifier == "" {
				result := Result{
					OK:      false,
					Message: "At least one of --email, --phone, or --identifier must be provided",
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			// Open database
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

			// Find person by name
			person, err := identify.GetPersonByName(database, personName)
			if err != nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Failed to find person: %v", err),
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
					Message: fmt.Sprintf("Person '%s' not found", personName),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			// Add identities
			var added []string

			if email != "" {
				err = identify.AddIdentityToPerson(database, person.ID, "email", email)
				if err != nil {
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
				added = append(added, fmt.Sprintf("email: %s", email))
			}

			if phone != "" {
				err = identify.AddIdentityToPerson(database, person.ID, "phone", phone)
				if err != nil {
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
				added = append(added, fmt.Sprintf("phone: %s", phone))
			}

			if identifier != "" {
				// Parse channel:identifier format
				parts := strings.SplitN(identifier, ":", 2)
				if len(parts) != 2 {
					result := Result{
						OK:      false,
						Message: "Identifier must be in format 'channel:identifier'",
					}
					if jsonOutput {
						printJSON(result)
					} else {
						fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
					}
					os.Exit(1)
				}
				channel, id := parts[0], parts[1]

				err = identify.AddIdentityToPerson(database, person.ID, channel, id)
				if err != nil {
					result := Result{
						OK:      false,
						Message: fmt.Sprintf("Failed to add identifier: %v", err),
					}
					if jsonOutput {
						printJSON(result)
					} else {
						fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
					}
					os.Exit(1)
				}
				added = append(added, fmt.Sprintf("%s: %s", channel, id))
			}

			result := Result{
				OK:      true,
				Message: fmt.Sprintf("Successfully added identities to '%s'", personName),
			}

			if jsonOutput {
				printJSON(result)
			} else {
				fmt.Printf("✓ Added identities to '%s':\n", personName)
				for _, a := range added {
					fmt.Printf("  - %s\n", a)
				}
			}
		},
	}

	identifyAddCmd.Flags().String("email", "", "Email address to add")
	identifyAddCmd.Flags().String("phone", "", "Phone number to add")
	identifyAddCmd.Flags().String("identifier", "", "Custom identifier in format 'channel:identifier'")

	identifyCmd.AddCommand(identifyMergeCmd)
	identifyCmd.AddCommand(identifyAddCmd)
	rootCmd.AddCommand(identifyCmd)

	// events command
	eventsCmd := &cobra.Command{
		Use:   "events",
		Short: "Query communication events",
		Long:  "Query and filter communication events across all channels",
		Run: func(cmd *cobra.Command, args []string) {
			type ParticipantInfo struct {
				Name string `json:"name"`
				Role string `json:"role"`
			}

			type EventInfo struct {
				ID           string            `json:"id"`
				Timestamp    int64             `json:"timestamp"`
				TimestampStr string            `json:"timestamp_str"`
				Channel      string            `json:"channel"`
				ContentTypes string            `json:"content_types"`
				Content      string            `json:"content"`
				Direction    string            `json:"direction"`
				ThreadID     *string           `json:"thread_id,omitempty"`
				ReplyTo      *string           `json:"reply_to,omitempty"`
				Participants []ParticipantInfo `json:"participants"`
			}

			type Result struct {
				OK      bool        `json:"ok"`
				Message string      `json:"message,omitempty"`
				Count   int         `json:"count"`
				Events  []EventInfo `json:"events,omitempty"`
			}

			// Parse flags
			personName, _ := cmd.Flags().GetString("person")
			channel, _ := cmd.Flags().GetString("channel")
			sinceStr, _ := cmd.Flags().GetString("since")
			untilStr, _ := cmd.Flags().GetString("until")
			direction, _ := cmd.Flags().GetString("direction")
			limit, _ := cmd.Flags().GetInt("limit")

			// Build filters
			filters := query.EventFilters{
				PersonName: personName,
				Channel:    channel,
				Direction:  direction,
				Limit:      limit,
			}

			// Parse since date
			if sinceStr != "" {
				since, err := parseDate(sinceStr)
				if err != nil {
					result := Result{
						OK:      false,
						Message: fmt.Sprintf("Invalid since date: %v. Use format YYYY-MM-DD", err),
					}
					if jsonOutput {
						printJSON(result)
					} else {
						fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
					}
					os.Exit(1)
				}
				filters.Since = since
			}

			// Parse until date
			if untilStr != "" {
				until, err := parseDate(untilStr)
				if err != nil {
					result := Result{
						OK:      false,
						Message: fmt.Sprintf("Invalid until date: %v. Use format YYYY-MM-DD", err),
					}
					if jsonOutput {
						printJSON(result)
					} else {
						fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
					}
					os.Exit(1)
				}
				filters.Until = until
			}

			// Validate direction
			if direction != "" && direction != "sent" && direction != "received" && direction != "observed" {
				result := Result{
					OK:      false,
					Message: "Invalid direction. Must be one of: sent, received, observed",
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			// Open database
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

			// Query events
			events, err := query.QueryEvents(database, filters)
			if err != nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Failed to query events: %v", err),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			result := Result{
				OK:    true,
				Count: len(events),
			}

			// Convert events to result format
			for _, e := range events {
				eventInfo := EventInfo{
					ID:           e.ID,
					Timestamp:    e.Timestamp,
					TimestampStr: query.FormatTimestamp(e.Timestamp),
					Channel:      e.Channel,
					ContentTypes: e.ContentTypes,
					Content:      e.Content,
					Direction:    e.Direction,
					ThreadID:     e.ThreadID,
					ReplyTo:      e.ReplyTo,
				}

				for _, p := range e.Participants {
					eventInfo.Participants = append(eventInfo.Participants, ParticipantInfo{
						Name: p.Name,
						Role: p.Role,
					})
				}

				result.Events = append(result.Events, eventInfo)
			}

			if jsonOutput {
				printJSON(result)
			} else {
				if len(events) == 0 {
					fmt.Println("No events found matching the specified filters.")
					return
				}

				// Build filter description
				filterParts := []string{}
				if personName != "" {
					filterParts = append(filterParts, fmt.Sprintf("person: %s", personName))
				}
				if channel != "" {
					filterParts = append(filterParts, fmt.Sprintf("channel: %s", channel))
				}
				if direction != "" {
					filterParts = append(filterParts, fmt.Sprintf("direction: %s", direction))
				}
				if sinceStr != "" {
					filterParts = append(filterParts, fmt.Sprintf("since: %s", sinceStr))
				}
				if untilStr != "" {
					filterParts = append(filterParts, fmt.Sprintf("until: %s", untilStr))
				}

				if len(filterParts) > 0 {
					fmt.Printf("Events (%s):\n", strings.Join(filterParts, ", "))
				} else {
					fmt.Printf("Recent events (showing up to %d):\n", limit)
				}
				fmt.Printf("Found %d event(s)\n\n", len(events))

				for i, e := range events {
					if i > 0 {
						fmt.Println("---")
					}

					fmt.Printf("ID: %s\n", e.ID)
					fmt.Printf("Time: %s\n", query.FormatTimestamp(e.Timestamp))
					fmt.Printf("Channel: %s\n", e.Channel)
					fmt.Printf("Direction: %s\n", e.Direction)

					if len(e.Participants) > 0 {
						fmt.Println("Participants:")
						for _, p := range e.Participants {
							fmt.Printf("  - %s (%s)\n", p.Name, p.Role)
						}
					}

					if e.Content != "" {
						// Truncate long content for display
						content := e.Content
						if len(content) > 200 {
							content = content[:200] + "..."
						}
						fmt.Printf("Content: %s\n", content)
					}

					if e.ThreadID != nil {
						fmt.Printf("Thread: %s\n", *e.ThreadID)
					}
				}
			}
		},
	}

	eventsCmd.Flags().String("person", "", "Filter by person name")
	eventsCmd.Flags().String("channel", "", "Filter by channel (e.g., imessage, gmail)")
	eventsCmd.Flags().String("since", "", "Filter by start date (YYYY-MM-DD)")
	eventsCmd.Flags().String("until", "", "Filter by end date (YYYY-MM-DD)")
	eventsCmd.Flags().String("direction", "", "Filter by direction (sent, received, observed)")
	eventsCmd.Flags().Int("limit", 100, "Maximum number of events to return")
	rootCmd.AddCommand(eventsCmd)

	// people command
	peopleCmd := &cobra.Command{
		Use:   "people [name]",
		Short: "List and search contacts",
		Long:  "List all persons, search by name, or show details for a specific person",
		Args:  cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			type IdentityInfo struct {
				Channel    string `json:"channel"`
				Identifier string `json:"identifier"`
			}

			type PersonInfo struct {
				ID            string         `json:"id"`
				Name          string         `json:"name"`
				DisplayName   string         `json:"display_name,omitempty"`
				IsMe          bool           `json:"is_me"`
				Relationship  string         `json:"relationship,omitempty"`
				Identities    []IdentityInfo `json:"identities"`
				EventCount    int            `json:"event_count"`
				LastEventAt   string         `json:"last_event_at,omitempty"`
			}

			type Result struct {
				OK      bool         `json:"ok"`
				Message string       `json:"message,omitempty"`
				Count   int          `json:"count,omitempty"`
				Persons []PersonInfo `json:"persons,omitempty"`
			}

			// Open database
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

			// Handle specific person detail view
			if len(args) == 1 {
				personName := args[0]
				person, err := identify.GetPersonByName(database, personName)
				if err != nil {
					result := Result{
						OK:      false,
						Message: fmt.Sprintf("Failed to get person: %v", err),
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
						Message: fmt.Sprintf("Person '%s' not found", personName),
					}
					if jsonOutput {
						printJSON(result)
					} else {
						fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
					}
					os.Exit(1)
				}

				// Convert to result format
				personInfo := PersonInfo{
					ID:         person.ID,
					Name:       person.CanonicalName,
					IsMe:       person.IsMe,
					EventCount: person.EventCount,
				}
				if person.DisplayName != nil {
					personInfo.DisplayName = *person.DisplayName
				}
				if person.RelationType != nil {
					personInfo.Relationship = *person.RelationType
				}
				if person.LastEventAt != nil {
					personInfo.LastEventAt = query.FormatTimestamp(person.LastEventAt.Unix())
				}
				for _, id := range person.Identities {
					personInfo.Identities = append(personInfo.Identities, IdentityInfo{
						Channel:    id.Channel,
						Identifier: id.Identifier,
					})
				}

				result := Result{
					OK:      true,
					Count:   1,
					Persons: []PersonInfo{personInfo},
				}

				if jsonOutput {
					printJSON(result)
				} else {
					nameStr := person.CanonicalName
					if person.IsMe {
						nameStr += " (me)"
					}
					fmt.Printf("%s\n", nameStr)
					fmt.Printf("ID: %s\n", person.ID)
					if person.DisplayName != nil {
						fmt.Printf("Display name: %s\n", *person.DisplayName)
					}
					if person.RelationType != nil {
						fmt.Printf("Relationship: %s\n", *person.RelationType)
					}
					fmt.Printf("\nStatistics:\n")
					fmt.Printf("  Events: %d\n", person.EventCount)
					if person.LastEventAt != nil {
						fmt.Printf("  Last event: %s\n", query.FormatTimestamp(person.LastEventAt.Unix()))
					}
					if len(person.Identities) > 0 {
						fmt.Printf("\nIdentities:\n")
						for _, id := range person.Identities {
							fmt.Printf("  - %s: %s\n", id.Channel, id.Identifier)
						}
					}
				}
				return
			}

			// Handle list/search mode
			searchTerm, _ := cmd.Flags().GetString("search")
			topN, _ := cmd.Flags().GetInt("top")

			var persons []identify.PersonWithIdentities
			if searchTerm != "" {
				persons, err = identify.Search(database, searchTerm)
				if err != nil {
					result := Result{
						OK:      false,
						Message: fmt.Sprintf("Failed to search persons: %v", err),
					}
					if jsonOutput {
						printJSON(result)
					} else {
						fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
					}
					os.Exit(1)
				}
			} else {
				persons, err = identify.ListAll(database)
				if err != nil {
					result := Result{
						OK:      false,
						Message: fmt.Sprintf("Failed to list persons: %v", err),
					}
					if jsonOutput {
						printJSON(result)
					} else {
						fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
					}
					os.Exit(1)
				}
			}

			// Apply top N limit if specified
			if topN > 0 && topN < len(persons) {
				persons = persons[:topN]
			}

			result := Result{
				OK:    true,
				Count: len(persons),
			}

			if len(persons) == 0 {
				result.Message = "No persons found"
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Println(result.Message)
				}
				return
			}

			// Convert to result format
			for _, p := range persons {
				personInfo := PersonInfo{
					ID:         p.ID,
					Name:       p.CanonicalName,
					IsMe:       p.IsMe,
					EventCount: p.EventCount,
				}
				if p.DisplayName != nil {
					personInfo.DisplayName = *p.DisplayName
				}
				if p.RelationType != nil {
					personInfo.Relationship = *p.RelationType
				}
				if p.LastEventAt != nil {
					personInfo.LastEventAt = query.FormatTimestamp(p.LastEventAt.Unix())
				}
				for _, id := range p.Identities {
					personInfo.Identities = append(personInfo.Identities, IdentityInfo{
						Channel:    id.Channel,
						Identifier: id.Identifier,
					})
				}
				result.Persons = append(result.Persons, personInfo)
			}

			if jsonOutput {
				printJSON(result)
			} else {
				// Print header
				if searchTerm != "" {
					fmt.Printf("Search results for '%s':\n", searchTerm)
				} else if topN > 0 {
					fmt.Printf("Top %d contacts by event count:\n", len(persons))
				} else {
					fmt.Printf("All contacts (%d total):\n", len(persons))
				}
				fmt.Println()

				// Print person list
				for _, p := range persons {
					nameStr := p.CanonicalName
					if p.IsMe {
						nameStr += " (me)"
					}
					if p.DisplayName != nil && *p.DisplayName != p.CanonicalName {
						nameStr += fmt.Sprintf(" [%s]", *p.DisplayName)
					}

					fmt.Printf("• %s\n", nameStr)
					if p.EventCount > 0 {
						fmt.Printf("  Events: %d", p.EventCount)
						if p.LastEventAt != nil {
							fmt.Printf(" (last: %s)", query.FormatTimestamp(p.LastEventAt.Unix()))
						}
						fmt.Println()
					}
					if len(p.Identities) > 0 {
						fmt.Printf("  Identities: ")
						identStrs := []string{}
						for _, id := range p.Identities {
							identStrs = append(identStrs, fmt.Sprintf("%s:%s", id.Channel, id.Identifier))
						}
						fmt.Println(strings.Join(identStrs, ", "))
					}
					fmt.Println()
				}
			}
		},
	}

	peopleCmd.Flags().String("search", "", "Search for persons by name")
	peopleCmd.Flags().Int("top", 0, "Show only top N persons by event count")
	rootCmd.AddCommand(peopleCmd)

	// timeline command
	timelineCmd := &cobra.Command{
		Use:   "timeline [period]",
		Short: "Show events in a time period",
		Long: `Display events grouped by day for a specified time period.

Examples:
  comms timeline 2026-01         # January 2026
  comms timeline 2026-01-15      # Specific day
  comms timeline --today         # Today's events
  comms timeline --week          # This week (Mon-Sun)`,
		Args: cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			type DayResult struct {
				Date        string         `json:"date"`
				TotalEvents int            `json:"total_events"`
				BySender    map[string]int `json:"by_sender"`
				ByChannel   map[string]int `json:"by_channel"`
				ByDirection map[string]int `json:"by_direction"`
			}

			type Result struct {
				OK        bool        `json:"ok"`
				Message   string      `json:"message,omitempty"`
				StartDate string      `json:"start_date,omitempty"`
				EndDate   string      `json:"end_date,omitempty"`
				Days      []DayResult `json:"days,omitempty"`
			}

			result := Result{OK: true}

			// Determine time range
			var opts timeline.TimelineOptions
			var rangeDesc string

			useToday, _ := cmd.Flags().GetBool("today")
			useWeek, _ := cmd.Flags().GetBool("week")

			if useToday {
				opts = timeline.GetTodayRange()
				rangeDesc = "today"
			} else if useWeek {
				opts = timeline.GetWeekRange()
				rangeDesc = "this week"
			} else if len(args) == 1 {
				var err error
				opts, err = timeline.ParseTimelineArg(args[0])
				if err != nil {
					result.OK = false
					result.Message = err.Error()
					if jsonOutput {
						printJSON(result)
					} else {
						fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
					}
					os.Exit(1)
				}
				rangeDesc = args[0]
			} else {
				result.OK = false
				result.Message = "Please specify a time period (e.g., '2026-01') or use --today or --week"
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n\n", result.Message)
					fmt.Fprintf(os.Stderr, "Usage:\n")
					fmt.Fprintf(os.Stderr, "  comms timeline 2026-01         # Month view\n")
					fmt.Fprintf(os.Stderr, "  comms timeline 2026-01-15      # Single day\n")
					fmt.Fprintf(os.Stderr, "  comms timeline --today         # Today\n")
					fmt.Fprintf(os.Stderr, "  comms timeline --week          # This week\n")
				}
				os.Exit(1)
			}

			result.StartDate = opts.StartDate.Format("2006-01-02")
			result.EndDate = opts.EndDate.Format("2006-01-02")

			// Open database
			database, err := db.Open()
			if err != nil {
				result.OK = false
				result.Message = fmt.Sprintf("Failed to open database: %v", err)
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}
			defer database.Close()

			// Query timeline
			days, err := timeline.QueryTimeline(database, opts)
			if err != nil {
				result.OK = false
				result.Message = fmt.Sprintf("Failed to query timeline: %v", err)
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			// Convert to result format
			for _, day := range days {
				result.Days = append(result.Days, DayResult{
					Date:        day.Date,
					TotalEvents: day.TotalEvents,
					BySender:    day.BySender,
					ByChannel:   day.ByChannel,
					ByDirection: day.ByDirection,
				})
			}

			if jsonOutput {
				printJSON(result)
			} else {
				// Text output
				fmt.Printf("Timeline for %s (%s to %s)\n\n", rangeDesc, result.StartDate, result.EndDate)

				if len(days) == 0 {
					fmt.Println("No events found in this time period.")
					return
				}

				for _, day := range days {
					fmt.Printf("📅 %s (%d events)\n", day.Date, day.TotalEvents)

					// Show top senders
					if len(day.BySender) > 0 {
						fmt.Print("  Senders: ")
						senderStrs := []string{}
						for sender, count := range day.BySender {
							senderStrs = append(senderStrs, fmt.Sprintf("%s (%d)", sender, count))
						}
						fmt.Println(strings.Join(senderStrs, ", "))
					}

					// Show channels
					if len(day.ByChannel) > 0 {
						fmt.Print("  Channels: ")
						channelStrs := []string{}
						for channel, count := range day.ByChannel {
							channelStrs = append(channelStrs, fmt.Sprintf("%s (%d)", channel, count))
						}
						fmt.Println(strings.Join(channelStrs, ", "))
					}

					// Show direction breakdown
					if len(day.ByDirection) > 0 {
						fmt.Print("  Direction: ")
						dirStrs := []string{}
						for direction, count := range day.ByDirection {
							dirStrs = append(dirStrs, fmt.Sprintf("%s (%d)", direction, count))
						}
						fmt.Println(strings.Join(dirStrs, ", "))
					}

					fmt.Println()
				}
			}
		},
	}

	timelineCmd.Flags().Bool("today", false, "Show today's events")
	timelineCmd.Flags().Bool("week", false, "Show this week's events")
	rootCmd.AddCommand(timelineCmd)

	// tag command
	tagCmd := &cobra.Command{
		Use:   "tag",
		Short: "Manage event tags",
		Long:  "Apply and manage soft tags on events for categorization and discovery",
	}

	// tag list command
	tagListCmd := &cobra.Command{
		Use:   "list",
		Short: "List all tags",
		Long:  "List all tags or filter by type",
		Run: func(cmd *cobra.Command, args []string) {
			type TagInfo struct {
				ID             string   `json:"id"`
				EventID        string   `json:"event_id"`
				TagType        string   `json:"tag_type"`
				Value          string   `json:"value"`
				Confidence     *float64 `json:"confidence,omitempty"`
				Source         string   `json:"source"`
				EventTimestamp int64    `json:"event_timestamp,omitempty"`
				EventChannel   string   `json:"event_channel,omitempty"`
			}

			type Result struct {
				OK      bool      `json:"ok"`
				Message string    `json:"message,omitempty"`
				Count   int       `json:"count"`
				Tags    []TagInfo `json:"tags,omitempty"`
			}

			// Open database
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

			// Get filter flags
			tagType, _ := cmd.Flags().GetString("type")

			var tags []tag.TagWithEvent
			if tagType != "" {
				tags, err = tag.ListByType(database, tagType)
			} else {
				tags, err = tag.ListAll(database)
			}

			if err != nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Failed to list tags: %v", err),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			result := Result{
				OK:    true,
				Count: len(tags),
			}

			// Convert to result format
			for _, t := range tags {
				tagInfo := TagInfo{
					ID:             t.ID,
					EventID:        t.EventID,
					TagType:        t.TagType,
					Value:          t.Value,
					Confidence:     t.Confidence,
					Source:         t.Source,
					EventTimestamp: t.EventTimestamp,
					EventChannel:   t.EventChannel,
				}
				result.Tags = append(result.Tags, tagInfo)
			}

			if jsonOutput {
				printJSON(result)
			} else {
				if len(tags) == 0 {
					fmt.Println("No tags found")
					return
				}

				if tagType != "" {
					fmt.Printf("Tags of type '%s' (%d total):\n\n", tagType, len(tags))
				} else {
					fmt.Printf("All tags (%d total):\n\n", len(tags))
				}

				for _, t := range tags {
					fmt.Printf("• %s:%s\n", t.TagType, t.Value)
					fmt.Printf("  ID: %s\n", t.ID)
					fmt.Printf("  Event: %s (%s)\n", t.EventID, t.EventChannel)
					fmt.Printf("  Source: %s\n", t.Source)
					if t.Confidence != nil {
						fmt.Printf("  Confidence: %.2f\n", *t.Confidence)
					}
					// Show event content preview
					if t.EventContent != "" {
						content := t.EventContent
						if len(content) > 100 {
							content = content[:100] + "..."
						}
						fmt.Printf("  Event content: %s\n", content)
					}
					fmt.Println()
				}
			}
		},
	}

	tagListCmd.Flags().String("type", "", "Filter by tag type (topic, entity, emotion, project, context)")

	// tag add command
	tagAddCmd := &cobra.Command{
		Use:   "add",
		Short: "Add tags to events",
		Long: `Add a tag to a specific event or multiple events matching a filter.

Examples:
  # Tag a specific event
  comms tag add --event abc123 --tag project:htaa

  # Bulk tag events by person
  comms tag add --person "Dane" --tag context:business

  # Bulk tag events by channel and time
  comms tag add --channel imessage --since 2026-01-01 --tag topic:planning`,
		Run: func(cmd *cobra.Command, args []string) {
			type Result struct {
				OK            bool   `json:"ok"`
				Message       string `json:"message,omitempty"`
				EventsTagged  int    `json:"events_tagged,omitempty"`
			}

			// Parse flags
			eventID, _ := cmd.Flags().GetString("event")
			tagStr, _ := cmd.Flags().GetString("tag")
			personName, _ := cmd.Flags().GetString("person")
			channel, _ := cmd.Flags().GetString("channel")
			sinceStr, _ := cmd.Flags().GetString("since")
			untilStr, _ := cmd.Flags().GetString("until")
			confidenceVal, _ := cmd.Flags().GetFloat64("confidence")

			// Validate required flags
			if tagStr == "" {
				result := Result{
					OK:      false,
					Message: "The --tag flag is required",
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			// Parse tag (format: type:value)
			parts := strings.SplitN(tagStr, ":", 2)
			if len(parts) != 2 {
				result := Result{
					OK:      false,
					Message: "Tag must be in format 'type:value' (e.g., 'project:htaa')",
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}
			tagType, tagValue := parts[0], parts[1]

			// Determine mode: single event or bulk
			if eventID == "" && personName == "" && channel == "" && sinceStr == "" && untilStr == "" {
				result := Result{
					OK:      false,
					Message: "Either --event or at least one filter (--person, --channel, --since, --until) must be provided",
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			// Open database
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

			// Handle confidence
			var confidence *float64
			if confidenceVal > 0 {
				confidence = &confidenceVal
			}

			// Single event mode
			if eventID != "" {
				err = tag.Add(database, eventID, tagType, tagValue, confidence, "user")
				if err != nil {
					result := Result{
						OK:      false,
						Message: fmt.Sprintf("Failed to add tag: %v", err),
					}
					if jsonOutput {
						printJSON(result)
					} else {
						fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
					}
					os.Exit(1)
				}

				result := Result{
					OK:           true,
					Message:      fmt.Sprintf("Tag '%s:%s' added to event %s", tagType, tagValue, eventID),
					EventsTagged: 1,
				}

				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Printf("✓ Added tag '%s:%s' to event %s\n", tagType, tagValue, eventID)
				}
				return
			}

			// Bulk mode - build filter
			filter := tag.EventFilter{
				PersonName: personName,
				Channel:    channel,
			}

			// Parse dates
			if sinceStr != "" {
				since, err := parseDate(sinceStr)
				if err != nil {
					result := Result{
						OK:      false,
						Message: fmt.Sprintf("Invalid since date: %v. Use format YYYY-MM-DD", err),
					}
					if jsonOutput {
						printJSON(result)
					} else {
						fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
					}
					os.Exit(1)
				}
				filter.Since = &since
			}

			if untilStr != "" {
				until, err := parseDate(untilStr)
				if err != nil {
					result := Result{
						OK:      false,
						Message: fmt.Sprintf("Invalid until date: %v. Use format YYYY-MM-DD", err),
					}
					if jsonOutput {
						printJSON(result)
					} else {
						fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
					}
					os.Exit(1)
				}
				filter.Until = &until
			}

			// Add tags in bulk
			tagged, err := tag.AddBulk(database, filter, tagType, tagValue, confidence, "user")
			if err != nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Failed to add tags: %v", err),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			result := Result{
				OK:           true,
				Message:      fmt.Sprintf("Tag '%s:%s' added to %d event(s)", tagType, tagValue, tagged),
				EventsTagged: tagged,
			}

			if jsonOutput {
				printJSON(result)
			} else {
				fmt.Printf("✓ Added tag '%s:%s' to %d event(s)\n", tagType, tagValue, tagged)

				// Show filter description
				filterParts := []string{}
				if personName != "" {
					filterParts = append(filterParts, fmt.Sprintf("person: %s", personName))
				}
				if channel != "" {
					filterParts = append(filterParts, fmt.Sprintf("channel: %s", channel))
				}
				if sinceStr != "" {
					filterParts = append(filterParts, fmt.Sprintf("since: %s", sinceStr))
				}
				if untilStr != "" {
					filterParts = append(filterParts, fmt.Sprintf("until: %s", untilStr))
				}

				if len(filterParts) > 0 {
					fmt.Printf("  Filter: %s\n", strings.Join(filterParts, ", "))
				}
			}
		},
	}

	tagAddCmd.Flags().String("event", "", "Event ID to tag")
	tagAddCmd.Flags().String("tag", "", "Tag in format 'type:value' (e.g., 'project:htaa')")
	tagAddCmd.Flags().String("person", "", "Filter events by person name (bulk mode)")
	tagAddCmd.Flags().String("channel", "", "Filter events by channel (bulk mode)")
	tagAddCmd.Flags().String("since", "", "Filter events since date YYYY-MM-DD (bulk mode)")
	tagAddCmd.Flags().String("until", "", "Filter events until date YYYY-MM-DD (bulk mode)")
	tagAddCmd.Flags().Float64("confidence", 0, "Confidence score (0.0-1.0) for analysis-discovered tags")

	tagCmd.AddCommand(tagListCmd)
	tagCmd.AddCommand(tagAddCmd)
	rootCmd.AddCommand(tagCmd)

	// db command
	dbCmd := &cobra.Command{
		Use:   "db",
		Short: "Database operations",
		Long:  "Perform database operations like raw SQL queries",
	}

	// db query command
	dbQueryCmd := &cobra.Command{
		Use:   "query <sql>",
		Short: "Execute raw SQL query",
		Long: `Execute a raw SQL query against the comms database.

By default, only SELECT statements are allowed for safety.
Use --write flag to allow mutations (INSERT, UPDATE, DELETE, etc.).

Examples:
  comms db query "SELECT COUNT(*) FROM events"
  comms db query "SELECT * FROM persons LIMIT 10"
  comms db query --write "UPDATE persons SET display_name = 'Dad' WHERE canonical_name = 'Father'"`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			type Result struct {
				OK      bool          `json:"ok"`
				Message string        `json:"message,omitempty"`
				Rows    []map[string]interface{} `json:"rows,omitempty"`
				Count   int           `json:"count,omitempty"`
			}

			sqlQuery := args[0]
			allowWrite, _ := cmd.Flags().GetBool("write")

			// Check if it's a mutation query without --write flag
			upperQuery := strings.ToUpper(strings.TrimSpace(sqlQuery))
			isMutation := false
			for _, keyword := range []string{"INSERT", "UPDATE", "DELETE", "DROP", "CREATE", "ALTER", "TRUNCATE"} {
				if strings.HasPrefix(upperQuery, keyword) {
					isMutation = true
					break
				}
			}

			if isMutation && !allowWrite {
				result := Result{
					OK:      false,
					Message: "Query appears to modify data. Use --write flag to allow mutations.",
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			// Open database
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

			// Execute query
			rows, err := database.Query(sqlQuery)
			if err != nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Query failed: %v", err),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}
			defer rows.Close()

			// Get column names
			columns, err := rows.Columns()
			if err != nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Failed to get columns: %v", err),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			// Fetch results
			results := []map[string]interface{}{}
			for rows.Next() {
				// Create a slice of interface{} to hold each column value
				values := make([]interface{}, len(columns))
				valuePtrs := make([]interface{}, len(columns))
				for i := range values {
					valuePtrs[i] = &values[i]
				}

				// Scan the row
				if err := rows.Scan(valuePtrs...); err != nil {
					result := Result{
						OK:      false,
						Message: fmt.Sprintf("Failed to scan row: %v", err),
					}
					if jsonOutput {
						printJSON(result)
					} else {
						fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
					}
					os.Exit(1)
				}

				// Build map for this row
				rowMap := make(map[string]interface{})
				for i, col := range columns {
					val := values[i]
					// Convert []byte to string for text fields
					if b, ok := val.([]byte); ok {
						rowMap[col] = string(b)
					} else {
						rowMap[col] = val
					}
				}
				results = append(results, rowMap)
			}

			if err := rows.Err(); err != nil {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("Error iterating rows: %v", err),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			result := Result{
				OK:    true,
				Rows:  results,
				Count: len(results),
			}

			if jsonOutput {
				printJSON(result)
			} else {
				// Text output
				if len(results) == 0 {
					fmt.Println("No rows returned.")
				} else {
					// Print as a simple table
					// Print header
					fmt.Println(strings.Join(columns, "\t"))
					fmt.Println(strings.Repeat("-", 80))

					// Print rows
					for _, row := range results {
						values := make([]string, len(columns))
						for i, col := range columns {
							val := row[col]
							if val == nil {
								values[i] = "NULL"
							} else {
								values[i] = fmt.Sprintf("%v", val)
							}
						}
						fmt.Println(strings.Join(values, "\t"))
					}

					fmt.Printf("\n%d row(s) returned\n", len(results))
				}
			}
		},
	}

	dbQueryCmd.Flags().Bool("write", false, "Allow mutation queries (INSERT, UPDATE, DELETE, etc.)")
	dbCmd.AddCommand(dbQueryCmd)
	rootCmd.AddCommand(dbCmd)

	// TODO: Add more commands as per PRD

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func printJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

// parseDate parses a date string in YYYY-MM-DD format
func parseDate(dateStr string) (time.Time, error) {
	return time.Parse("2006-01-02", dateStr)
}

// checkAdapterStatus checks if an adapter's prerequisites are met
func checkAdapterStatus(name string, adapter config.AdapterConfig) string {
	switch adapter.Type {
	case "eve":
		// Check if Eve database exists
		home, err := os.UserHomeDir()
		if err != nil {
			return "error"
		}
		eveDBPath := filepath.Join(home, "Library", "Application Support", "Eve", "eve.db")
		if _, err := os.Stat(eveDBPath); os.IsNotExist(err) {
			return "missing Eve database"
		}
		return "ready"

	case "gogcli":
		// Check if account is configured
		if account, ok := adapter.Options["account"].(string); ok && account != "" {
			// TODO: Check if gogcli is installed and authenticated
			// For now, assume ready if account is configured
			return "ready (check gogcli auth)"
		}
		return "missing account"

	default:
		return "unknown adapter type"
	}
}
