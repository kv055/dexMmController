package escrow

// Minimal Bitcoin transaction parsing/serialization and the BIP143 (segwit v0) sighash,
// hand-rolled on the standard library so the module needs no btcd dependency. Only what
// the HodlHodl release flow requires is implemented: SIGHASH_ALL over P2WSH multisig
// inputs. It is deliberately small and unit-tested against the official BIP143 vector.

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// sighashAll is the only hash type used for a contract release.
const sighashAll uint32 = 0x01

type txInput struct {
	PrevTxID  [32]byte // serialized (wire) byte order
	PrevIndex uint32
	ScriptSig []byte
	Sequence  uint32
	Witness   [][]byte
}

type txOutput struct {
	Value    int64
	PkScript []byte
}

type transaction struct {
	Version    int32
	Inputs     []txInput
	Outputs    []txOutput
	LockTime   uint32
	HasWitness bool
}

// dsha256 is Bitcoin's double-SHA256.
func dsha256(b []byte) []byte {
	first := sha256.Sum256(b)
	second := sha256.Sum256(first[:])
	return second[:]
}

// ---------- varint ----------

func readVarint(r *bytes.Reader) (uint64, error) {
	b, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	switch {
	case b < 0xfd:
		return uint64(b), nil
	case b == 0xfd:
		var v uint16
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return 0, err
		}
		return uint64(v), nil
	case b == 0xfe:
		var v uint32
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return 0, err
		}
		return uint64(v), nil
	default:
		var v uint64
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return 0, err
		}
		return v, nil
	}
}

func writeVarint(buf *bytes.Buffer, v uint64) {
	switch {
	case v < 0xfd:
		buf.WriteByte(byte(v))
	case v <= 0xffff:
		buf.WriteByte(0xfd)
		_ = binary.Write(buf, binary.LittleEndian, uint16(v))
	case v <= 0xffffffff:
		buf.WriteByte(0xfe)
		_ = binary.Write(buf, binary.LittleEndian, uint32(v))
	default:
		buf.WriteByte(0xff)
		_ = binary.Write(buf, binary.LittleEndian, v)
	}
}

func readBytes(r *bytes.Reader) ([]byte, error) {
	n, err := readVarint(r)
	if err != nil {
		return nil, err
	}
	out := make([]byte, n)
	if _, err := io_ReadFull(r, out); err != nil {
		return nil, err
	}
	return out, nil
}

// io_ReadFull reads exactly len(buf) bytes from a bytes.Reader.
func io_ReadFull(r *bytes.Reader, buf []byte) (int, error) {
	for i := range buf {
		b, err := r.ReadByte()
		if err != nil {
			return i, err
		}
		buf[i] = b
	}
	return len(buf), nil
}

func writeScript(buf *bytes.Buffer, script []byte) {
	writeVarint(buf, uint64(len(script)))
	buf.Write(script)
}

// ---------- parse ----------

// parseTx decodes a raw transaction (legacy or segwit serialization).
func parseTx(raw []byte) (*transaction, error) {
	r := bytes.NewReader(raw)
	tx := &transaction{}

	var version int32
	if err := binary.Read(r, binary.LittleEndian, &version); err != nil {
		return nil, fmt.Errorf("read version: %w", err)
	}
	tx.Version = version

	// Detect the segwit marker (0x00) + flag (0x01) following the version.
	marker, err := r.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("read marker: %w", err)
	}
	if marker == 0x00 {
		flag, err := r.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("read flag: %w", err)
		}
		if flag != 0x01 {
			return nil, fmt.Errorf("unsupported segwit flag 0x%02x", flag)
		}
		tx.HasWitness = true
	} else {
		// Not a marker: it was the input-count varint. Put it back.
		_ = r.UnreadByte()
	}

	inCount, err := readVarint(r)
	if err != nil {
		return nil, fmt.Errorf("read input count: %w", err)
	}
	for i := uint64(0); i < inCount; i++ {
		var in txInput
		if _, err := io_ReadFull(r, in.PrevTxID[:]); err != nil {
			return nil, fmt.Errorf("input %d prev txid: %w", i, err)
		}
		if err := binary.Read(r, binary.LittleEndian, &in.PrevIndex); err != nil {
			return nil, fmt.Errorf("input %d prev index: %w", i, err)
		}
		if in.ScriptSig, err = readBytes(r); err != nil {
			return nil, fmt.Errorf("input %d scriptSig: %w", i, err)
		}
		if err := binary.Read(r, binary.LittleEndian, &in.Sequence); err != nil {
			return nil, fmt.Errorf("input %d sequence: %w", i, err)
		}
		tx.Inputs = append(tx.Inputs, in)
	}

	outCount, err := readVarint(r)
	if err != nil {
		return nil, fmt.Errorf("read output count: %w", err)
	}
	for i := uint64(0); i < outCount; i++ {
		var out txOutput
		if err := binary.Read(r, binary.LittleEndian, &out.Value); err != nil {
			return nil, fmt.Errorf("output %d value: %w", i, err)
		}
		if out.PkScript, err = readBytes(r); err != nil {
			return nil, fmt.Errorf("output %d pkScript: %w", i, err)
		}
		tx.Outputs = append(tx.Outputs, out)
	}

	if tx.HasWitness {
		for i := range tx.Inputs {
			items, err := readVarint(r)
			if err != nil {
				return nil, fmt.Errorf("input %d witness count: %w", i, err)
			}
			wit := make([][]byte, 0, items)
			for j := uint64(0); j < items; j++ {
				item, err := readBytes(r)
				if err != nil {
					return nil, fmt.Errorf("input %d witness item %d: %w", i, j, err)
				}
				wit = append(wit, item)
			}
			tx.Inputs[i].Witness = wit
		}
	}

	if err := binary.Read(r, binary.LittleEndian, &tx.LockTime); err != nil {
		return nil, fmt.Errorf("read locktime: %w", err)
	}
	return tx, nil
}

// ---------- serialize ----------

// serialize encodes the transaction. When the tx carries any witness data it is encoded
// in segwit form (marker+flag+witness), otherwise legacy.
func (tx *transaction) serialize() []byte {
	hasWitness := tx.HasWitness
	for _, in := range tx.Inputs {
		if len(in.Witness) > 0 {
			hasWitness = true
			break
		}
	}

	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, tx.Version)
	if hasWitness {
		buf.WriteByte(0x00)
		buf.WriteByte(0x01)
	}

	writeVarint(&buf, uint64(len(tx.Inputs)))
	for _, in := range tx.Inputs {
		buf.Write(in.PrevTxID[:])
		_ = binary.Write(&buf, binary.LittleEndian, in.PrevIndex)
		writeScript(&buf, in.ScriptSig)
		_ = binary.Write(&buf, binary.LittleEndian, in.Sequence)
	}

	writeVarint(&buf, uint64(len(tx.Outputs)))
	for _, out := range tx.Outputs {
		_ = binary.Write(&buf, binary.LittleEndian, out.Value)
		writeScript(&buf, out.PkScript)
	}

	if hasWitness {
		for _, in := range tx.Inputs {
			writeVarint(&buf, uint64(len(in.Witness)))
			for _, item := range in.Witness {
				writeScript(&buf, item)
			}
		}
	}

	_ = binary.Write(&buf, binary.LittleEndian, tx.LockTime)
	return buf.Bytes()
}

// ---------- BIP143 sighash ----------

// sighashSegwit computes the BIP143 (segwit v0) SIGHASH_ALL digest for input idx, where
// scriptCode is the P2WSH witness script and amount is that input's value in satoshis.
func (tx *transaction) sighashSegwit(idx int, scriptCode []byte, amount int64) []byte {
	var prevouts, sequences, outputs bytes.Buffer
	for _, in := range tx.Inputs {
		prevouts.Write(in.PrevTxID[:])
		_ = binary.Write(&prevouts, binary.LittleEndian, in.PrevIndex)
		_ = binary.Write(&sequences, binary.LittleEndian, in.Sequence)
	}
	for _, out := range tx.Outputs {
		_ = binary.Write(&outputs, binary.LittleEndian, out.Value)
		writeScript(&outputs, out.PkScript)
	}
	hashPrevouts := dsha256(prevouts.Bytes())
	hashSequence := dsha256(sequences.Bytes())
	hashOutputs := dsha256(outputs.Bytes())

	in := tx.Inputs[idx]
	var pre bytes.Buffer
	_ = binary.Write(&pre, binary.LittleEndian, tx.Version)
	pre.Write(hashPrevouts)
	pre.Write(hashSequence)
	pre.Write(in.PrevTxID[:])
	_ = binary.Write(&pre, binary.LittleEndian, in.PrevIndex)
	writeScript(&pre, scriptCode)
	_ = binary.Write(&pre, binary.LittleEndian, amount)
	_ = binary.Write(&pre, binary.LittleEndian, in.Sequence)
	pre.Write(hashOutputs)
	_ = binary.Write(&pre, binary.LittleEndian, tx.LockTime)
	_ = binary.Write(&pre, binary.LittleEndian, sighashAll)
	return dsha256(pre.Bytes())
}
