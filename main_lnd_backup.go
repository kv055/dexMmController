package main

import (
	"context"
	"log"
	"strings"
	"time"

	"dexMmController/internal/db"
	lnd "dexMmController/internal/wallethub"
)

func mergeLNDInitConfig(fileCfg lnd.InitConfig, mnemonic string) lnd.InitConfig {
	cfg := fileCfg
	mnemonic = strings.TrimSpace(mnemonic)
	if mnemonic != "" {
		cfg.CipherSeedMnemonic = strings.Fields(mnemonic)
	}
	return cfg
}

func importLNDSeedFromFile(ctx context.Context, repo *db.TransactionRepository, accountID int64, fileCfg lnd.InitConfig) error {
	if len(fileCfg.CipherSeedMnemonic) == 0 {
		return nil
	}
	return repo.EnsureLNDSeed(ctx, accountID, strings.Join(fileCfg.CipherSeedMnemonic, " "))
}

type lndChannelBackupRepository interface {
	LatestLNDChannelBackup(ctx context.Context, accountID int64) (*db.LNDChannelBackup, error)
	RecordLNDChannelBackup(ctx context.Context, accountID int64, channelCount int, multiChanBackup []byte) error
}

func loadLNDChannelBackupRestore(ctx context.Context, repo lndChannelBackupRepository, accountID int64) []byte {
	backup, err := repo.LatestLNDChannelBackup(ctx, accountID)
	if err != nil {
		log.Printf("warning: failed to load LND channel backup from database: %v", err)
		return nil
	}
	if backup == nil || len(backup.MultiChanBackup) == 0 {
		return nil
	}
	return backup.MultiChanBackup
}

func persistLNDChannelBackup(ctx context.Context, repo lndChannelBackupRepository, client *lnd.Client, accountID int64) error {
	if client == nil {
		return nil
	}
	raw, channelCount := client.LastExportedChannelBackup()
	if len(raw) == 0 {
		log.Println("LND channel backup export returned no multi-channel backup payload")
		return nil
	}
	if err := repo.RecordLNDChannelBackup(ctx, accountID, channelCount, raw); err != nil {
		return err
	}
	log.Printf("LND channel backup stored (%d channel(s), %d bytes)", channelCount, len(raw))
	return nil
}

func runPeriodicLNDChannelBackup(ctx context.Context, interval time.Duration, repo lndChannelBackupRepository, client *lnd.Client, accountID int64) {
	if client == nil || interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := client.ExportChannelBackup(ctx); err != nil {
				log.Printf("warning: periodic LND channel backup export failed: %v", err)
				continue
			}
			if err := persistLNDChannelBackup(ctx, repo, client, accountID); err != nil {
				log.Printf("warning: periodic LND channel backup failed: %v", err)
			}
		}
	}
}

func parseLNDBackupInterval(raw string, fallback time.Duration) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		return fallback
	}
	return interval
}
