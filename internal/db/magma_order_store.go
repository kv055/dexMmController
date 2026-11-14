package db

import (
	"context"
	"errors"

	"dexTakerController/internal/lsp"
	"dexTakerController/internal/magma"
)

// MagmaOrderStore adapts LSPMagmaOrderRepository to magma.OrderStore.
type MagmaOrderStore struct {
	repo *LSPMagmaOrderRepository
}

func NewMagmaOrderStore(repo *LSPMagmaOrderRepository) *MagmaOrderStore {
	return &MagmaOrderStore{repo: repo}
}

func (s *MagmaOrderStore) Create(ctx context.Context, row magma.OrderRecord) error {
	return s.repo.Create(ctx, magmaOrderFromRecord(row))
}

func (s *MagmaOrderStore) GetByID(ctx context.Context, id string) (magma.OrderRecord, error) {
	row, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return magma.OrderRecord{}, err
	}
	return magmaOrderToRecord(row), nil
}

func (s *MagmaOrderStore) List(ctx context.Context, limit int) ([]magma.OrderRecord, error) {
	rows, err := s.repo.List(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]magma.OrderRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, magmaOrderToRecord(row))
	}
	return out, nil
}

func (s *MagmaOrderStore) Incomplete(ctx context.Context) ([]magma.OrderRecord, error) {
	rows, err := s.repo.Incomplete(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]magma.OrderRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, magmaOrderToRecord(row))
	}
	return out, nil
}

func (s *MagmaOrderStore) Update(ctx context.Context, row magma.OrderRecord) error {
	return s.repo.Update(ctx, magmaOrderFromRecord(row))
}

func (s *MagmaOrderStore) MarkPayment(ctx context.Context, id, paymentHash string) error {
	return s.repo.MarkPayment(ctx, id, paymentHash)
}

func magmaOrderToRecord(row LSPMagmaOrder) magma.OrderRecord {
	return magma.OrderRecord{
		ID:                  row.ID,
		Pool:                row.Pool,
		OrderID:             row.OrderID,
		PublicKey:           row.PublicKey,
		LSPBalanceSat:       row.LSPBalanceSat,
		AnnounceChannel:     row.AnnounceChannel,
		ChannelExpiryBlocks: row.ChannelExpiryBlocks,
		OrderState:          row.OrderState,
		PaymentState:        row.PaymentState,
		Bolt11Invoice:       row.Bolt11Invoice,
		OrderTotalSat:       row.OrderTotalSat,
		FeeTotalSat:         row.FeeTotalSat,
		FundingOutpoint:     row.FundingOutpoint,
		RemotePubkey:        row.RemotePubkey,
		PaymentHash:         row.PaymentHash,
		AutoRegistered:      row.AutoRegistered,
		FailureReason:       row.FailureReason,
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
	}
}

func magmaOrderFromRecord(row magma.OrderRecord) LSPMagmaOrder {
	return LSPMagmaOrder{
		ID:                  row.ID,
		Pool:                row.Pool,
		OrderID:             row.OrderID,
		PublicKey:           row.PublicKey,
		LSPBalanceSat:       row.LSPBalanceSat,
		AnnounceChannel:     row.AnnounceChannel,
		ChannelExpiryBlocks: row.ChannelExpiryBlocks,
		OrderState:          row.OrderState,
		PaymentState:        row.PaymentState,
		Bolt11Invoice:       row.Bolt11Invoice,
		OrderTotalSat:       row.OrderTotalSat,
		FeeTotalSat:         row.FeeTotalSat,
		FundingOutpoint:     row.FundingOutpoint,
		RemotePubkey:        row.RemotePubkey,
		PaymentHash:         row.PaymentHash,
		AutoRegistered:      row.AutoRegistered,
		FailureReason:       row.FailureReason,
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
	}
}

// LSPConfigRegistrar adapts LSPChannelConfigRepository to magma.LSPRegistrar.
type LSPConfigRegistrar struct {
	repo *LSPChannelConfigRepository
}

func NewLSPConfigRegistrar(repo *LSPChannelConfigRepository) *LSPConfigRegistrar {
	return &LSPConfigRegistrar{repo: repo}
}

func (r *LSPConfigRegistrar) Get(ctx context.Context) (lsp.Config, error) {
	cfg, err := r.repo.Get(ctx)
	if errors.Is(err, ErrLSPChannelConfigNotFound) {
		return lsp.Config{}, magma.ErrLSPConfigNotFound
	}
	return cfg, err
}

func (r *LSPConfigRegistrar) Update(ctx context.Context, cfg lsp.Config) (lsp.Config, error) {
	return r.repo.Update(ctx, cfg)
}
