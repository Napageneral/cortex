package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	stdsync "sync"
	"time"

	"github.com/Napageneral/comms/internal/bus"
	"github.com/Napageneral/comms/internal/config"
	"github.com/Napageneral/comms/internal/db"
	"github.com/Napageneral/comms/internal/identify"
	"github.com/Napageneral/comms/internal/importer"
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
				OK        bool   `json:"ok"`
				Message   string `json:"message,omitempty"`
				ConfigDir string `json:"config_dir,omitempty"`
				DataDir   string `json:"data_dir,omitempty"`
				DBPath    string `json:"db_path,omitempty"`
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

			account = strings.TrimSpace(strings.ToLower(account))
			adapterName := fmt.Sprintf("gmail-%s", account)
			workers, _ := cmd.Flags().GetInt("workers")
			qps, _ := cmd.Flags().GetFloat64("qps")

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

			opts := map[string]interface{}{
				"account": account,
			}
			if workers > 0 {
				opts["workers"] = workers
			}
			if qps > 0 {
				opts["qps"] = qps
			}

			cfg.Adapters[adapterName] = config.AdapterConfig{
				Type:    "gogcli",
				Enabled: true,
				Options: opts,
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
				fmt.Printf("  Adapter: %s\n", adapterName)
				fmt.Printf("  Account: %s\n", account)
				fmt.Println("\nNote: Ensure gogcli is installed and authenticated:")
				fmt.Println("  brew install steipete/tap/gogcli")
				fmt.Printf("  gog auth add %s\n", account)
				fmt.Printf("\nRun 'comms sync --adapter %s' to sync Gmail events\n", adapterName)
			}
		},
	}
	connectGmailCmd.Flags().String("account", "", "Gmail account email address")
	connectGmailCmd.Flags().Int("workers", 0, "Parallel thread fetch workers (default: adapter default)")
	connectGmailCmd.Flags().Float64("qps", 0, "Approx API requests/sec for thread fetch (default: adapter default)")

	connectCmd.AddCommand(connectImessageCmd)
	connectCmd.AddCommand(connectGmailCmd)

	// connect calendar
	connectCalendarCmd := &cobra.Command{
		Use:   "calendar",
		Short: "Configure Google Calendar adapter (via gogcli)",
		Run: func(cmd *cobra.Command, args []string) {
			type Result struct {
				OK      bool   `json:"ok"`
				Message string `json:"message,omitempty"`
			}

			account, _ := cmd.Flags().GetString("account")
			if account == "" {
				result := Result{OK: false, Message: "The --account flag is required (e.g., --account user@gmail.com)"}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}
			account = strings.TrimSpace(strings.ToLower(account))
			adapterName := fmt.Sprintf("calendar-%s", account)

			cfg, err := config.Load()
			if err != nil {
				result := Result{OK: false, Message: fmt.Sprintf("Failed to load config: %v", err)}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			cfg.Adapters[adapterName] = config.AdapterConfig{
				Type:    "gogcli_calendar",
				Enabled: true,
				Options: map[string]interface{}{
					"account": account,
				},
			}
			if err := cfg.Save(); err != nil {
				result := Result{OK: false, Message: fmt.Sprintf("Failed to save config: %v", err)}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			result := Result{OK: true, Message: "Calendar adapter configured successfully"}
			if jsonOutput {
				printJSON(result)
			} else {
				fmt.Println("✓ Calendar adapter configured")
				fmt.Printf("  Adapter: %s\n", adapterName)
				fmt.Printf("  Account: %s\n", account)
				fmt.Println("\nNote: Ensure gogcli is installed and authenticated:")
				fmt.Println("  brew install steipete/tap/gogcli")
				fmt.Printf("  gog auth add %s\n", account)
				fmt.Printf("\nRun 'comms sync --adapter %s' to sync calendar events\n", adapterName)
			}
		},
	}
	connectCalendarCmd.Flags().String("account", "", "Google account email address")
	connectCmd.AddCommand(connectCalendarCmd)

	// connect contacts
	connectContactsCmd := &cobra.Command{
		Use:   "contacts",
		Short: "Configure Google Contacts adapter (via gogcli)",
		Run: func(cmd *cobra.Command, args []string) {
			type Result struct {
				OK      bool   `json:"ok"`
				Message string `json:"message,omitempty"`
			}

			account, _ := cmd.Flags().GetString("account")
			if account == "" {
				result := Result{OK: false, Message: "The --account flag is required (e.g., --account user@gmail.com)"}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}
			account = strings.TrimSpace(strings.ToLower(account))
			adapterName := fmt.Sprintf("contacts-%s", account)

			cfg, err := config.Load()
			if err != nil {
				result := Result{OK: false, Message: fmt.Sprintf("Failed to load config: %v", err)}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			cfg.Adapters[adapterName] = config.AdapterConfig{
				Type:    "gogcli_contacts",
				Enabled: true,
				Options: map[string]interface{}{
					"account": account,
				},
			}
			if err := cfg.Save(); err != nil {
				result := Result{OK: false, Message: fmt.Sprintf("Failed to save config: %v", err)}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			result := Result{OK: true, Message: "Contacts adapter configured successfully"}
			if jsonOutput {
				printJSON(result)
			} else {
				fmt.Println("✓ Contacts adapter configured")
				fmt.Printf("  Adapter: %s\n", adapterName)
				fmt.Printf("  Account: %s\n", account)
				fmt.Println("\nNote: Ensure People API is enabled for your gogcli OAuth project:")
				fmt.Println("  People API: https://console.cloud.google.com/apis/library/people.googleapis.com")
				fmt.Println("\nRun 'comms sync --adapter " + adapterName + " --full' to ingest contacts and unify identities")
			}
		},
	}
	connectContactsCmd.Flags().String("account", "", "Google account email address")
	connectCmd.AddCommand(connectContactsCmd)

	// connect google (gmail + calendar + contacts)
	connectGoogleCmd := &cobra.Command{
		Use:   "google",
		Short: "Configure Gmail+Calendar+Contacts for an account and start backfills",
		Run: func(cmd *cobra.Command, args []string) {
			account, _ := cmd.Flags().GetString("account")
			startBackfill, _ := cmd.Flags().GetBool("start")
			if account == "" {
				fmt.Fprintf(os.Stderr, "Error: --account is required\n")
				os.Exit(1)
			}
			account = strings.TrimSpace(strings.ToLower(account))

			// Configure adapters by reusing connect subcommands logic (direct config edits).
			cfg, err := config.Load()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to load config: %v\n", err)
				os.Exit(1)
			}

			gmailName := fmt.Sprintf("gmail-%s", account)
			calName := fmt.Sprintf("calendar-%s", account)
			contactsName := fmt.Sprintf("contacts-%s", account)

			if cfg.Adapters == nil {
				cfg.Adapters = map[string]config.AdapterConfig{}
			}
			cfg.Adapters[gmailName] = config.AdapterConfig{
				Type:    "gogcli",
				Enabled: true,
				Options: map[string]interface{}{"account": account},
			}
			cfg.Adapters[calName] = config.AdapterConfig{
				Type:    "gogcli_calendar",
				Enabled: true,
				Options: map[string]interface{}{"account": account},
			}
			cfg.Adapters[contactsName] = config.AdapterConfig{
				Type:    "gogcli_contacts",
				Enabled: true,
				Options: map[string]interface{}{"account": account},
			}

			if err := cfg.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to save config: %v\n", err)
				os.Exit(1)
			}

			fmt.Println("✓ Google adapters configured")
			fmt.Printf("  Gmail: %s\n", gmailName)
			fmt.Printf("  Calendar: %s\n", calName)
			fmt.Printf("  Contacts: %s\n", contactsName)

			fmt.Println("\nEnable APIs (open these once, enable, done):")
			fmt.Println("  People API: https://console.cloud.google.com/apis/library/people.googleapis.com")
			fmt.Println("  Gmail API: https://console.cloud.google.com/apis/library/gmail.googleapis.com")
			fmt.Println("  Calendar API: https://console.cloud.google.com/apis/library/calendar-json.googleapis.com")
			fmt.Println("  Pub/Sub API (for realtime): https://console.cloud.google.com/apis/library/pubsub.googleapis.com")

			if startBackfill {
				// Start background backfills immediately.
				fmt.Println("\nStarting background backfills...")
				_ = exec.Command(os.Args[0], "sync", "--adapter", gmailName, "--full", "--background").Run()
				_ = exec.Command(os.Args[0], "sync", "--adapter", calName, "--full", "--background").Run()
				_ = exec.Command(os.Args[0], "sync", "--adapter", contactsName, "--full", "--background").Run()
				fmt.Println("Run: comms sync status")
			} else {
				fmt.Println("\nBackfills not started (use --start to kick off automatically).")
			}

			fmt.Println("\nRealtime (Gmail):")
			fmt.Println("  1) Run: comms watch gmail")
			fmt.Println("  2) Configure Pub/Sub topic + start watch:")
			fmt.Printf("     gog gmail watch start --account %s --topic projects/.../topics/... --hook-url http://127.0.0.1:8799/hook/gmail\n", account)
		},
	}
	connectGoogleCmd.Flags().String("account", "", "Google account email address")
	connectGoogleCmd.Flags().Bool("start", true, "Start background backfills immediately")
	connectCmd.AddCommand(connectGoogleCmd)

	// connect cursor (via aix)
	connectCursorCmd := &cobra.Command{
		Use:   "cursor",
		Short: "Configure Cursor AI sessions adapter (via aix)",
		Run: func(cmd *cobra.Command, args []string) {
			type Result struct {
				OK      bool   `json:"ok"`
				Message string `json:"message,omitempty"`
			}

			home, err := os.UserHomeDir()
			if err != nil {
				result := Result{OK: false, Message: fmt.Sprintf("Failed to get home directory: %v", err)}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			aixDBPath := filepath.Join(home, "Library", "Application Support", "aix", "aix.db")
			if _, err := os.Stat(aixDBPath); os.IsNotExist(err) {
				result := Result{
					OK:      false,
					Message: fmt.Sprintf("aix database not found at %s. Run aix first: aix init && aix sync --source cursor (or --all)", aixDBPath),
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n\n", result.Message)
					fmt.Fprintf(os.Stderr, "To fix this:\n")
					fmt.Fprintf(os.Stderr, "  1. Build/install aix\n")
					fmt.Fprintf(os.Stderr, "  2. Run: aix init\n")
					fmt.Fprintf(os.Stderr, "  3. Run: aix sync --source cursor (or: aix sync --all)\n")
				}
				os.Exit(1)
			}

			cfg, err := config.Load()
			if err != nil {
				result := Result{OK: false, Message: fmt.Sprintf("Failed to load config: %v", err)}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			cfg.Adapters["cursor"] = config.AdapterConfig{
				Type:    "aix",
				Enabled: true,
				Options: map[string]interface{}{
					"source": "cursor",
				},
			}

			if err := cfg.Save(); err != nil {
				result := Result{OK: false, Message: fmt.Sprintf("Failed to save config: %v", err)}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			result := Result{OK: true, Message: "Cursor adapter configured successfully"}
			if jsonOutput {
				printJSON(result)
			} else {
				fmt.Println("✓ Cursor adapter configured")
				fmt.Printf("  aix database: %s\n", aixDBPath)
				fmt.Println("\nRun 'comms sync --adapter cursor' to sync Cursor AI sessions")
			}
		},
	}

	connectCmd.AddCommand(connectCursorCmd)

	// connect x (via bird)
	connectXCmd := &cobra.Command{
		Use:   "x",
		Short: "Configure X/Twitter adapter (via bird CLI)",
		Run: func(cmd *cobra.Command, args []string) {
			type Result struct {
				OK      bool   `json:"ok"`
				Message string `json:"message,omitempty"`
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

			cfg.Adapters["x"] = config.AdapterConfig{
				Type:    "bird",
				Enabled: true,
				Options: map[string]interface{}{},
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
				Message: "X/Twitter adapter configured successfully",
			}

			if jsonOutput {
				printJSON(result)
			} else {
				fmt.Println("✓ X/Twitter adapter configured")
				fmt.Println("\nNote: Ensure bird is installed and authenticated:")
				fmt.Println("  brew install steipete/tap/bird")
				fmt.Println("  bird check")
				fmt.Println("\nRun 'comms sync' to sync X events (bookmarks, likes, mentions)")
			}
		},
	}
	connectCmd.AddCommand(connectXCmd)
	rootCmd.AddCommand(connectCmd)

	// sync command
	syncCmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync communications from adapters",
		Long:  "Synchronize communications from all enabled adapters or a specific adapter",
		Run: func(cmd *cobra.Command, args []string) {
			type Result struct {
				OK       bool                 `json:"ok"`
				Message  string               `json:"message,omitempty"`
				Adapters []sync.AdapterResult `json:"adapters,omitempty"`
				Mode     string               `json:"mode,omitempty"`
			}

			adapterName, _ := cmd.Flags().GetString("adapter")
			full, _ := cmd.Flags().GetBool("full")
			background, _ := cmd.Flags().GetBool("background")

			// Background mode: re-exec without --background and return immediately.
			if background {
				dataDir, err := config.GetDataDir()
				if err != nil {
					result := Result{OK: false, Message: fmt.Sprintf("Failed to get data dir: %v", err)}
					if jsonOutput {
						printJSON(result)
					} else {
						fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
					}
					os.Exit(1)
				}
				logPath := filepath.Join(dataDir, "comms-sync.log")

				argv := make([]string, 0, len(os.Args))
				for _, a := range os.Args {
					// Drop --background variants.
					if a == "--background" || a == "--background=true" {
						continue
					}
					argv = append(argv, a)
				}
				// Ensure we have at least the binary name.
				if len(argv) == 0 {
					argv = []string{os.Args[0]}
				}

				f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
				if err != nil {
					result := Result{OK: false, Message: fmt.Sprintf("Failed to open log file: %v", err)}
					if jsonOutput {
						printJSON(result)
					} else {
						fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
					}
					os.Exit(1)
				}
				defer f.Close()

				bg := exec.Command(argv[0], argv[1:]...)
				bg.Stdout = f
				bg.Stderr = f
				if err := bg.Start(); err != nil {
					result := Result{OK: false, Message: fmt.Sprintf("Failed to start background sync: %v", err)}
					if jsonOutput {
						printJSON(result)
					} else {
						fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
					}
					os.Exit(1)
				}

				result := Result{
					OK:      true,
					Message: fmt.Sprintf("Background sync started (pid %d). Logs: %s", bg.Process.Pid, logPath),
					Mode:    "background",
				}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Printf("✓ %s\n", result.Message)
					fmt.Println("Run: comms sync status")
				}
				return
			}

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
				Mode:     "foreground",
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
						fmt.Printf("  Threads created: %d\n", adapterResult.ThreadsCreated)
						fmt.Printf("  Threads updated: %d\n", adapterResult.ThreadsUpdated)
						fmt.Printf("  Attachments created: %d\n", adapterResult.AttachmentsCreated)
						fmt.Printf("  Attachments updated: %d\n", adapterResult.AttachmentsUpdated)
						fmt.Printf("  Reactions created: %d\n", adapterResult.ReactionsCreated)
						fmt.Printf("  Reactions updated: %d\n", adapterResult.ReactionsUpdated)
						fmt.Printf("  Duration: %s\n", adapterResult.Duration)
						if len(adapterResult.Perf) > 0 {
							// Print a few high-signal perf keys if present.
							keys := []string{"watermark_read", "incremental_sync_query", "backfill_windows", "backfill_total", "backfill_last", "total", "hint_takeout"}
							for _, k := range keys {
								if v, ok := adapterResult.Perf[k]; ok && v != "" {
									fmt.Printf("  %s: %s\n", k, v)
								}
							}
						}
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
	syncCmd.Flags().Bool("background", false, "Run sync in background (writes logs to comms-sync.log)")

	// sync status subcommand
	syncStatusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show sync job progress/status",
		Run: func(cmd *cobra.Command, args []string) {
			type Result struct {
				OK      bool             `json:"ok"`
				Jobs    []sync.JobStatus `json:"jobs,omitempty"`
				Message string           `json:"message,omitempty"`
			}

			database, err := db.Open()
			if err != nil {
				result := Result{OK: false, Message: fmt.Sprintf("Failed to open database: %v", err)}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}
			defer database.Close()

			jobs, err := sync.ListJobs(database)
			if err != nil {
				result := Result{OK: false, Message: fmt.Sprintf("Failed to list jobs: %v", err)}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			if len(jobs) == 0 {
				result := Result{OK: true, Message: "No job status found yet. Run: comms sync"}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Println(result.Message)
				}
				return
			}

			result := Result{OK: true, Jobs: jobs}
			if jsonOutput {
				printJSON(result)
				return
			}

			fmt.Println("Sync job status:")
			for _, j := range jobs {
				updated := time.Unix(j.UpdatedAt, 0).Local().Format(time.RFC3339)
				fmt.Printf("\n• %s\n", j.Adapter)
				fmt.Printf("  Status: %s\n", j.Status)
				fmt.Printf("  Phase: %s\n", j.Phase)
				if j.Cursor != nil && *j.Cursor != "" {
					fmt.Printf("  Cursor: %s\n", *j.Cursor)
				}
				fmt.Printf("  Updated: %s\n", updated)
				if j.LastError != nil && *j.LastError != "" {
					fmt.Printf("  Error: %s\n", *j.LastError)
				}
				// Print a couple of common progress fields if present.
				if j.Progress != nil {
					if v, ok := j.Progress["eta_seconds"]; ok {
						fmt.Printf("  ETA: %v seconds\n", v)
					}
					if v, ok := j.Progress["hint_takeout"]; ok {
						fmt.Printf("  Hint: %v\n", v)
					}
					if bf, ok := j.Progress["backfill"].(map[string]interface{}); ok {
						if wd, ok := bf["windows_done"]; ok {
							if wt, ok := bf["windows_total"]; ok {
								fmt.Printf("  Backfill: %v/%v windows\n", wd, wt)
							}
						}
						if cw, ok := bf["current_window"]; ok {
							fmt.Printf("  Window: %v\n", cw)
						}
					}
					if th, ok := j.Progress["threads"].(map[string]interface{}); ok {
						if td, ok := th["done"]; ok {
							if tt, ok := th["total"]; ok {
								fmt.Printf("  Threads: %v/%v\n", td, tt)
							}
						}
					}
				}
			}
		},
	}
	syncCmd.AddCommand(syncStatusCmd)
	rootCmd.AddCommand(syncCmd)

	// import command
	importCmd := &cobra.Command{
		Use:   "import",
		Short: "Import external data into comms",
	}

	// import mbox (Google Takeout)
	importMBoxCmd := &cobra.Command{
		Use:   "mbox",
		Short: "Import Gmail MBOX (Google Takeout)",
		Run: func(cmd *cobra.Command, args []string) {
			type Result struct {
				OK      bool   `json:"ok"`
				Message string `json:"message,omitempty"`

				MessagesSeen      int    `json:"messages_seen,omitempty"`
				EventsCreated     int    `json:"events_created,omitempty"`
				EventsUpdated     int    `json:"events_updated,omitempty"`
				PersonsCreated    int    `json:"persons_created,omitempty"`
				MessagesTruncated int    `json:"messages_truncated,omitempty"`
				Duration          string `json:"duration,omitempty"`
			}

			adapterName, _ := cmd.Flags().GetString("adapter")
			account, _ := cmd.Flags().GetString("account")
			path, _ := cmd.Flags().GetString("file")
			limit, _ := cmd.Flags().GetInt("limit")
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			if adapterName == "" {
				result := Result{OK: false, Message: "The --adapter flag is required (e.g., gmail-tyler@intent-systems.com)"}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}
			if account == "" {
				result := Result{OK: false, Message: "The --account flag is required (Gmail account email address)"}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}
			if path == "" {
				result := Result{OK: false, Message: "The --file flag is required (path to .mbox)"}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			database, err := db.Open()
			if err != nil {
				result := Result{OK: false, Message: fmt.Sprintf("Failed to open database: %v", err)}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}
			defer database.Close()

			ctx := context.Background()
			res, err := importer.ImportMBox(ctx, database, importer.MBoxImportOptions{
				AdapterName:     adapterName,
				AccountEmail:    account,
				Path:            path,
				Source:          "takeout",
				LimitMessages:   limit,
				DryRun:          dryRun,
				MaxMessageBytes: 50 * 1024 * 1024,
				CommitEvery:     500,
			})
			if err != nil && err != io.EOF {
				result := Result{OK: false, Message: fmt.Sprintf("Import failed: %v", err)}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			result := Result{
				OK:                true,
				Message:           "MBOX import completed",
				MessagesSeen:      res.MessagesSeen,
				EventsCreated:     res.EventsCreated,
				EventsUpdated:     res.EventsUpdated,
				PersonsCreated:    res.PersonsCreated,
				MessagesTruncated: res.MessagesTruncated,
				Duration:          res.Duration.String(),
			}
			if jsonOutput {
				printJSON(result)
			} else {
				fmt.Println("✓ MBOX import completed")
				fmt.Printf("  Messages seen: %d\n", res.MessagesSeen)
				fmt.Printf("  Events created: %d\n", res.EventsCreated)
				fmt.Printf("  Events updated: %d\n", res.EventsUpdated)
				fmt.Printf("  Persons created: %d\n", res.PersonsCreated)
				if res.MessagesTruncated > 0 {
					fmt.Printf("  Messages truncated: %d\n", res.MessagesTruncated)
				}
				fmt.Printf("  Duration: %s\n", res.Duration)
				fmt.Println("\nNext: run an incremental API sync to establish history baseline:")
				fmt.Printf("  comms sync --adapter %s\n", adapterName)
			}
		},
	}
	importMBoxCmd.Flags().String("adapter", "", "Adapter instance name (e.g., gmail-tyler@intent-systems.com)")
	importMBoxCmd.Flags().String("account", "", "Gmail account email address (used for sent/received inference)")
	importMBoxCmd.Flags().String("file", "", "Path to MBOX file (Google Takeout)")
	importMBoxCmd.Flags().Int("limit", 0, "Only import first N messages (debug)")
	importMBoxCmd.Flags().Bool("dry-run", false, "Parse and count but do not write to database")

	importCmd.AddCommand(importMBoxCmd)
	rootCmd.AddCommand(importCmd)

	// watch command
	watchCmd := &cobra.Command{
		Use:   "watch",
		Short: "Run a local webhook receiver to trigger sync",
	}

	// watch gmail: receive forwarded Pub/Sub events from `gog gmail watch serve`
	watchGmailCmd := &cobra.Command{
		Use:   "gmail",
		Short: "Receive gog Gmail watch webhooks and sync",
		Run: func(cmd *cobra.Command, args []string) {
			bind, _ := cmd.Flags().GetString("bind")
			port, _ := cmd.Flags().GetInt("port")
			path, _ := cmd.Flags().GetString("path")
			token, _ := cmd.Flags().GetString("token")
			adapterOnly, _ := cmd.Flags().GetString("adapter")
			debounceSec, _ := cmd.Flags().GetInt("debounce-seconds")

			cfg, err := config.Load()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to load config: %v\n", err)
				os.Exit(1)
			}

			database, err := db.Open()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to open database: %v\n", err)
				os.Exit(1)
			}
			defer database.Close()

			type adapterRunState struct {
				running bool
				lastAt  time.Time
			}
			var mu stdsync.Mutex
			stateByAdapter := map[string]*adapterRunState{}

			selectAdapters := func() []string {
				if adapterOnly != "" {
					return []string{adapterOnly}
				}
				var out []string
				for name, a := range cfg.Adapters {
					if !a.Enabled {
						continue
					}
					if a.Type == "gogcli" {
						out = append(out, name)
					}
				}
				return out
			}

			runAdapter := func(adapterName string) {
				mu.Lock()
				st, ok := stateByAdapter[adapterName]
				if !ok {
					st = &adapterRunState{}
					stateByAdapter[adapterName] = st
				}
				if st.running {
					mu.Unlock()
					return
				}
				if debounceSec > 0 && !st.lastAt.IsZero() && time.Since(st.lastAt) < time.Duration(debounceSec)*time.Second {
					mu.Unlock()
					return
				}
				st.running = true
				st.lastAt = time.Now()
				mu.Unlock()

				go func() {
					defer func() {
						mu.Lock()
						st.running = false
						mu.Unlock()
					}()

					res := sync.SyncOne(context.Background(), database, cfg, adapterName, false)
					if !res.OK {
						fmt.Fprintf(os.Stderr, "watch sync error (%s): %s\n", adapterName, res.Message)
					} else {
						fmt.Printf("watch sync OK (%s)\n", adapterName)
					}
				}()
			}

			mux := http.NewServeMux()
			mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost && r.Method != http.MethodPut {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				if token != "" {
					ok := false
					// Accept either Authorization: Bearer <token> or ?token=<token>.
					auth := r.Header.Get("Authorization")
					if strings.HasPrefix(strings.ToLower(auth), "bearer ") && strings.TrimSpace(auth[7:]) == token {
						ok = true
					}
					if r.URL.Query().Get("token") == token {
						ok = true
					}
					if !ok {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
				}

				// Drain body (ignore contents; Gmail adapter will use History API baseline).
				_, _ = io.ReadAll(io.LimitReader(r.Body, 256*1024))
				_ = r.Body.Close()

				for _, a := range selectAdapters() {
					runAdapter(a)
				}

				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok\n"))
			})

			addr := fmt.Sprintf("%s:%d", bind, port)
			fmt.Printf("Listening on http://%s%s\n", addr, path)
			fmt.Println("To connect gogcli watch forwarding to this receiver:")
			fmt.Printf("  gog gmail watch serve --bind %s --port 8788 --path /gmail-pubsub --hook-url http://%s%s", bind, addr, path)
			if token != "" {
				fmt.Printf(" --hook-token %s", token)
			}
			fmt.Println()
			fmt.Println("Then start/renew watch (requires Pub/Sub topic):")
			fmt.Println("  gog gmail watch start --topic projects/.../topics/... --account <acct>")

			srv := &http.Server{
				Addr:    addr,
				Handler: mux,
			}
			if err := srv.ListenAndServe(); err != nil {
				fmt.Fprintf(os.Stderr, "watch server stopped: %v\n", err)
				os.Exit(1)
			}
		},
	}
	watchGmailCmd.Flags().String("bind", "127.0.0.1", "Bind address")
	watchGmailCmd.Flags().Int("port", 8799, "Listen port")
	watchGmailCmd.Flags().String("path", "/hook/gmail", "Webhook path")
	watchGmailCmd.Flags().String("token", "", "Shared token (Authorization Bearer or ?token=)")
	watchGmailCmd.Flags().String("adapter", "", "Only trigger sync for this adapter (default: all gogcli adapters)")
	watchGmailCmd.Flags().Int("debounce-seconds", 10, "Minimum seconds between sync triggers per adapter")

	watchCmd.AddCommand(watchGmailCmd)
	rootCmd.AddCommand(watchCmd)

	// bus command
	busCmd := &cobra.Command{
		Use:   "bus",
		Short: "Inspect the comms event bus",
	}

	busListCmd := &cobra.Command{
		Use:   "list",
		Short: "List bus events",
		Run: func(cmd *cobra.Command, args []string) {
			since, _ := cmd.Flags().GetInt64("since")
			limit, _ := cmd.Flags().GetInt("limit")

			database, err := db.Open()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to open database: %v\n", err)
				os.Exit(1)
			}
			defer database.Close()

			events, err := bus.List(database, since, limit)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to list bus events: %v\n", err)
				os.Exit(1)
			}

			if jsonOutput {
				printJSON(map[string]any{"ok": true, "events": events})
				return
			}

			if len(events) == 0 {
				fmt.Println("No bus events.")
				return
			}
			for _, e := range events {
				t := time.Unix(e.CreatedAt, 0).Local().Format(time.RFC3339)
				adapter := ""
				if e.Adapter != nil {
					adapter = *e.Adapter
				}
				ce := ""
				if e.CommsEvent != nil {
					ce = *e.CommsEvent
				}
				fmt.Printf("%d\t%s\t%s\t%s\t%s\n", e.Seq, t, e.Type, adapter, ce)
			}
		},
	}
	busListCmd.Flags().Int64("since", 0, "Only show events with seq > since")
	busListCmd.Flags().Int("limit", 200, "Max events to return")

	busTailCmd := &cobra.Command{
		Use:   "tail",
		Short: "Tail bus events (optionally follow)",
		Run: func(cmd *cobra.Command, args []string) {
			since, _ := cmd.Flags().GetInt64("since")
			follow, _ := cmd.Flags().GetBool("follow")
			intervalSec, _ := cmd.Flags().GetInt("interval-seconds")
			limit, _ := cmd.Flags().GetInt("limit")

			database, err := db.Open()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to open database: %v\n", err)
				os.Exit(1)
			}
			defer database.Close()

			emit := func(events []bus.Event) {
				for _, e := range events {
					t := time.Unix(e.CreatedAt, 0).Local().Format(time.RFC3339)
					adapter := ""
					if e.Adapter != nil {
						adapter = *e.Adapter
					}
					ce := ""
					if e.CommsEvent != nil {
						ce = *e.CommsEvent
					}
					fmt.Printf("%d\t%s\t%s\t%s\t%s\n", e.Seq, t, e.Type, adapter, ce)
					since = e.Seq
				}
			}

			for {
				events, err := bus.List(database, since, limit)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error: failed to list bus events: %v\n", err)
					os.Exit(1)
				}
				if jsonOutput {
					printJSON(map[string]any{"ok": true, "events": events})
					if !follow {
						return
					}
				} else {
					emit(events)
					if !follow {
						return
					}
				}
				if intervalSec <= 0 {
					intervalSec = 1
				}
				time.Sleep(time.Duration(intervalSec) * time.Second)
			}
		},
	}
	busTailCmd.Flags().Int64("since", 0, "Start tailing from seq > since")
	busTailCmd.Flags().Bool("follow", true, "Keep polling for new events")
	busTailCmd.Flags().Int("interval-seconds", 1, "Polling interval in seconds when following")
	busTailCmd.Flags().Int("limit", 200, "Max events per poll")

	busCmd.AddCommand(busListCmd)
	busCmd.AddCommand(busTailCmd)
	rootCmd.AddCommand(busCmd)

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
				ID          string         `json:"id"`
				Name        string         `json:"name"`
				DisplayName string         `json:"display_name,omitempty"`
				IsMe        bool           `json:"is_me"`
				Identities  []IdentityInfo `json:"identities"`
				EventCount  int            `json:"event_count"`
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

	// identify suggestions command - generate merge suggestions
	identifySuggestCmd := &cobra.Command{
		Use:   "suggest",
		Short: "Generate merge suggestions for similar persons",
		Long:  "Analyze the identity graph and generate merge suggestions based on name similarity, shared domains, etc.",
		Run: func(cmd *cobra.Command, args []string) {
			type Result struct {
				OK                 bool   `json:"ok"`
				Message            string `json:"message,omitempty"`
				SuggestionsCreated int    `json:"suggestions_created"`
			}

			minEvents, _ := cmd.Flags().GetInt("min-events")
			minConfidence, _ := cmd.Flags().GetFloat64("min-confidence")
			maxSuggestions, _ := cmd.Flags().GetInt("max")

			database, err := db.Open()
			if err != nil {
				result := Result{OK: false, Message: fmt.Sprintf("Failed to open database: %v", err)}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}
			defer database.Close()

			opts := identify.SuggestionOptions{
				MinEventCount:  minEvents,
				MinConfidence:  minConfidence,
				MaxSuggestions: maxSuggestions,
				NameSimilarity: true,
				SharedDomain:   true,
			}

			count, err := identify.GenerateSuggestions(database, opts)
			if err != nil {
				result := Result{OK: false, Message: fmt.Sprintf("Failed to generate suggestions: %v", err)}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			result := Result{OK: true, SuggestionsCreated: count}
			if jsonOutput {
				printJSON(result)
			} else {
				fmt.Printf("Generated %d merge suggestions\n", count)
				fmt.Println("Use 'comms identify suggestions' to review them")
			}
		},
	}
	identifySuggestCmd.Flags().Int("min-events", 5, "Minimum event count for persons to consider")
	identifySuggestCmd.Flags().Float64("min-confidence", 0.5, "Minimum confidence threshold")
	identifySuggestCmd.Flags().Int("max", 100, "Maximum suggestions to generate")

	// identify suggestions list command
	identifySuggestionsCmd := &cobra.Command{
		Use:   "suggestions",
		Short: "List pending merge suggestions",
		Long:  "Show merge suggestions that need user review",
		Run: func(cmd *cobra.Command, args []string) {
			type SuggestionInfo struct {
				ID           string  `json:"id"`
				Person1      string  `json:"person1"`
				Person2      string  `json:"person2"`
				EvidenceType string  `json:"evidence_type"`
				Evidence     any     `json:"evidence,omitempty"`
				Confidence   float64 `json:"confidence"`
				EventCount   int     `json:"combined_event_count"`
			}

			type Result struct {
				OK          bool             `json:"ok"`
				Message     string           `json:"message,omitempty"`
				Count       int              `json:"count"`
				Suggestions []SuggestionInfo `json:"suggestions,omitempty"`
			}

			status, _ := cmd.Flags().GetString("status")
			limit, _ := cmd.Flags().GetInt("limit")

			database, err := db.Open()
			if err != nil {
				result := Result{OK: false, Message: fmt.Sprintf("Failed to open database: %v", err)}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}
			defer database.Close()

			suggestions, err := identify.ListSuggestions(database, status, limit)
			if err != nil {
				result := Result{OK: false, Message: fmt.Sprintf("Failed to list suggestions: %v", err)}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			var infos []SuggestionInfo
			for _, s := range suggestions {
				infos = append(infos, SuggestionInfo{
					ID:           s.ID,
					Person1:      s.Person1Name,
					Person2:      s.Person2Name,
					EvidenceType: s.EvidenceType,
					Evidence:     s.Evidence,
					Confidence:   s.Confidence,
					EventCount:   s.Person1EventCount + s.Person2EventCount,
				})
			}

			result := Result{OK: true, Count: len(infos), Suggestions: infos}
			if jsonOutput {
				printJSON(result)
			} else {
				if len(infos) == 0 {
					fmt.Println("No pending suggestions. Run 'comms identify suggest' to generate some.")
				} else {
					fmt.Printf("Found %d pending suggestions:\n\n", len(infos))
					for _, s := range infos {
						fmt.Printf("  [%s] %s ↔ %s\n", s.ID[:8], s.Person1, s.Person2)
						fmt.Printf("    Evidence: %s (confidence: %.1f%%)\n", s.EvidenceType, s.Confidence*100)
						fmt.Printf("    Combined events: %d\n\n", s.EventCount)
					}
					fmt.Println("Use 'comms identify accept <id>' or 'comms identify reject <id>'")
				}
			}
		},
	}
	identifySuggestionsCmd.Flags().String("status", "pending", "Filter by status (pending, accepted, rejected, expired)")
	identifySuggestionsCmd.Flags().Int("limit", 50, "Maximum suggestions to show")

	// identify accept command
	identifyAcceptCmd := &cobra.Command{
		Use:   "accept <suggestion-id>",
		Short: "Accept a merge suggestion (merges the two persons)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			type Result struct {
				OK      bool   `json:"ok"`
				Message string `json:"message,omitempty"`
			}

			suggestionID := args[0]

			database, err := db.Open()
			if err != nil {
				result := Result{OK: false, Message: fmt.Sprintf("Failed to open database: %v", err)}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}
			defer database.Close()

			// Support short IDs
			if len(suggestionID) < 36 {
				// Try to find full ID
				var fullID string
				err := database.QueryRow(`SELECT id FROM merge_suggestions WHERE id LIKE ? AND status = 'pending' LIMIT 1`, suggestionID+"%").Scan(&fullID)
				if err == nil {
					suggestionID = fullID
				}
			}

			err = identify.AcceptSuggestion(database, suggestionID)
			if err != nil {
				result := Result{OK: false, Message: fmt.Sprintf("Failed to accept suggestion: %v", err)}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			result := Result{OK: true, Message: "Suggestion accepted - persons merged"}
			if jsonOutput {
				printJSON(result)
			} else {
				fmt.Println("✓ Suggestion accepted - persons merged")
			}
		},
	}

	// identify reject command
	identifyRejectCmd := &cobra.Command{
		Use:   "reject <suggestion-id>",
		Short: "Reject a merge suggestion",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			type Result struct {
				OK      bool   `json:"ok"`
				Message string `json:"message,omitempty"`
			}

			suggestionID := args[0]

			database, err := db.Open()
			if err != nil {
				result := Result{OK: false, Message: fmt.Sprintf("Failed to open database: %v", err)}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}
			defer database.Close()

			// Support short IDs
			if len(suggestionID) < 36 {
				var fullID string
				err := database.QueryRow(`SELECT id FROM merge_suggestions WHERE id LIKE ? AND status = 'pending' LIMIT 1`, suggestionID+"%").Scan(&fullID)
				if err == nil {
					suggestionID = fullID
				}
			}

			err = identify.RejectSuggestion(database, suggestionID)
			if err != nil {
				result := Result{OK: false, Message: fmt.Sprintf("Failed to reject suggestion: %v", err)}
				if jsonOutput {
					printJSON(result)
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
				}
				os.Exit(1)
			}

			result := Result{OK: true, Message: "Suggestion rejected"}
			if jsonOutput {
				printJSON(result)
			} else {
				fmt.Println("✓ Suggestion rejected")
			}
		},
	}

	identifyCmd.AddCommand(identifySuggestCmd)
	identifyCmd.AddCommand(identifySuggestionsCmd)
	identifyCmd.AddCommand(identifyAcceptCmd)
	identifyCmd.AddCommand(identifyRejectCmd)
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
				ID           string         `json:"id"`
				Name         string         `json:"name"`
				DisplayName  string         `json:"display_name,omitempty"`
				IsMe         bool           `json:"is_me"`
				Relationship string         `json:"relationship,omitempty"`
				Identities   []IdentityInfo `json:"identities"`
				EventCount   int            `json:"event_count"`
				LastEventAt  string         `json:"last_event_at,omitempty"`
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
				OK           bool   `json:"ok"`
				Message      string `json:"message,omitempty"`
				EventsTagged int    `json:"events_tagged,omitempty"`
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
				OK      bool                     `json:"ok"`
				Message string                   `json:"message,omitempty"`
				Rows    []map[string]interface{} `json:"rows,omitempty"`
				Count   int                      `json:"count,omitempty"`
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

	case "aix":
		home, err := os.UserHomeDir()
		if err != nil {
			return "error"
		}
		aixDBPath := filepath.Join(home, "Library", "Application Support", "aix", "aix.db")
		if _, err := os.Stat(aixDBPath); os.IsNotExist(err) {
			return "missing aix database (run: aix sync --all)"
		}
		if source, ok := adapter.Options["source"].(string); ok && source != "" {
			return "ready"
		}
		return "missing source"

	case "bird":
		// Check if bird CLI is available
		if _, err := exec.LookPath("bird"); err != nil {
			return "bird not installed (brew install steipete/tap/bird)"
		}
		return "ready"

	default:
		return "unknown adapter type"
	}
}
