package bitcoin

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

func TestProveFunding(t *testing.T) {
	a, _ := btcutil.NewAddressWitnessPubKeyHash(make([]byte, 20), &chaincfg.MainNetParams)
	script, _ := txscript.PayToAddrScript(a)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(1200, script))
	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		amount       uint64
		hash, status string
		tip          int
		wantErr      bool
		waiting      bool
	}{
		{"valid", 1200, tx.TxHash().String(), `{"confirmed":true,"block_height":99}`, 100, false, false},
		{"short", 1201, tx.TxHash().String(), `{"confirmed":true,"block_height":99}`, 100, true, false},
		{"wrong hash", 1200, strings.Repeat("0", 64), `{"confirmed":true,"block_height":99}`, 100, true, false},
		{"unconfirmed", 1200, tx.TxHash().String(), `{"confirmed":false}`, 100, true, true},
		{"one confirmation", 1200, tx.TxHash().String(), `{"confirmed":true,"block_height":100}`, 100, true, true},
		{"future height", 1200, tx.TxHash().String(), `{"confirmed":true,"block_height":101}`, 100, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/hex"):
					fmt.Fprint(w, hex.EncodeToString(buf.Bytes()))
				case strings.HasSuffix(r.URL.Path, "/status"):
					fmt.Fprint(w, tc.status)
				default:
					fmt.Fprint(w, tc.tip)
				}
			}))
			defer srv.Close()
			f := &NetworkDataFetcher{client: srv.Client(), baseURL: srv.URL}
			p, err := f.ProveFunding(context.Background(), tc.hash, a.EncodeAddress(), tc.amount, 2)
			if (err != nil) != tc.wantErr {
				t.Fatalf("proof=%+v error=%v", p, err)
			}
			if tc.waiting && !errors.Is(err, ErrFundingUnconfirmed) {
				t.Fatal(err)
			}
		})
	}
}
