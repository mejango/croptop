// Package shop stores wallet deployment intents before signing and independently
// verifies their receipts. It never holds a wallet key or broadcasts transactions.
package shop

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/sha3"
)

const Deployer = "0xb552eb94284f94b833837d4b2cbb237128415d4e"
const DeploySelector = "0x86dbf9c7" // six-argument v6 deployFor, from the shared ABI
const HookDeployer = "0xb7b8ec35e2dd84afff04ee769c6189e7a4d44a78"

type Network struct {
	ID      int    `json:"id"`
	Label   string `json:"label"`
	Setting string `json:"setting"`
	RPC     string `json:"rpc"`
	Testnet bool   `json:"testnet"`
}

var Networks = []Network{
	{1, "Ethereum", "ethereumMainnet", "https://juicebox.center/v1/rpc/1", false},
	{10, "Optimism", "optimismMainnet", "https://juicebox.center/v1/rpc/10", false},
	{42161, "Arbitrum", "arbitrumMainnet", "https://juicebox.center/v1/rpc/42161", false},
	{8453, "Base", "baseMainnet", "https://juicebox.center/v1/rpc/8453", false},
	{11155111, "Ethereum Sepolia", "ethereumSepolia", "https://juicebox.center/v1/rpc/11155111", true},
	{11155420, "Optimism Sepolia", "optimismSepolia", "https://juicebox.center/v1/rpc/11155420", true},
	{421614, "Arbitrum Sepolia", "arbitrumSepolia", "https://juicebox.center/v1/rpc/421614", true},
	{84532, "Base Sepolia", "baseSepolia", "https://juicebox.center/v1/rpc/84532", true},
}

func NetworkFor(id int) (Network, bool) {
	for _, n := range Networks {
		if n.ID == id {
			return n, true
		}
	}
	return Network{}, false
}

var addressRE = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
var hashRE = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
var hexRE = regexp.MustCompile(`^0x[0-9a-fA-F]+$`)

func Address(s string) bool {
	return addressRE.MatchString(s) && !strings.EqualFold(s, "0x"+strings.Repeat("0", 40))
}
func Hash(s string) bool { return hashRE.MatchString(s) }
func Random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

type Config struct {
	Name     string `json:"name"`
	Symbol   string `json:"symbol"`
	Owner    string `json:"owner"`
	Price    string `json:"price"`
	ChainIDs []int  `json:"chainIds"`
}
type Attempt struct {
	From  string `json:"from"`
	Nonce string `json:"nonce"`
	Hash  string `json:"hash,omitempty"`
	State string `json:"state"` // signing, pending, confirmed, reverted, replaced, cancelled
}
type Chain struct {
	ID        int       `json:"id"`
	Data      string    `json:"data"`
	Value     string    `json:"value"`
	Attempts  []Attempt `json:"attempts"`
	Address   string    `json:"address,omitempty"`
	ProjectID string    `json:"projectId,omitempty"`
	Applied   bool      `json:"applied"`
}
type Session struct {
	Bundle     *SetupBundle   `json:"bundle,omitempty"`
	Connection *Connection    `json:"connection,omitempty"`
	ID         string         `json:"id"`
	SiteID     string         `json:"siteId"`
	SiteName   string         `json:"siteName"`
	Token      string         `json:"token,omitempty"`
	Salt       string         `json:"salt"`
	StartsAt   int64          `json:"startsAt"`
	Config     *Config        `json:"config,omitempty"`
	Previous   map[string]any `json:"previous,omitempty"`
	Chains     []Chain        `json:"chains"`
}

func (s *Session) ValidatePlan() error {
	c := s.Config
	if c == nil || len(strings.TrimSpace(c.Name)) == 0 || len(c.Name) > 100 || len(c.Symbol) == 0 || len(c.Symbol) > 20 || !Address(c.Owner) {
		return errors.New("enter a name, ID and revenue wallet")
	}
	if !regexp.MustCompile(`^(0|[1-9][0-9]{0,10})(\.[0-9]{1,18})?$`).MatchString(c.Price) {
		return errors.New("enter a price in ETH with at most 18 decimals")
	}
	if len(c.ChainIDs) == 0 || len(c.ChainIDs) > 8 || len(s.Chains) != len(c.ChainIDs) {
		return errors.New("select networks")
	}
	seen := map[int]bool{}
	var testnet bool
	for i, id := range c.ChainIDs {
		n, ok := NetworkFor(id)
		if !ok || seen[id] {
			return errors.New("unsupported or duplicate network")
		}
		seen[id] = true
		if i == 0 {
			testnet = n.Testnet
		} else if testnet != n.Testnet {
			return errors.New("select mainnets or testnets together")
		}
		ch := s.Chains[i]
		if ch.ID != id || len(ch.Data) < 74 || !strings.HasPrefix(strings.ToLower(ch.Data), DeploySelector+strings.Repeat("0", 64)) || len(ch.Data) > 100000 || len(ch.Data)%2 != 0 || !hexRE.MatchString(ch.Data) || !strings.Contains(strings.ToLower(ch.Data), strings.TrimPrefix(s.Salt, "0x")) || !hexRE.MatchString(ch.Value) {
			return errors.New("invalid deployment intent")
		}
		if len(ch.Attempts) > 0 || ch.Address != "" || ch.ProjectID != "" || ch.Applied {
			return errors.New("a new intent cannot contain results")
		}
	}
	return nil
}
func Load(dir, siteID string) (*Session, error) {
	var s Session
	b, e := os.ReadFile(filepath.Join(dir, siteID+".json"))
	if e != nil {
		return nil, e
	}
	e = json.Unmarshal(b, &s)
	if e == nil && s.SiteID != siteID {
		e = errors.New("session site mismatch")
	}
	return &s, e
}
func Save(dir string, s *Session) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".shop-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), filepath.Join(dir, s.SiteID+".json")); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

type RPC func(context.Context, int, string, any, any) error

func Call(ctx context.Context, id int, method string, params any, out any) error {
	n, ok := NetworkFor(id)
	if !ok {
		return errors.New("unsupported network")
	}
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, err := http.NewRequestWithContext(ctx, "POST", n.RPC, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Croptop/1.0")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("network returned %s", resp.Status)
	}
	var result struct {
		Result json.RawMessage
		Error  *struct{ Message string }
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&result); err != nil {
		return err
	}
	if result.Error != nil {
		return errors.New(result.Error.Message)
	}
	if len(result.Result) == 0 {
		return errors.New("missing RPC result")
	}
	return json.Unmarshal(result.Result, out)
}
func topic(signature string) string {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(signature))
	return "0x" + hex.EncodeToString(h.Sum(nil))
}
func quantity(v string) (*big.Int, bool) {
	if !hexRE.MatchString(v) {
		return nil, false
	}
	n, ok := new(big.Int).SetString(v[2:], 16)
	return n, ok
}
func equalQuantity(a, b string) bool {
	x, ok := quantity(a)
	y, ok2 := quantity(b)
	return ok && ok2 && x.Cmp(y) == 0
}
func wordAddress(s string) (string, bool) {
	if len(s) != 64 || s[:24] != strings.Repeat("0", 24) {
		return "", false
	}
	a := "0x" + s[24:]
	return a, Address(a)
}

type Transaction struct{ Hash, From, To, Input, Value, Nonce, BlockHash string }
type Log struct {
	Address                          string
	Topics                           []string
	Data, BlockHash, TransactionHash string
	Removed                          bool
}
type Receipt struct {
	TransactionHash, BlockHash, BlockNumber, Status, To, From string
	Logs                                                      []Log
}

// Verify reads a finalized receipt from the pinned chain RPC. A browser-provided
// address, event, chain ID or success flag is never accepted as confirmation.
func Verify(ctx context.Context, rpc RPC, ch *Chain, hash string) (string, error) {
	if len(ch.Attempts) == 0 {
		return "", errors.New("record a signing intent first")
	}
	state, receipt, err := verifiedReceipt(ctx, rpc, ch.ID, Deployer, ch.Data, ch.Value, &ch.Attempts[len(ch.Attempts)-1], hash)
	if err != nil || state != "confirmed" {
		return state, err
	}
	var address, project string
	for _, l := range receipt.Logs {
		if !strings.EqualFold(l.Address, HookDeployer) || len(l.Topics) == 0 || !strings.EqualFold(l.Topics[0], topic("HookDeployed(uint256,address,address)")) {
			continue
		}
		if l.Removed || len(l.Topics) != 2 || !Hash(l.Topics[1]) || len(l.Data) != 130 || !hexRE.MatchString(l.Data) || !strings.EqualFold(l.BlockHash, receipt.BlockHash) || !strings.EqualFold(l.TransactionHash, hash) {
			return "", errors.New("invalid hook event")
		}
		hook, ok := wordAddress(l.Data[2:66])
		caller, ok2 := wordAddress(l.Data[66:])
		pid, _ := quantity(l.Topics[1])
		if !ok || !ok2 || !strings.EqualFold(caller, Deployer) || pid.Sign() == 0 || address != "" {
			return "", errors.New("ambiguous or invalid hook event")
		}
		address = hook
		project = l.Topics[1]
	}
	if address == "" {
		return "", errors.New("verified hook event not found")
	}
	var code, hookProject string
	if err := rpc(ctx, ch.ID, "eth_getCode", []any{address, receipt.BlockNumber}, &code); err != nil {
		return "", err
	}
	if len(code) <= 2 {
		return "", errors.New("hook has no code")
	}
	if err := rpc(ctx, ch.ID, "eth_call", []any{map[string]string{"to": address, "data": topic("projectId()")[:10]}, receipt.BlockNumber}, &hookProject); err != nil {
		return "", err
	}
	if !equalQuantity(hookProject, project) {
		return "", errors.New("hook project mismatch")
	}
	ch.Address = address
	p, _ := quantity(project)
	ch.ProjectID = p.String()
	return "confirmed", nil
}

func verifyTransaction(ctx context.Context, rpc RPC, id int, to, data, value string, a *Attempt, hash string) (string, error) {
	state, _, err := verifiedReceipt(ctx, rpc, id, to, data, value, a, hash)
	return state, err
}
func verifiedReceipt(ctx context.Context, rpc RPC, id int, to, data, value string, a *Attempt, hash string) (string, *Receipt, error) {
	if !Hash(hash) || a == nil {
		return "", nil, errors.New("record a signing intent first")
	}
	var chainID string
	if err := rpc(ctx, id, "eth_chainId", []any{}, &chainID); err != nil {
		return "", nil, err
	}
	if !equalQuantity(chainID, fmt.Sprintf("0x%x", id)) {
		return "", nil, errors.New("RPC chain mismatch")
	}
	var tx *Transaction
	if err := rpc(ctx, id, "eth_getTransactionByHash", []any{hash}, &tx); err != nil {
		return "", nil, err
	}
	if tx == nil {
		return "unknown", nil, nil
	}
	if !strings.EqualFold(tx.Hash, hash) || !strings.EqualFold(tx.From, a.From) || !equalQuantity(tx.Nonce, a.Nonce) {
		return "", nil, errors.New("transaction does not match wallet and nonce")
	}
	matches := strings.EqualFold(tx.To, to) && strings.EqualFold(tx.Input, data) && equalQuantity(tx.Value, value)
	var receipt *Receipt
	if err := rpc(ctx, id, "eth_getTransactionReceipt", []any{hash}, &receipt); err != nil {
		return "", nil, err
	}
	if receipt == nil {
		return "pending", nil, nil
	}
	if !Hash(receipt.BlockHash) || !strings.EqualFold(receipt.BlockHash, tx.BlockHash) || !strings.EqualFold(receipt.TransactionHash, hash) || !strings.EqualFold(receipt.From, tx.From) || !strings.EqualFold(receipt.To, tx.To) {
		return "", nil, errors.New("receipt transaction mismatch")
	}
	var block *struct{ Hash, Number string }
	if err := rpc(ctx, id, "eth_getBlockByNumber", []any{receipt.BlockNumber, false}, &block); err != nil {
		return "", nil, err
	}
	if block == nil || !strings.EqualFold(block.Hash, receipt.BlockHash) {
		return "pending", nil, nil
	}
	var finalized *struct{ Number string }
	if err := rpc(ctx, id, "eth_getBlockByNumber", []any{"finalized", false}, &finalized); err != nil {
		return "", nil, err
	}
	height, ok := quantity(receipt.BlockNumber)
	if !ok {
		return "", nil, errors.New("invalid receipt block")
	}
	if finalized == nil {
		return "pending", nil, nil
	}
	end, ok := quantity(finalized.Number)
	if !ok || end.Cmp(height) < 0 {
		return "pending", nil, nil
	}
	if !matches {
		return "replaced", nil, nil
	}
	if receipt.Status == "0x0" {
		return "reverted", nil, nil
	}
	if receipt.Status != "0x1" {
		return "", nil, errors.New("invalid receipt status")
	}

	return "confirmed", receipt, nil
}
