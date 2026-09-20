package lnd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

func TestPreparedFundingChecksCapBeforeSignAndSeparatesPublish(t *testing.T) {
	a, _ := btcutil.NewAddressWitnessPubKeyHash(make([]byte, 20), &chaincfg.MainNetParams)
	script, _ := txscript.PayToAddrScript(a)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(10000, script))
	p, _ := psbt.NewFromUnsignedTx(tx)
	p.Inputs[0].WitnessUtxo = wire.NewTxOut(10500, script)
	encoded, _ := p.B64Encode()
	var raw bytes.Buffer
	if err := tx.Serialize(&raw); err != nil {
		t.Fatal(err)
	}
	for _, cap := range []uint64{499, 500} {
		t.Run(strconv.FormatUint(cap, 10), func(t *testing.T) {
			signed, published, released := 0, 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v2/wallet/psbt/fund":
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body["min_confs"] != float64(1) || body["spend_unconfirmed"] != false {
						t.Error("unconfirmed input spending allowed")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"funded_psbt": encoded, "change_output_index": -1, "locked_utxos": []map[string]string{{"id": "lock", "expiration": strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)}}})
				case "/v2/wallet/psbt/finalize":
					signed++
					_ = json.NewEncoder(w).Encode(map[string]string{"raw_final_tx": base64.StdEncoding.EncodeToString(raw.Bytes())})
				case "/v2/wallet/utxos/release":
					released++
					fmt.Fprint(w, `{}`)
				case "/v2/wallet/tx":
					if r.Method == http.MethodPost {
						published++
						fmt.Fprint(w, `{}`)
					} else {
						_ = json.NewEncoder(w).Encode(map[string]string{"tx_hash": tx.TxHash().String()})
					}
				default:
					t.Error(r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			c := &Client{host: srv.URL, httpClient: srv.Client()}
			deadline := time.Now().Add(time.Minute)
			prepared, err := c.PrepareFunding(context.Background(), a.EncodeAddress(), 10000, cap, 2, deadline)
			if cap == 499 {
				if err == nil || signed != 0 || published != 0 || released != 1 {
					t.Fatalf("err=%v signed=%d published=%d released=%d", err, signed, published, released)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if signed != 1 || published != 0 || prepared.FeeSats != 500 {
				t.Fatalf("not separate preparation: %+v", prepared)
			}
			if err := c.PublishPreparedFunding(context.Background(), *prepared, time.Now().Add(-time.Second)); err == nil {
				t.Fatal("expired publish accepted")
			}
			if err := c.PublishPreparedFunding(context.Background(), *prepared, deadline); err != nil {
				t.Fatal(err)
			}
			if ok, err := c.PreparedFundingPublished(context.Background(), *prepared); err != nil || !ok {
				t.Fatalf("recovery: %v %v", ok, err)
			}
			if published != 1 {
				t.Fatalf("reconciliation repeated broadcast %d", published)
			}
		})
	}
}
