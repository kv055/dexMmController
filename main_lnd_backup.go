package main

import (
	"context"
	"fmt"
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

type lndBackupClient interface {
	WalletJustInitialized() bool
	RestoreChannelBackups(ctx context.Context, multiChanBackup []byte) (uint32, error)
	ExportAllChannelBackups(ctx context.Context) (*lnd.ChanBackupSnapshot, error)
}

func runLNDChannelBackupCycle(ctx context.Context, repo lndChannelBackupRepository, client lndBackupClient, accountID int64) error {
	if client == nil {
		return nil
	}
	if client.WalletJustInitialized() {
		backup, err := repo.LatestLNDChannelBackup(ctx, accountID)
		if err != nil {
			return err
		}
		if backup != nil && backup.ChannelCount > 0 && len(backup.MultiChanBackup) > 0 {
			restored, err := client.RestoreChannelBackups(ctx, backup.MultiChanBackup)
			if err != nil {
				return fmt.Errorf("restore LND channel backups: %w", err)
			}
			log.Printf("restored %d LND channel backup(s) from database", restored)
		}
	}

	snapshot, err := client.ExportAllChannelBackups(ctx)
	if err != nil {
		return fmt.Errorf("export LND channel backups: %w", err)
	}
	raw, channelCount, err := lnd.MultiBackupBytes(snapshot)
	if err != nil {
		return err
	}
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

func runPeriodicLNDChannelBackup(ctx context.Context, interval time.Duration, repo lndChannelBackupRepository, client lndBackupClient, accountID int64) {
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
			if err := runLNDChannelBackupCycle(ctx, repo, client, accountID); err != nil {
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
