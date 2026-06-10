package lnd

import (
	"encoding/base64"
	"testing"
)

func TestMultiBackupBytes(t *testing.T) {
	raw := []byte("encrypted-scb-payload")
	snapshot := &ChanBackupSnapshot{
		MultiChanBackup: &MultiChanBackup{
			MultiChanBackup: base64.StdEncoding.EncodeToString(raw),
			ChanPoints: []ChannelPoint{
				{FundingTxidStr: "abc", OutputIndex: 1},
			},
		},
	}

	got, count, err := MultiBackupBytes(snapshot)
	if err != nil {
		t.Fatalf("MultiBackupBytes() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("channel count = %d, want 1", count)
	}
	if string(got) != string(raw) {
		t.Fatalf("backup bytes = %q, want %q", got, raw)
	}
}

func TestMultiBackupBytesEmptySnapshot(t *testing.T) {
	got, count, err := MultiBackupBytes(nil)
	if err != nil {
		t.Fatalf("MultiBackupBytes(nil) error = %v", err)
	}
	if got != nil || count != 0 {
		t.Fatalf("got (%q, %d), want (nil, 0)", got, count)
	}
}
