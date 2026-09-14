package shop

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/crypto/sha3"
	"io"
	"math/big"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const Forwarder = "0x3ba60b60933916a7c87d0860dcee62a0ce34e3e2"
const RelayrPaymentAddress = "0x1c05f7841379d4393574c0ffa17908ec40ffd97d"
const relayrPaymentCodeHash = "0x6006b5acadb4cd60aa5c00cb844c34563e182dff83d4f4ff4fde226f7df16fa6"

type ForwardRequest struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Value     string `json:"value"`
	Gas       string `json:"gas"`
	Nonce     string `json:"nonce"`
	Deadline  int64  `json:"deadline"`
	Data      string `json:"data"`
	Signature string `json:"signature,omitempty"`
	Kind      string `json:"kind"`
}
type BundleChain struct {
	Connection *Connection      `json:"connection"`
	Requests   []ForwardRequest `json:"requests"`
	Status     string           `json:"status"`
	Ready      bool             `json:"ready"`
}
type SetupBundle struct {
	Resolved      bool   `json:"resolved"`
	SourceChain   int    `json:"sourceChain"`
	SourceProject string `json:"sourceProject"`
	Category      uint32 `json:"category"`

	Chains    []*BundleChain `json:"chains"`
	From      string         `json:"from,omitempty"`
	Price     string         `json:"price,omitempty"`
	Deadline  int64          `json:"deadline,omitempty"`
	Quote     *RelayrQuote   `json:"quote,omitempty"`
	Payment   *RelayrPayment `json:"payment,omitempty"`
	Attempt   *Attempt       `json:"attempt,omitempty"`
	Completed bool           `json:"completed"`
}
type RelayrEntry struct {
	Chain  int    `json:"chain"`
	Target string `json:"target"`
	Data   string `json:"data"`
	Value  string `json:"value"`
}
type RelayrPayment struct {
	Chain           int             `json:"chain"`
	Amount          string          `json:"amount"`
	Calldata        string          `json:"calldata"`
	Target          string          `json:"target"`
	Token           string          `json:"token"`
	PaymentDeadline json.RawMessage `json:"payment_deadline"`
}
type RelayrQuote struct {
	UUID     string          `json:"bundle_uuid"`
	Payments []RelayrPayment `json:"payment_info"`
}

func Testnet(id int) bool           { return id != 1 && id != 10 && id != 8453 && id != 42161 }
func (b *SetupBundle) Active() bool { return b.Deadline != 0 && !b.Completed }

// A plan becomes a durable signing intent before the wallet is opened. Even an
// interrupted signature dialog must not let a later plan reuse its live nonce.
func PlanBundle(ctx context.Context, rpc RPC, b *SetupBundle, from, price string) error {
	if !Address(from) {
		return errors.New("connect the project owner or operator wallet")
	}
	if b.Active() {
		if b.Attempt != nil && b.Attempt.State != "confirmed" && b.Attempt.State != "reverted" && b.Attempt.State != "replaced" {
			return errors.New("check the saved funding transaction before reviewing again")
		}
		for _, chain := range b.Chains {
			if len(chain.Requests) == 0 {
				continue
			}
			var block struct {
				Timestamp string `json:"timestamp"`
			}
			if err := rpc(ctx, chain.Connection.ChainID, "eth_getBlockByNumber", []any{"finalized", false}, &block); err != nil {
				return err
			}
			timestamp, ok := quantity(block.Timestamp)
			if !ok || !timestamp.IsInt64() || timestamp.Int64() <= b.Deadline {
				return errors.New("resume the saved setup; its wallet authorizations have not expired on every chain yet")
			}
		}
	}
	next := &SetupBundle{Resolved: b.Resolved, SourceChain: b.SourceChain, SourceProject: b.SourceProject, Category: b.Category, From: strings.ToLower(from), Price: price, Deadline: time.Now().Add(time.Hour).Unix()}
	var currency uint32
	for i, old := range b.Chains {
		c := &Connection{ChainID: old.Connection.ChainID, Hook: old.Connection.Hook, Category: old.Connection.Category}
		n, _ := NetworkFor(c.ChainID)
		state, err := ReadConnection(ctx, rpc, c, from, "latest")
		if err != nil {
			return fmt.Errorf("%s: %w", n.Label, err)
		}
		if i > 0 && state.Currency != currency {
			return errors.New("these shops use different price currencies; choose shops with the same price currency")
		}
		currency = state.Currency
		if err = PlanConnection(c, state, from, price); err != nil {
			return fmt.Errorf("%s: %w", n.Label, err)
		}
		chain := &BundleChain{Connection: c, Requests: []ForwardRequest{}, Status: "Already configured; checking finality"}
		next.Chains = append(next.Chains, chain)
		if c.Transaction == nil {
			continue
		}
		w, err := rpcWords(ctx, rpc, c.ChainID, Forwarder, abiCall("nonces(address)", abiAddress(from)), "latest")
		if err != nil {
			return err
		}
		nonce := wordN(w[0])
		// The second call is prepared against the state produced by the grant.
		virtual := *state
		for {
			tx, err := NextSetup(c, &virtual, from)
			if err != nil {
				return err
			}
			if tx == nil {
				break
			}
			trusted, err := rpcWords(ctx, rpc, c.ChainID, tx.To, abiCall("isTrustedForwarder(address)", abiAddress(Forwarder)), "latest")
			if err != nil || wordN(trusted[0]).Cmp(big.NewInt(1)) != 0 {
				return fmt.Errorf("%s: contract does not trust the Juicebox forwarder", n.Label)
			}
			var result string
			if err = rpc(ctx, c.ChainID, "eth_call", []any{map[string]string{"from": from, "to": tx.To, "data": tx.Data}, "latest"}, &result); err != nil {
				return fmt.Errorf("%s: setup simulation failed: %w", n.Label, err)
			}
			var estimate string
			if err = rpc(ctx, c.ChainID, "eth_estimateGas", []any{map[string]string{"from": from, "to": tx.To, "data": tx.Data}}, &estimate); err != nil {
				return err
			}
			gas, ok := quantity(estimate)
			if !ok || !gas.IsUint64() || gas.Uint64() > 4000000 {
				return errors.New("unexpected setup gas estimate")
			}
			gas.Mul(gas, big.NewInt(2))
			gas.Add(gas, big.NewInt(100000))
			chain.Requests = append(chain.Requests, ForwardRequest{From: next.From, To: tx.To, Value: "0", Gas: gas.String(), Nonce: nonce.String(), Deadline: next.Deadline, Data: tx.Data, Kind: tx.Kind})
			nonce.Add(nonce, big.NewInt(1))
			if tx.Kind == "permission" {
				virtual.PublisherGranted = true
			} else {
				virtual.Criteria = *c.Expected
			}
		}
		c.Transaction = nil
		chain.Status = "Awaiting wallet signatures"
	}
	*b = *next
	return nil
}

func dynamicBytes(data string) string {
	data = strings.TrimPrefix(data, "0x")
	return abiN(uint64(len(data)/2)) + data + strings.Repeat("0", (64-len(data)%64)%64)
}

// ABI: executeBatch((address,address,uint256,uint256,uint48,bytes,bytes)[],address).
// Nonces are in the signed EIP-712 message, not in ForwardRequestData.
func ForwardBatch(requests []ForwardRequest) string {
	tuples := []string{}
	offsets := []string{}
	offset := len(requests) * 32
	for _, r := range requests {
		gas, _ := new(big.Int).SetString(r.Gas, 10)
		data := dynamicBytes(r.Data)
		sig := dynamicBytes(r.Signature)
		tuple := abiAddress(r.From) + abiAddress(r.To) + abiN(0) + abiUint(gas) + abiN(uint64(r.Deadline)) + abiN(224) + abiN(uint64(224+len(data)/2)) + data + sig
		offsets = append(offsets, abiN(uint64(offset)))
		tuples = append(tuples, tuple)
		offset += len(tuple) / 2
	}
	return abiCall("executeBatch((address,address,uint256,uint256,uint48,bytes,bytes)[],address)", abiN(64), abiN(0), abiN(uint64(len(requests))), strings.Join(offsets, ""), strings.Join(tuples, ""))
}
func ValidateBundleState(ctx context.Context, rpc RPC, b *SetupBundle) error {
	if !b.Active() || time.Now().Unix()+60 >= b.Deadline {
		return errors.New("the saved authorizations are expiring; wait for finality, then review setup again")
	}
	for _, chain := range b.Chains {
		if len(chain.Requests) == 0 {
			continue
		}
		c := chain.Connection
		state, err := ReadConnection(ctx, rpc, c, b.From, "latest")
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(state, c.PlanState) {
			return errors.New("shop permissions or posting rules changed; wait for the saved authorization to expire before reviewing again")
		}
		w, err := rpcWords(ctx, rpc, c.ChainID, Forwarder, abiCall("nonces(address)", abiAddress(b.From)), "latest")
		if err != nil {
			return err
		}
		if wordN(w[0]).String() != chain.Requests[0].Nonce {
			return errors.New("the wallet's forwarder nonce changed; check setup progress before continuing")
		}
	}
	return nil
}
func BundleEntries(ctx context.Context, rpc RPC, b *SetupBundle) ([]RelayrEntry, error) {
	if err := ValidateBundleState(ctx, rpc, b); err != nil {
		return nil, err
	}
	entries := []RelayrEntry{}
	for _, chain := range b.Chains {
		if len(chain.Requests) == 0 {
			continue
		}
		for _, request := range chain.Requests {
			if !regexp.MustCompile(`^0x[0-9a-fA-F]{130}$`).MatchString(request.Signature) {
				return nil, errors.New("sign the setup requests on each network first")
			}
		}
		data := ForwardBatch(chain.Requests)
		var result string
		if err := rpc(ctx, chain.Connection.ChainID, "eth_call", []any{map[string]string{"to": Forwarder, "data": data}, "latest"}, &result); err != nil {
			return nil, fmt.Errorf("forwarded setup simulation failed: %w", err)
		}
		entries = append(entries, RelayrEntry{Chain: chain.Connection.ChainID, Target: Forwarder, Data: data, Value: "0"})
	}
	if len(entries) == 0 {
		return nil, errors.New("these shops are already configured; check confirmation")
	}
	return entries, nil
}

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func PaymentDetails(p RelayrPayment, uuid string, testnet bool, now int64) (int64, error) {
	invalid := errors.New("Relayr returned an invalid funding quote")
	if _, ok := NetworkFor(p.Chain); !ok || Testnet(p.Chain) != testnet {
		return 0, invalid
	}
	if !strings.EqualFold(p.Target, RelayrPaymentAddress) || !strings.EqualFold(p.Token, "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee") {
		return 0, invalid
	}
	amount, ok := relayrAmount(p.Amount)
	if !ok || amount.Sign() < 0 || amount.BitLen() > 256 {
		return 0, invalid
	}
	uuid = strings.ToLower(uuid)
	data := strings.ToLower(p.Calldata)
	if !uuidRE.MatchString(uuid) || len(data) != 138 || !hexRE.MatchString(data) || data[:10] != "0x103903a7" || data[10:74] != strings.ReplaceAll(uuid, "-", "")+strings.Repeat("0", 32) {
		return 0, invalid
	}
	deadline := wordN(data[74:])
	if deadline.BitLen() > 40 {
		return 0, invalid
	}
	raw := strings.Trim(string(p.PaymentDeadline), `"`)
	quoted, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		date, e := time.Parse(time.RFC3339Nano, raw)
		if e != nil {
			return 0, invalid
		}
		quoted = date.Unix()
	}
	if quoted != deadline.Int64() || quoted <= now+15 {
		return 0, errors.New("the Relayr funding quote expired; request a new quote")
	}
	return quoted, nil
}
func QuoteBundle(ctx context.Context, entries []RelayrEntry) (*RelayrQuote, error) {
	body, _ := json.Marshal(map[string]any{"transactions": entries, "virtual_nonce_mode": "Disabled"})
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.relayr.ba5ed.com/v1/bundle/prepaid", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("Relayr did not return a quote; your saved signatures can be reused")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 && res.StatusCode != 201 {
		return nil, fmt.Errorf("Relayr quote request failed (HTTP %d)", res.StatusCode)
	}
	var q RelayrQuote
	if err = json.NewDecoder(io.LimitReader(res.Body, 128<<10)).Decode(&q); err != nil {
		return nil, errors.New("invalid Relayr response")
	}
	if !uuidRE.MatchString(q.UUID) || len(q.Payments) == 0 || len(q.Payments) > 8 {
		return nil, errors.New("Relayr returned no funding options")
	}
	seen := map[int]bool{}
	for _, p := range q.Payments {
		if seen[p.Chain] {
			return nil, errors.New("duplicate Relayr funding chain")
		}
		seen[p.Chain] = true
		if _, err = PaymentDetails(p, q.UUID, Testnet(entries[0].Chain), time.Now().Unix()); err != nil {
			return nil, err
		}
	}
	return &q, nil
}
func ValidatePayment(ctx context.Context, rpc RPC, b *SetupBundle, p RelayrPayment) error {
	if b.Quote == nil {
		return errors.New("get a Relayr quote first")
	}
	deadline, err := PaymentDetails(p, b.Quote.UUID, Testnet(b.Chains[0].Connection.ChainID), time.Now().Unix())
	if err != nil {
		return err
	}
	if deadline+120 >= b.Deadline {
		return errors.New("the saved signatures expire too soon for this funding quote; wait for their expiry and review setup again")
	}
	var chain, code, result string
	if err := rpc(ctx, p.Chain, "eth_chainId", []any{}, &chain); err != nil {
		return err
	}
	if !equalQuantity(chain, fmt.Sprintf("0x%x", p.Chain)) {
		return errors.New("funding RPC chain mismatch")
	}
	if err := rpc(ctx, p.Chain, "eth_getCode", []any{RelayrPaymentAddress, "latest"}, &code); err != nil {
		return err
	}
	if len(code) > 4098 || len(code) < 4 || !hexRE.MatchString(code) || hashHex(code) != relayrPaymentCodeHash {
		return errors.New("Relayr funding contract code is not recognized")
	}
	amount, _ := relayrAmount(p.Amount)
	return rpc(ctx, p.Chain, "eth_call", []any{map[string]string{"from": b.From, "to": p.Target, "data": p.Calldata, "value": "0x" + amount.Text(16), "gas": "0x249f0"}, "latest"}, &result)
}
func VerifyBundlePayment(ctx context.Context, rpc RPC, b *SetupBundle, hash string) (string, error) {
	if b.Payment == nil || b.Attempt == nil {
		return "", errors.New("no funding transaction to check")
	}
	amount, _ := new(big.Int).SetString(b.Payment.Amount, 10)
	return verifyTransaction(ctx, rpc, b.Payment.Chain, b.Payment.Target, b.Payment.Calldata, "0x"+amount.Text(16), b.Attempt, hash)
}

// Relayr's status is not proof. Only matching finalized AND current contract
// state can connect a shop, including when a batch contains a failed inner call.
func CheckBundle(ctx context.Context, rpc RPC, b *SetupBundle) bool {
	all := true
	for _, chain := range b.Chains {
		chain.Ready = false
		c := chain.Connection
		if c.Expected == nil {
			chain.Status = "Review setup first"
			all = false
			continue
		}
		ready := true
		for _, block := range []string{"finalized", "latest"} {
			state, err := ReadConnection(ctx, rpc, c, b.From, block)
			if err != nil {
				chain.Status = "Could not check this network: " + err.Error()
				ready = false
				break
			}
			if c.PlanState == nil || state.Owner != c.PlanState.Owner || state.ProjectID != c.PlanState.ProjectID || !state.PublisherGranted || !reflect.DeepEqual(state.Criteria, *c.Expected) {
				chain.Status = "Waiting for posting permissions and rules to be confirmed"
				ready = false
				break
			}
		}
		chain.Ready = ready
		if ready {
			chain.Status = "Posting setup confirmed"
		} else {
			all = false
		}
	}
	return all
}

func hashHex(code string) string {
	raw, err := hex.DecodeString(strings.TrimPrefix(code, "0x"))
	if err != nil {
		return ""
	}
	h := sha3.NewLegacyKeccak256()
	h.Write(raw)
	return "0x" + hex.EncodeToString(h.Sum(nil))
}

// Relayr's live API uses hex quantities; decimal fixtures and older responses
// are accepted too. Do not interpret a decimal leading zero as octal.
func relayrAmount(value string) (*big.Int, bool) {
	if strings.HasPrefix(value, "0x") {
		return quantity(value)
	}
	if !regexp.MustCompile(`^[0-9]+$`).MatchString(value) {
		return nil, false
	}
	return new(big.Int).SetString(value, 10)
}
