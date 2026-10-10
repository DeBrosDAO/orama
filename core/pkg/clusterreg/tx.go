package clusterreg

import "fmt"

// RegisterClusterTypeURL is the Any type URL of orama.nodes.v1.MsgRegisterCluster.
const RegisterClusterTypeURL = "/orama.nodes.v1.MsgRegisterCluster"

// PubKeyTypeURL is the Any type URL of a compressed secp256k1 public key.
const PubKeyTypeURL = "/cosmos.crypto.secp256k1.PubKey"

// FeeDenom is the chain's base denom. Fees are an integer count of it.
const FeeDenom = "norama"

// SignInput is everything SIGN_MODE_DIRECT needs besides the message body.
// PubKey is the 33-byte compressed secp256k1 key that will sign the document.
type SignInput struct {
	Registration
	PubKey        []byte
	Sequence      uint64
	FeeAmount     string
	Gas           uint64
	ChainID       string
	AccountNumber uint64
	// TimeoutHeight is the last block the transaction may be included in; the chain refuses it
	// after that. Zero sets none. It is part of the signed body, so a node cannot hold the
	// transaction and release it later.
	TimeoutHeight uint64
}

// SignDoc is the protobuf cosmos.tx.v1beta1.SignDoc for this registration.
func (in SignInput) SignDoc() ([]byte, error) {
	if err := Validate(in.Registration); err != nil {
		return nil, err
	}
	return Direct{
		TypeURL: RegisterClusterTypeURL, Msg: EncodeRegisterCluster(in.Registration),
		PubKey: in.PubKey, Sequence: in.Sequence, FeeAmount: in.FeeAmount, Gas: in.Gas,
		ChainID: in.ChainID, AccountNumber: in.AccountNumber, TimeoutHeight: in.TimeoutHeight,
	}.SignDoc()
}

// TxRaw is the protobuf cosmos.tx.v1beta1.Tx built from the same body and
// auth info as SignDoc, plus the signature over that SignDoc.
func (in SignInput) TxRaw(signature []byte) ([]byte, error) {
	if err := Validate(in.Registration); err != nil {
		return nil, err
	}
	return Direct{
		TypeURL: RegisterClusterTypeURL, Msg: EncodeRegisterCluster(in.Registration),
		PubKey: in.PubKey, Sequence: in.Sequence, FeeAmount: in.FeeAmount, Gas: in.Gas,
		ChainID: in.ChainID, AccountNumber: in.AccountNumber, TimeoutHeight: in.TimeoutHeight,
	}.TxRaw(signature)
}

// Direct is a SIGN_MODE_DIRECT document for one protobuf message.
type Direct struct {
	TypeURL       string
	Msg           []byte
	PubKey        []byte
	Sequence      uint64
	FeeAmount     string
	Gas           uint64
	ChainID       string
	AccountNumber uint64
	// TimeoutHeight is the last block the transaction may be included in; the chain refuses it
	// after that. Zero sets none. It is part of the signed body, so a node cannot hold the
	// transaction and release it later.
	TimeoutHeight uint64
}

// SignDoc is the cosmos.tx.v1beta1.SignDoc for this message.
func (d Direct) SignDoc() ([]byte, error) {
	if err := d.validate(); err != nil {
		return nil, err
	}
	body := txBody(d.TypeURL, d.Msg, "", d.TimeoutHeight)
	auth := authInfo(d.PubKey, d.Sequence, d.FeeAmount, d.Gas)
	doc := appendBytesField(nil, 1, body)
	doc = appendBytesField(doc, 2, auth)
	doc = appendStringField(doc, 3, d.ChainID)
	return appendUvarintField(doc, 4, d.AccountNumber), nil
}

// TxRaw is the cosmos.tx.v1beta1.Tx for this message and signature.
func (d Direct) TxRaw(signature []byte) ([]byte, error) {
	if len(signature) != 64 {
		return nil, fmt.Errorf("signature is %d bytes, want 64", len(signature))
	}
	if err := d.validate(); err != nil {
		return nil, err
	}
	body := txBody(d.TypeURL, d.Msg, "", d.TimeoutHeight)
	auth := authInfo(d.PubKey, d.Sequence, d.FeeAmount, d.Gas)
	tx := appendBytesField(nil, 1, body)
	tx = appendBytesField(tx, 2, auth)
	return appendBytesField(tx, 3, signature), nil
}

func (d Direct) validate() error {
	if d.TypeURL == "" || len(d.Msg) == 0 {
		return fmt.Errorf("message is empty")
	}
	if len(d.PubKey) != 33 || (d.PubKey[0] != 0x02 && d.PubKey[0] != 0x03) {
		return fmt.Errorf("pubkey must be a 33-byte compressed secp256k1 key")
	}
	if d.ChainID == "" || len(d.ChainID) > 64 {
		return fmt.Errorf("chain id must be 1..64 characters")
	}
	if d.Gas == 0 {
		return fmt.Errorf("gas must be positive")
	}
	if !positiveInteger(d.FeeAmount) {
		return fmt.Errorf("fee must be a positive integer of %s", FeeDenom)
	}
	return nil
}

func positiveInteger(s string) bool {
	if s == "" || s[0] == '0' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// EncodeRegisterCluster is the protobuf orama.nodes.v1.MsgRegisterCluster.
// Field numbers match chain/x/nodes/types/tx.pb.go.
func EncodeRegisterCluster(r Registration) []byte {
	b := appendStringField(nil, 1, r.Operator)
	b = appendStringField(b, 2, r.ClusterID)
	b = appendStringField(b, 3, r.BaseDomain)
	for _, ep := range r.Endpoints {
		b = appendStringField(b, 4, ep)
	}
	if r.MetadataURI != "" {
		b = appendStringField(b, 5, r.MetadataURI)
	}
	return b
}

// EncodeMsgSendSignDoc rebuilds a SIGN_MODE_DIRECT document for one bank
// MsgSend. The RootWallet ORAMA vector test uses it to prove this encoder
// matches the Cosmos SDK encoding.
func EncodeMsgSendSignDoc(from, to, amount, memo string, pubKey []byte, sequence uint64, fee string, gas uint64, chainID string, account uint64) []byte {
	coin := appendStringField(nil, 1, FeeDenom)
	coin = appendStringField(coin, 2, amount)
	msg := appendStringField(nil, 1, from)
	msg = appendStringField(msg, 2, to)
	msg = appendBytesField(msg, 3, coin)
	body := txBody("/cosmos.bank.v1beta1.MsgSend", msg, memo, 0)
	auth := authInfo(pubKey, sequence, fee, gas)
	doc := appendBytesField(nil, 1, body)
	doc = appendBytesField(doc, 2, auth)
	doc = appendStringField(doc, 3, chainID)
	return appendUvarintField(doc, 4, account)
}

func txBody(typeURL string, msg []byte, memo string, timeoutHeight uint64) []byte {
	any := appendStringField(nil, 1, typeURL)
	any = appendBytesField(any, 2, msg)
	body := appendBytesField(nil, 1, any)
	if memo != "" {
		body = appendStringField(body, 2, memo)
	}
	if timeoutHeight > 0 {
		body = appendUvarintField(body, 3, timeoutHeight)
	}
	return body
}

func authInfo(pubKey []byte, sequence uint64, fee string, gas uint64) []byte {
	pk := appendBytesField(nil, 1, pubKey)
	pkAny := appendStringField(nil, 1, PubKeyTypeURL)
	pkAny = appendBytesField(pkAny, 2, pk)
	single := appendUvarintField(nil, 1, 1) // SIGN_MODE_DIRECT
	mode := appendBytesField(nil, 1, single)
	signer := appendBytesField(nil, 1, pkAny)
	signer = appendBytesField(signer, 2, mode)
	signer = appendUvarintField(signer, 3, sequence)
	coin := appendStringField(nil, 1, FeeDenom)
	coin = appendStringField(coin, 2, fee)
	feeMsg := appendBytesField(nil, 1, coin)
	feeMsg = appendUvarintField(feeMsg, 2, gas)
	auth := appendBytesField(nil, 1, signer)
	return appendBytesField(auth, 2, feeMsg)
}

func appendBytesField(dst []byte, field int, b []byte) []byte {
	dst = appendVarint(dst, uint64(field<<3|2))
	dst = appendVarint(dst, uint64(len(b)))
	return append(dst, b...)
}

func appendStringField(dst []byte, field int, s string) []byte {
	return appendBytesField(dst, field, []byte(s))
}

// appendUvarintField writes a varint scalar field, and nothing when v is zero: proto3
// omits a scalar at its zero value, and every message encoded here is proto3. A zero
// written out is not canonical: the RootWallet agent refuses to sign such bytes (an
// operator's first transaction has sequence 0), and the chain rebuilds the SignDoc it
// verifies canonically, so a zero account number would sign other bytes than it checks.
func appendUvarintField(dst []byte, field int, v uint64) []byte {
	if v == 0 {
		return dst
	}
	dst = appendVarint(dst, uint64(field<<3))
	return appendVarint(dst, v)
}

func appendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}
