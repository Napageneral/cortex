package sync

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Napageneral/comms/internal/adapters"
	"github.com/Napageneral/comms/internal/config"
)

// AdapterResult contains the result of syncing a single adapter
type AdapterResult struct {
	AdapterName    string `json:"adapter_name"`
	Success        bool   `json:"success"`
	Error          string `json:"error,omitempty"`
	EventsCreated  int    `json:"events_created"`
	EventsUpdated  int    `json:"events_updated"`
	PersonsCreated int    `json:"persons_created"`
	Duration       string `json:"duration"`
}

// SyncResult contains the results of syncing all adapters
type SyncResult struct {
	OK       bool            `json:"ok"`
	Message  string          `json:"message,omitempty"`
	Adapters []AdapterResult `json:"adapters,omitempty"`
}

// SyncAll runs all enabled adapters
func SyncAll(ctx context.Context, db *sql.DB, cfg *config.Config, full bool) SyncResult {
	result := SyncResult{OK: true}

	if len(cfg.Adapters) == 0 {
		result.Message = "No adapters configured"
		return result
	}

	enabledCount := 0
	for _, adapter := range cfg.Adapters {
		if adapter.Enabled {
			enabledCount++
		}
	}

	if enabledCount == 0 {
		result.Message = "No adapters enabled"
		return result
	}

	// Sync each enabled adapter
	for name, adapterCfg := range cfg.Adapters {
		if !adapterCfg.Enabled {
			continue
		}

		adapterResult := syncAdapter(ctx, db, name, adapterCfg, full)
		result.Adapters = append(result.Adapters, adapterResult)

		if !adapterResult.Success {
			// One adapter failing doesn't stop others, but overall sync is not OK
			result.OK = false
		}
	}

	return result
}

// SyncOne runs a specific adapter by name
func SyncOne(ctx context.Context, db *sql.DB, cfg *config.Config, adapterName string, full bool) SyncResult {
	result := SyncResult{OK: true}

	adapterCfg, exists := cfg.Adapters[adapterName]
	if !exists {
		result.OK = false
		result.Message = fmt.Sprintf("Adapter '%s' not configured", adapterName)
		return result
	}

	if !adapterCfg.Enabled {
		result.OK = false
		result.Message = fmt.Sprintf("Adapter '%s' is disabled", adapterName)
		return result
	}

	adapterResult := syncAdapter(ctx, db, adapterName, adapterCfg, full)
	result.Adapters = []AdapterResult{adapterResult}

	if !adapterResult.Success {
		result.OK = false
	}

	return result
}

// syncAdapter syncs a single adapter and returns its result
func syncAdapter(ctx context.Context, db *sql.DB, name string, cfg config.AdapterConfig, full bool) AdapterResult {
	result := AdapterResult{
		AdapterName: name,
		Success:     false,
	}

	// Create adapter instance based on type
	var adapter adapters.Adapter
	var err error

	switch cfg.Type {
	case "eve":
		adapter, err = adapters.NewEveAdapter()
		if err != nil {
			result.Error = fmt.Sprintf("Failed to create adapter: %v", err)
			return result
		}

	case "gogcli":
		// Gmail adapter via gogcli
		accountVal, ok := cfg.Options["account"]
		if !ok {
			result.Error = "Gmail adapter requires 'account' in config"
			return result
		}
		account, ok := accountVal.(string)
		if !ok || account == "" {
			result.Error = "Gmail adapter 'account' must be a string"
			return result
		}
		adapter, err = adapters.NewGmailAdapter(account)
		if err != nil {
			result.Error = fmt.Sprintf("Failed to create adapter: %v", err)
			return result
		}

	default:
		result.Error = fmt.Sprintf("Unknown adapter type: %s", cfg.Type)
		return result
	}

	// Run sync
	syncResult, err := adapter.Sync(ctx, db, full)
	if err != nil {
		result.Error = fmt.Sprintf("Sync failed: %v", err)
		return result
	}

	// Populate result
	result.Success = true
	result.EventsCreated = syncResult.EventsCreated
	result.EventsUpdated = syncResult.EventsUpdated
	result.PersonsCreated = syncResult.PersonsCreated
	result.Duration = syncResult.Duration.String()

	return result
}
