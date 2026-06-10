package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// LNDChannelBackup is a stored static channel backup row.
type LNDChannelBackup struct {
	ID              int64
	AccountID       int64
	BackupSHA256    string
	BackupSizeBytes int64
	ChannelCount    int
	MultiChanBackup []byte
	CreatedAt       time.Time
}

// LNDWalletCredentials returns the wallet password and latest aezeed for the LND account.
func (r *TransactionRepository) LNDWalletCredentials(ctx context.Context) (password string, mnemonic string, err error) {
	account, err := r.GetAccountByProvider(ctx, LNDAccountProvider)
	if err != nil {
		return "", "", fmt.Errorf("load LND account: %w", err)
	}
	password = strings.TrimSpace(account.Password)

	var seedPhrase string
	if err := r.db.WithContext(ctx).Raw(`
		SELECT seed_phrase
		FROM seeds
		WHERE account_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, account.ID).Scan(&seedPhrase).Error; err != nil {
		return password, "", fmt.Errorf("load LND seed: %w", err)
	}
	return password, strings.TrimSpace(seedPhrase), nil
}

// EnsureLNDSeed stores the aezeed in seeds when it is not already present.
func (r *TransactionRepository) EnsureLNDSeed(ctx context.Context, accountID int64, mnemonic string) error {
	mnemonic = strings.TrimSpace(mnemonic)
	if mnemonic == "" {
		return nil
	}
	if accountID <= 0 {
		return errors.New("LND account id is required")
	}
	if err := r.db.WithContext(ctx).Exec(`
		INSERT INTO seeds (account_id, seed_phrase, wallet_created_at)
		SELECT ?, ?, NULL
		WHERE NOT EXISTS (
			SELECT 1
			FROM seeds
			WHERE account_id = ?
			  AND seed_phrase = ?
		)
	`, accountID, mnemonic, accountID, mnemonic).Error; err != nil {
		return fmt.Errorf("insert LND seed: %w", err)
	}
	return nil
}

// RecordLNDChannelBackup persists a multi-channel backup blob for the LND account.
func (r *TransactionRepository) RecordLNDChannelBackup(ctx context.Context, accountID int64, channelCount int, multiChanBackup []byte) error {
	if accountID <= 0 {
		return errors.New("LND account id is required")
	}
	if len(multiChanBackup) == 0 {
		return nil
	}
	sum := sha256.Sum256(multiChanBackup)
	backupSHA256 := hex.EncodeToString(sum[:])
	backupSize := int64(len(multiChanBackup))

	if err := r.db.WithContext(ctx).Exec(`
		INSERT INTO lnd_channel_backups (
			account_id,
			backup_sha256,
			backup_size_bytes,
			channel_count,
			multi_chan_backup
		)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT ON CONSTRAINT lnd_channel_backups_account_hash_size_key DO NOTHING
	`, accountID, backupSHA256, backupSize, channelCount, multiChanBackup).Error; err != nil {
		return fmt.Errorf("insert LND channel backup: %w", err)
	}
	return nil
}

// LatestLNDChannelBackup returns the newest stored channel backup for an account.
func (r *TransactionRepository) LatestLNDChannelBackup(ctx context.Context, accountID int64) (*LNDChannelBackup, error) {
	if accountID <= 0 {
		return nil, errors.New("LND account id is required")
	}
	var item LNDChannelBackup
	if err := r.db.WithContext(ctx).Raw(`
		SELECT id,
		       account_id,
		       backup_sha256,
		       backup_size_bytes,
		       channel_count,
		       multi_chan_backup,
		       created_at
		FROM lnd_channel_backups
		WHERE account_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, accountID).Scan(&item).Error; err != nil {
		return nil, fmt.Errorf("load latest LND channel backup: %w", err)
	}
	if item.ID == 0 {
		return nil, nil
	}
	return &item, nil
}
