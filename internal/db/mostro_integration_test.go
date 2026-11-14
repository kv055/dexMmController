package db

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"commons/credentialcrypto"
	"dexTakerController/internal/exchangeconnectors/dex/mostro"
	"github.com/google/uuid"
	"github.com/nbd-wtf/go-nostr"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestMostroPostgresRecovery(t *testing.T) {
	dsn := os.Getenv("MOSTRO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set MOSTRO_TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	root, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlRoot, _ := root.DB()
	defer sqlRoot.Close()
	schema := "mostro_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err = root.Exec(`CREATE SCHEMA ` + schema).Error; err != nil {
		t.Fatal(err)
	}
	defer root.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
	db, err := gorm.Open(postgres.Open(dsn+" search_path="+schema), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB()
	defer pool.Close()
	pool.SetMaxOpenConns(8)
	for _, stmt := range []string{`CREATE TABLE transactions(id BIGSERIAL PRIMARY KEY,status TEXT,deleted_at TIMESTAMPTZ)`, `CREATE TABLE offers(exchange TEXT,offer_id TEXT)`, `INSERT INTO transactions(id,status) VALUES(42,'DexPositionTaken'),(43,'DexPositionTaken')`} {
		if err = db.Exec(stmt).Error; err != nil {
			t.Fatal(err)
		}
	}
	migrations, err := filepath.Glob("../../../dexUnifiedApi/db/migrations/20260913*_mostro_*.sql")
	if err != nil || len(migrations) < 2 {
		t.Fatalf("Mostro migrations not found: %v %v", migrations, err)
	}
	for _, p := range migrations {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err = db.Exec(string(raw)).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := NewTransactionRepository(db)
	ctx := context.Background()
	unlock, err := repo.LockMostro(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := repo.LockMostro(ctx, 42); err == nil {
		again()
		t.Fatal("concurrent worker acquired trade lock")
	}
	unlock()
	unlock, err = repo.LockMostro(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	t.Setenv("APP_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	words := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	cipher, err := credentialcrypto.Encrypt(words)
	if err != nil {
		t.Fatal(err)
	}
	makeSession := func(id uint64) func([]byte, int64) (*mostro.Session, error) {
		return func(encrypted []byte, index int64) (*mostro.Session, error) {
			got, err := credentialcrypto.Decrypt(string(encrypted))
			if err != nil || got != words {
				t.Fatalf("identity restore: %v", err)
			}
			trade, _ := mostro.DeriveKey(words, uint32(index))
			pub, _ := nostr.GetPublicKey(trade)
			identity, _ := mostro.DeriveKey(words, 0)
			identityPub, _ := nostr.GetPublicKey(identity)
			return &mostro.Session{IdentityPubkey: identityPub, TradePubkey: pub, Request: mostro.TakeRequest{RequestID: id, Node: strings.Repeat("a", 64), OrderID: uuid.NewString(), Protocol: 2}, State: "created", CreatedAt: time.Now().UTC(), Payments: map[string]*mostro.Payment{}, Seen: map[string]bool{}}, nil
		}
	}
	session, err := repo.CreateMostroSession(ctx, "test", []byte(cipher), makeSession(42))
	if err != nil {
		t.Fatal(err)
	}
	if session.ID == 0 || session.TradeIndex != 1 {
		t.Fatalf("invalid allocation %+v", session)
	}
	if _, err = repo.CreateMostroSession(ctx, "test", []byte(cipher), makeSession(43)); err == nil {
		t.Fatal("new key allocated while earlier take is unresolved")
	}
	reload, err := NewTransactionRepository(db).LoadMostroSession(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if reload.TradePubkey != session.TradePubkey || reload.TradeIndex != 1 || string(reload.EncryptedMnemonic) != cipher {
		t.Fatal("restart changed identity")
	}
	calls := 0
	build := func() (nostr.Event, error) {
		calls++
		e := nostr.Event{Kind: 14, CreatedAt: nostr.Now(), Content: "encrypted"}
		err := e.Sign(nostr.GeneratePrivateKey())
		return e, err
	}
	e, err := repo.MostroOutbox(ctx, session.ID, "take-buy", build)
	if err != nil {
		t.Fatal(err)
	}
	again, err := repo.MostroOutbox(ctx, session.ID, "take-buy", build)
	if err != nil || again.ID != e.ID || calls != 1 {
		t.Fatalf("outbox replay differs: %v", err)
	}
	session.State = "funded"
	session.Funded = true
	session.Payments["escrow"] = &mostro.Payment{Purpose: "escrow", Invoice: "invoice", Hash: strings.Repeat("b", 64), AmountSat: 100, Status: "IN_FLIGHT"}
	session.Seen[e.ID] = true
	if err = repo.SaveMostroSession(ctx, session, &e); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Table("mostro_payments").Count(&count)
	if count != 1 {
		t.Fatal("payment not committed")
	}
	db.Table("mostro_inbox_events").Count(&count)
	if count != 1 {
		t.Fatal("inbox not committed")
	}
	if err = repo.AuthorizeMostroRelease(ctx, 42); err == nil {
		t.Fatal("unconfirmed fiat authorized")
	}
	if err = db.Exec(`UPDATE transactions SET status='DexFiatHasArrived' WHERE id=42`).Error; err != nil {
		t.Fatal(err)
	}
	if err = repo.AuthorizeMostroRelease(ctx, 42); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CreateMostroSession(ctx, "alias", []byte(cipher), makeSession(43)); err == nil {
		t.Fatal("same identity accepted under another label with a reset counter")
	}
	next, err := repo.CreateMostroSession(ctx, "test", nil, makeSession(43))
	if err != nil {
		t.Fatal(err)
	}
	if next.TradeIndex != 2 {
		t.Fatalf("counter=%d", next.TradeIndex)
	}
}
