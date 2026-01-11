package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Napageneral/comms/internal/config"
	"github.com/Napageneral/comms/internal/db"
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

	// TODO: Add more commands as per PRD
	// - me
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
