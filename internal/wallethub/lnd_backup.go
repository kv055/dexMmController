package lnd

import (
	"context"
	"encoding/base64"
	"fmt"
)

// ChannelPoint identifies a channel funding output.
type ChannelPoint struct {
	FundingTxidStr string `json:"funding_txid_str"`
	OutputIndex    uint32 `json:"output_index"`
}

// ChannelBackup is a single encrypted static channel backup.
type ChannelBackup struct {
	ChanPoint   ChannelPoint `json:"chan_point"`
	ChanBackup  string       `json:"chan_backup"`
}

// ChannelBackups is a list of single-channel backups.
type ChannelBackups struct {
	ChanBackups []ChannelBackup `json:"chan_backups"`
}

// MultiChanBackup is the multi-channel encrypted backup blob.
type MultiChanBackup struct {
	ChanPoints      []ChannelPoint `json:"chan_points"`
	MultiChanBackup string         `json:"multi_chan_backup"`
}

// ChanBackupSnapshot is the export-all-channel-backups response.
type ChanBackupSnapshot struct {
	SingleChanBackups *ChannelBackups `json:"single_chan_backups"`
	MultiChanBackup   *MultiChanBackup `json:"multi_chan_backup"`
}

type restoreChanBackupRequest struct {
	MultiChanBackup string `json:"multi_chan_backup"`
}

type restoreChanBackupResponse struct {
	NumRestored uint32 `json:"num_restored"`
}

// ExportAllChannelBackups returns static channel backups for all known channels.
func (c *Client) ExportAllChannelBackups(ctx context.Context) (*ChanBackupSnapshot, error) {
	var resp ChanBackupSnapshot
	if err := c.get(ctx, "/v1/channels/backup", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// RestoreChannelBackups restores channel funds from a multi-channel backup blob.
func (c *Client) RestoreChannelBackups(ctx context.Context, multiChanBackup []byte) (uint32, error) {
	if len(multiChanBackup) == 0 {
		return 0, fmt.Errorf("multi-channel backup is empty")
	}
	req := restoreChanBackupRequest{
		MultiChanBackup: base64.StdEncoding.EncodeToString(multiChanBackup),
	}
	var resp restoreChanBackupResponse
	if err := c.post(ctx, "/v1/channels/backup/restore", req, &resp); err != nil {
		return 0, err
	}
	return resp.NumRestored, nil
}

// MultiBackupBytes decodes the multi-channel backup payload from an export snapshot.
func MultiBackupBytes(snapshot *ChanBackupSnapshot) ([]byte, int, error) {
	if snapshot == nil || snapshot.MultiChanBackup == nil {
		return nil, 0, nil
	}
	encoded := snapshot.MultiChanBackup.MultiChanBackup
	if encoded == "" {
		return nil, len(snapshot.MultiChanBackup.ChanPoints), nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, 0, fmt.Errorf("decode multi_chan_backup: %w", err)
	}
	channelCount := len(snapshot.MultiChanBackup.ChanPoints)
	if channelCount == 0 && snapshot.SingleChanBackups != nil {
		channelCount = len(snapshot.SingleChanBackups.ChanBackups)
	}
	return raw, channelCount, nil
}
