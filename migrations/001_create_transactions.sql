-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS transactions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    
    -- Basic transaction info
    direction TEXT NOT NULL,
    fiat_amount REAL NOT NULL,
    fiat_currency TEXT NOT NULL,
    transfer_destination_cex TEXT NOT NULL,
    transfer_time_fiat_to_cex DATETIME,
    fiat_at_cex_arrival DATETIME,
    
    -- CEX transaction details
    cex_transaction_details JSON,
    cex_transaction_fee REAL,
    
    -- CEX withdrawal details
    cex_to_dex_withdrawal_tx_details JSON,
    cex_withdrawal_fee REAL,
    
    -- CEX counter position details
    cex_counter_position_open_details JSON,
    cex_counter_position_close_details JSON,
    cex_counter_position_fee REAL,
    
    -- DEX details
    dex_offer_creation JSON,
    dex_offer_taken DATETIME,
    dex_fiat_arrival JSON,
    dex_btc_release DATETIME,
    dex_fees REAL,
    
    -- Operational costs
    open_claw_llm_inference_cost REAL,
    vps_hosting_cost REAL,
    
    -- Transaction analytics
    total_transaction_pnl REAL,
    total_transaction_time_elapsed INTEGER,
    total_transaction_volatility_in_btc_price REAL,
    premium_over_cex_price_to_break_even_prediction REAL,
    
    -- Timestamps
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

-- Create indexes for commonly queried fields
CREATE INDEX idx_transactions_direction ON transactions(direction);
CREATE INDEX idx_transactions_cex ON transactions(transfer_destination_cex);
CREATE INDEX idx_transactions_created_at ON transactions(created_at);
CREATE INDEX idx_transactions_deleted_at ON transactions(deleted_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS transactions;
-- +goose StatementEnd
