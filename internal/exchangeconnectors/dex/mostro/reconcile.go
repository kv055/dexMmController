package mostro

import (
	"context"
	"fmt"
	"time"
)

// Reconcile observes authenticated daemon history and wallet state. It never
// publishes a command or initiates a payment. Complete requires both a daemon
// release acknowledgement and settled escrow (plus returned bond, if required).
func (m *Manager) Reconcile(ctx context.Context, requestID uint64, ref string) (complete bool, err error) {
	if m == nil || m.Store == nil || m.Wallet == nil {
		return false, fmt.Errorf("mostro: recovery dependencies are not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	unlock, err := m.Store.LockMostro(ctx, requestID)
	if err != nil {
		return false, err
	}
	defer unlock()
	s, err := m.Store.LoadMostroSession(ctx, requestID)
	if err != nil {
		return false, err
	}
	if s == nil || s.Request.RequestID != requestID || s.TradeRef() != ref {
		return false, fmt.Errorf("mostro: reconciliation session does not match transaction")
	}
	if err := m.poll(ctx, s); err != nil {
		return false, err
	}
	if err := reviewError(s); err != nil {
		return false, err
	}
	if s.Released {
		err = m.confirmSettlement(ctx, s)
		return err == nil, err
	}
	if !s.Funded {
		return false, fmt.Errorf("mostro: daemon has not acknowledged funding")
	}
	return false, m.confirmFunding(ctx, s)
}
