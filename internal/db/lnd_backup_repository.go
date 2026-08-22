package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"commons/credentialcrypto"
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
	password, err = credentialcrypto.Decrypt(strings.TrimSpace(account.Password))
	if err != nil {
		return "", "", fmt.Errorf("decrypt LND wallet password: %w", err)
	}

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
	mnemonic, err = credentialcrypto.Decrypt(strings.TrimSpace(seedPhrase))
	if err != nil {
		return password, "", fmt.Errorf("decrypt LND seed: %w", err)
	}
	return strings.TrimSpace(password), strings.TrimSpace(mnemonic), nil
}

// EnsureLNDWalletPassword stores an encrypted wallet password for the LND
// account when none is configured yet. A guarded UPDATE keeps the write
// idempotent: an existing password is never overwritten, so a concurrently
// generated value cannot clobber the credential the wallet was created with.
func (r *TransactionRepository) EnsureLNDWalletPassword(ctx context.Context, accountID int64, password string) error {
	password = strings.TrimSpace(password)
	if password == "" {
		return errors.New("LND wallet password is required")
	}
	if accountID <= 0 {
		return errors.New("LND account id is required")
	}
	encrypted, err := credentialcrypto.Encrypt(password)
	if err != nil {
		return fmt.Errorf("encrypt LND wallet password: %w", err)
	}
	if err := r.db.WithContext(ctx).Exec(`
		UPDATE accounts
		SET password = ?
		WHERE id = ? AND (password IS NULL OR TRIM(password) = '')
	`, encrypted, accountID).Error; err != nil {
		return fmt.Errorf("store LND wallet password: %w", err)
	}
	return nil
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

	var stored []string
	if err := r.db.WithContext(ctx).Raw(`
		SELECT seed_phrase
		FROM seeds
		WHERE account_id = ?
	`, accountID).Scan(&stored).Error; err != nil {
		return fmt.Errorf("load existing LND seeds: %w", err)
	}
	for _, value := range stored {
		plaintext, err := credentialcrypto.Decrypt(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("decrypt existing LND seed: %w", err)
		}
		if strings.TrimSpace(plaintext) == mnemonic {
			return nil
		}
	}

	encryptedMnemonic, err := credentialcrypto.Encrypt(mnemonic)
	if err != nil {
		return fmt.Errorf("encrypt LND seed: %w", err)
	}
	if err := r.db.WithContext(ctx).Exec(`
		INSERT INTO seeds (account_id, seed_phrase, wallet_created_at)
		VALUES (?, ?, NULL)
	`, accountID, encryptedMnemonic).Error; err != nil {
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
