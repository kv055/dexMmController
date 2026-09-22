package db

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This suite uses the real migrations and PostgreSQL locks/constraints. The
// supplied database is never truncated: all test objects live in a fresh schema.
func bisqPostgres(t *testing.T) (*TransactionRepository, *gorm.DB) {
	t.Helper()
	dsn := os.Getenv("BISQ_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set BISQ_TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	config := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	root, err := gorm.Open(postgres.Open(dsn), config)
	if err != nil {
		t.Fatal(err)
	}
	rootPool, _ := root.DB()
	t.Cleanup(func() { _ = rootPool.Close() })
	schema := "bisq_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := root.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Error(err)
		}
	})
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		dsn = u.String()
	} else {
		dsn += " search_path=" + schema
	}
	database, err := gorm.Open(postgres.Open(dsn), config)
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := database.DB()
	pool.SetMaxOpenConns(12)
	t.Cleanup(func() { _ = pool.Close() })
	if err := database.AutoMigrate(&Transaction{}); err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`CREATE TABLE accounts(id BIGSERIAL PRIMARY KEY);
CREATE TABLE payment_methods(name TEXT, account_id BIGINT, is_active BOOLEAN)`).Error; err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"20260916190000_bisq_execution_safety.sql", "20260917090000_bisq_durable_actions.sql"} {
		raw, err := os.ReadFile("../../../dexUnifiedApi/db/migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err := database.Exec(string(raw)).Error; err != nil {
			t.Fatal(err)
		}
	}
	return NewTransactionRepository(database), database
}

func insertBisqQuote(t *testing.T, repo *TransactionRepository) *BisqExecutionQuote {
	t.Helper()
	quote := &BisqExecutionQuote{ID: uuid.NewString(), OfferID: "offer-1", TradeMode: "hedgefull_single_collateral", PaymentAccountID: "account-1", FundingAddress: "test-address", Quote: json.RawMessage(`{}`), Limits: json.RawMessage(`{}`), ResolvedRoute: json.RawMessage(`{}`), ExpiresAt: time.Now().Add(time.Minute)}
	if err := repo.CreateBisqExecutionQuote(context.Background(), quote); err != nil {
		t.Fatal(err)
	}
	return quote
}

func buildBisqTestRow(q *BisqExecutionQuote, now time.Time) (*Transaction, error) {
	raw, err := MarshalDexOfferCreation(DexOfferMeta{BisqExecution: &BisqExecutionPlan{Version: 2, QuoteID: q.ID, Stage: BisqStageAccepted, DeadlineAt: now.Add(30 * time.Minute)}})
	return &Transaction{Dex: DexBisq, TradeMode: q.TradeMode, Status: StatusCexTradeflowStarted, DexOfferCreation: raw}, err
}

func TestBisqPostgresAtomicAcceptance(t *testing.T) {
	repo, database := bisqPostgres(t)
	ctx := context.Background()
	quote := insertBisqQuote(t, repo)
	// Fail AFTER inserting the transaction, proving the entire acceptance rolls back.
	if err := database.Exec(`CREATE FUNCTION reject_bisq_run() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected failure'; END $$;
CREATE TRIGGER reject_bisq_run BEFORE INSERT ON bisq_execution_runs FOR EACH ROW EXECUTE FUNCTION reject_bisq_run()`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AcceptBisqQuote(ctx, quote.ID, "key-1", buildBisqTestRow); err == nil {
		t.Fatal("injected failure was not returned")
	}
	var count int64
	database.Model(&Transaction{}).Count(&count)
	if count != 0 {
		t.Fatal("failed acceptance left an executable transaction")
	}
	if err := database.Exec("DROP TRIGGER reject_bisq_run ON bisq_execution_runs").Error; err != nil {
		t.Fatal(err)
	}
	const workers = 10
	ids := make(chan uint, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			row, err := repo.AcceptBisqQuote(ctx, quote.ID, "key-1", buildBisqTestRow)
			if err != nil {
				errs <- err
				return
			}
			ids <- row.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var id uint
	for got := range ids {
		if id != 0 && id != got {
			t.Fatal("concurrent retries created distinct executions")
		}
		id = got
	}
	database.Model(&Transaction{}).Count(&count)
	if count != 1 {
		t.Fatalf("transactions=%d", count)
	}
	stored, err := repo.GetBisqExecutionQuote(ctx, quote.ID)
	if err != nil || stored.TransactionID == nil || *stored.TransactionID != id || stored.AcceptedAt == nil {
		t.Fatalf("quote binding not atomic: %+v %v", stored, err)
	}
	if _, err := repo.AcceptBisqQuote(ctx, quote.ID, "different-key", buildBisqTestRow); !errors.Is(err, ErrBisqConflict) {
		t.Fatalf("quote accepted twice with different keys: %v", err)
	}
	other := insertBisqQuote(t, repo)
	if _, err := repo.AcceptBisqQuote(ctx, other.ID, "key-1", buildBisqTestRow); !errors.Is(err, ErrBisqConflict) {
		t.Fatalf("idempotency key reused across quotes: %v", err)
	}
	if _, err := repo.AcceptBisqQuote(ctx, other.ID, "key-2", buildBisqTestRow); !errors.Is(err, ErrBisqConflict) {
		t.Fatalf("second trade stole durable reservation: %v", err)
	}
}

func TestBisqPostgresFencingAndReceiptPreservation(t *testing.T) {
	repo, database := bisqPostgres(t)
	ctx := context.Background()
	quote := insertBisqQuote(t, repo)
	row, err := repo.AcceptBisqQuote(ctx, quote.ID, "key", buildBisqTestRow)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := repo.ClaimBisqRun(ctx, row.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{lease.Owner, uuid.NewString()} {
		if _, err := repo.ClaimBisqRun(ctx, row.ID, owner); !errors.Is(err, ErrBisqLeaseLost) {
			t.Fatalf("active lease reacquired: %v", err)
		}
	}
	action := BisqAction{Kind: "buy", ExternalKey: "buy-key", Request: json.RawMessage(`{"max_fiat":"100.00"}`)}
	created, err := repo.BeginBisqAction(ctx, *lease, action)
	if err != nil || !created {
		t.Fatalf("intent: %v %v", created, err)
	}
	if created, err := repo.BeginBisqAction(ctx, *lease, action); err != nil || created {
		t.Fatalf("intent duplicated: %v %v", created, err)
	}
	if err := database.Exec("UPDATE bisq_execution_leases SET lease_until=clock_timestamp()-interval '1 second' WHERE transaction_id=?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	next, err := repo.ClaimBisqRun(ctx, row.ID, uuid.NewString())
	if err != nil || next.Fence <= lease.Fence {
		t.Fatalf("fence did not increase: %+v %v", next, err)
	}
	receipt := json.RawMessage(`{"order_id":"bought-once","btc":"0.01"}`)
	if err := repo.CompleteBisqAction(ctx, *lease, "buy", receipt); !errors.Is(err, ErrBisqLeaseLost) {
		t.Fatalf("stale receipt writer was not fenced: %v", err)
	}
	if err := repo.ParkBisqRun(ctx, *lease, "stale worker"); !errors.Is(err, ErrBisqLeaseLost) {
		t.Fatalf("stale review writer was not fenced: %v", err)
	}
	_ = repo.ReleaseBisqLease(ctx, *lease)
	if err := repo.RenewBisqLease(ctx, *next); err != nil {
		t.Fatalf("stale release stole current lease: %v", err)
	}
	if err := repo.CompleteBisqAction(ctx, *next, "buy", receipt); err != nil {
		t.Fatal(err)
	}
	// A separate receipt/display writer completes after the worker read its row.
	if err := database.Model(row).Updates(map[string]any{"cex_bitcoin_amount": 0.01, "cex_transaction_details": receipt}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`UPDATE transactions SET dex_offer_creation = dex_offer_creation || '{"newer_adapter_receipt":{"order":"bought-once"}}'::jsonb WHERE id=?`, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.ParkBisqRun(ctx, *next, "operator review"); err != nil {
		t.Fatal(err)
	}
	if err := database.First(row, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.CexBitcoinAmount != 0.01 || !strings.Contains(string(row.CexTransactionDetails), "bought-once") || row.Status != StatusTransactionNeedsReview {
		t.Fatalf("parking overwrote financial evidence: %+v", row)
	}
	if !strings.Contains(string(row.DexOfferCreation), "newer_adapter_receipt") {
		t.Fatal("parking discarded metadata unknown to the current model")
	}
	_, actions, err := repo.GetBisqRun(ctx, row.ID)
	if err != nil || len(actions) != 1 || actions[0].State != "confirmed" {
		t.Fatalf("ledger lost receipt: %+v %v", actions, err)
	}
	var reserved int64
	database.Table("bisq_execution_reservations").Where("transaction_id=?", row.ID).Count(&reserved)
	if reserved != 1 {
		t.Fatal("review released financially active reservation")
	}
}

func TestBisqPostgresDeadlineAndLinkedAccount(t *testing.T) {
	repo, database := bisqPostgres(t)
	ctx := context.Background()
	quote := insertBisqQuote(t, repo)
	row, err := repo.AcceptBisqQuote(ctx, quote.ID, "key", buildBisqTestRow)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := repo.ClaimBisqRun(ctx, row.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("UPDATE bisq_execution_runs SET deadline_at=clock_timestamp()-interval '1 second' WHERE transaction_id=?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.BeginBisqAction(ctx, *lease, BisqAction{Kind: "take", ExternalKey: "take-key", Request: json.RawMessage(`{}`)}); !errors.Is(err, ErrBisqExpired) {
		t.Fatalf("expired execution started an action: %v", err)
	}
	if err := repo.ParkBisqRun(ctx, *lease, "expired before spending"); err != nil {
		t.Fatal(err)
	}
	var reserved int64
	database.Table("bisq_execution_reservations").Count(&reserved)
	if reserved != 0 {
		t.Fatal("unspent expiry leaked reservation")
	}
	if err := database.Exec(`INSERT INTO accounts(id) VALUES(1),(2);
INSERT INTO payment_methods VALUES('SEPA',2,true);
INSERT INTO bisq_payment_account_mappings(local_account_id,payment_method_name,currency,bisq_payment_account_id)
VALUES(1,'SEPA','EUR','stale-account'),(2,'SEPA','EUR','current-account')`).Error; err != nil {
		t.Fatal(err)
	}
	mapping, err := repo.GetBisqPaymentAccountMapping(ctx, "SEPA", "EUR")
	if err != nil || mapping == nil || mapping.BisqPaymentAccountID != "current-account" {
		t.Fatalf("selected stale mapping: %+v %v", mapping, err)
	}
}

func TestBisqPostgresLegacyRunBlocksNewFunding(t *testing.T) {
	repo, database := bisqPostgres(t)
	quote := insertBisqQuote(t, repo)
	raw, err := MarshalDexOfferCreation(DexOfferMeta{BisqExecution: &BisqExecutionPlan{Version: 1, Stage: BisqStageAccepted}})
	if err != nil {
		t.Fatal(err)
	}
	legacy := &Transaction{Dex: DexBisq, Status: StatusCexTradeflowStarted, DexOfferCreation: raw}
	if err := database.Create(legacy).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AcceptBisqQuote(context.Background(), quote.ID, "key", buildBisqTestRow); !errors.Is(err, ErrBisqConflict) {
		t.Fatalf("new funding overlapped an unreconciled legacy execution: %v", err)
	}
}
