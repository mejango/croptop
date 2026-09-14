package shop

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"regexp"
	"strings"
)

// Deployment addresses and permission ID from the v6 contracts in jb/v6/evm.
const Publisher = "0xcbc84cf9b0293efe3ac7dd1bea128a404f2e6a1c"
const Permissions = "0xf92ac1ab5a00033e35a3975739124f61928c36b0"
const RevnetOwner = "0x2ba4705ad0332cdfb299b452068438bcba3faaf3"
const AdjustTiers = 24

type PostingCriteria struct {
	MinimumPrice        string   `json:"minimumPrice"`
	MinimumTotalSupply  uint32   `json:"minimumTotalSupply"`
	MaximumTotalSupply  uint32   `json:"maximumTotalSupply"`
	MaximumSplitPercent uint32   `json:"maximumSplitPercent"`
	AllowedAddresses    []string `json:"allowedAddresses"`
}
type ConnectionState struct {
	Owner            string          `json:"owner"`
	ProjectID        string          `json:"projectId"`
	Currency         uint32          `json:"currency"`
	Decimals         uint8           `json:"decimals"`
	PublisherGranted bool            `json:"publisherGranted"`
	CanConfigure     bool            `json:"canConfigure"`
	CanGrant         bool            `json:"canGrant"`
	Permissions      string          `json:"permissions"`
	Criteria         PostingCriteria `json:"criteria"`
}
type SetupTransaction struct {
	To      string   `json:"to"`
	Data    string   `json:"data"`
	Kind    string   `json:"kind"`
	Attempt *Attempt `json:"attempt,omitempty"`
}
type Connection struct {
	PlanState   *ConnectionState  `json:"planState,omitempty"`
	ChainID     int               `json:"chainId"`
	Hook        string            `json:"hook"`
	Category    uint32            `json:"category"`
	Price       string            `json:"price,omitempty"`
	Expected    *PostingCriteria  `json:"expected,omitempty"`
	State       *ConnectionState  `json:"state,omitempty"`
	Transaction *SetupTransaction `json:"transaction,omitempty"`
	Completed   bool              `json:"completed"`
}

func abiUint(n *big.Int) string { return fmt.Sprintf("%064x", n) }
func abiN(n uint64) string      { return fmt.Sprintf("%064x", n) }
func abiAddress(a string) string {
	return strings.Repeat("0", 24) + strings.ToLower(strings.TrimPrefix(a, "0x"))
}
func abiCall(signature string, words ...string) string {
	return topic(signature)[:10] + strings.Join(words, "")
}
func rpcWords(ctx context.Context, rpc RPC, chain int, to, data, block string) ([]string, error) {
	var result string
	if err := rpc(ctx, chain, "eth_call", []any{map[string]string{"to": to, "data": data}, block}, &result); err != nil {
		return nil, err
	}
	if len(result) < 66 || (len(result)-2)%64 != 0 || !hexRE.MatchString(result) {
		return nil, errors.New("invalid contract response")
	}
	var words []string
	for i := 2; i < len(result); i += 64 {
		words = append(words, result[i:i+64])
	}
	return words, nil
}
func wordN(s string) *big.Int { n, _ := new(big.Int).SetString(s, 16); return n }
func checkedUint(s string, bits int) (uint64, error) {
	n := wordN(s)
	if n == nil || n.BitLen() > bits {
		return 0, errors.New("invalid contract number")
	}
	return n.Uint64(), nil
}
func readAddress(ctx context.Context, rpc RPC, c *Connection, to, signature, block string) (string, error) {
	w, e := rpcWords(ctx, rpc, c.ChainID, to, abiCall(signature), block)
	if e != nil {
		return "", e
	}
	a, ok := wordAddress(w[0])
	if !ok {
		return "", errors.New("invalid contract address")
	}
	return strings.ToLower(a), nil
}
func ReadConnection(ctx context.Context, rpc RPC, c *Connection, from, block string) (*ConnectionState, error) {
	if _, ok := NetworkFor(c.ChainID); !ok || !Address(c.Hook) || c.Category > 16777215 {
		return nil, errors.New("invalid shop or category")
	}
	var chain string
	if e := rpc(ctx, c.ChainID, "eth_chainId", []any{}, &chain); e != nil {
		return nil, e
	}
	if !equalQuantity(chain, fmt.Sprintf("0x%x", c.ChainID)) {
		return nil, errors.New("RPC chain mismatch")
	}
	for _, a := range []string{c.Hook, Publisher, Permissions} {
		var code string
		if e := rpc(ctx, c.ChainID, "eth_getCode", []any{a, block}, &code); e != nil {
			return nil, e
		}
		if len(code) <= 2 {
			return nil, errors.New("v6 shop contracts are unavailable on this network")
		}
	}
	for _, a := range []string{c.Hook, Publisher} {
		p, e := readAddress(ctx, rpc, c, a, "PERMISSIONS()", block)
		if e != nil || p != Permissions {
			return nil, errors.New("this shop does not use the supported v6 permissions contract")
		}
	}
	owner, e := readAddress(ctx, rpc, c, c.Hook, "owner()", block)
	if e != nil {
		return nil, e
	}
	w, e := rpcWords(ctx, rpc, c.ChainID, c.Hook, abiCall("projectId()"), block)
	if e != nil {
		return nil, e
	}
	pid := wordN(w[0])
	if pid.Sign() == 0 || pid.BitLen() > 64 {
		return nil, errors.New("shop has no project")
	}
	w, e = rpcWords(ctx, rpc, c.ChainID, c.Hook, abiCall("pricingContext()"), block)
	if e != nil || len(w) != 2 {
		return nil, errors.New("shop pricing context unavailable")
	}
	currency, e := checkedUint(w[0], 32)
	if e != nil {
		return nil, e
	}
	decimals, e := checkedUint(w[1], 8)
	if e != nil || decimals > 18 {
		return nil, errors.New("unsupported shop price precision")
	}
	s := &ConnectionState{Owner: owner, ProjectID: pid.String(), Currency: uint32(currency), Decimals: uint8(decimals)}
	has := func(operator string, id uint64) (bool, error) {
		if strings.EqualFold(operator, owner) {
			return true, nil
		}
		if !Address(operator) {
			return false, nil
		}
		w, e := rpcWords(ctx, rpc, c.ChainID, Permissions, abiCall("hasPermission(address,address,uint256,uint256,bool,bool)", abiAddress(operator), abiAddress(owner), abiUint(pid), abiN(id), abiN(1), abiN(1)), block)
		if e != nil {
			return false, e
		}
		v, e := checkedUint(w[0], 1)
		return v == 1, e
	}
	s.PublisherGranted, e = has(Publisher, AdjustTiers)
	if e != nil {
		return nil, e
	}
	s.CanConfigure, e = has(from, AdjustTiers)
	if e != nil {
		return nil, e
	}
	s.CanGrant, e = has(from, 1)
	if e != nil {
		return nil, e
	}
	w, e = rpcWords(ctx, rpc, c.ChainID, Permissions, abiCall("permissionsOf(address,address,uint256)", abiAddress(Publisher), abiAddress(owner), abiUint(pid)), block)
	if e != nil {
		return nil, e
	}
	s.Permissions = "0x" + w[0]
	w, e = rpcWords(ctx, rpc, c.ChainID, Publisher, abiCall("allowanceFor(address,uint256)", abiAddress(c.Hook), abiN(uint64(c.Category))), block)
	if e != nil || len(w) < 6 {
		return nil, errors.New("shop posting rules unavailable")
	}
	price := wordN(w[0])
	if price.BitLen() > 104 {
		return nil, errors.New("invalid minimum price")
	}
	s.Criteria.MinimumPrice = price.String()
	min, e := checkedUint(w[1], 32)
	if e != nil {
		return nil, e
	}
	max, e := checkedUint(w[2], 32)
	if e != nil {
		return nil, e
	}
	split, e := checkedUint(w[3], 32)
	if e != nil {
		return nil, e
	}
	offset, e := checkedUint(w[4], 32)
	if e != nil || offset != 160 {
		return nil, errors.New("invalid posting allowlist offset")
	}
	count, e := checkedUint(w[5], 32)
	if e != nil || count > 1024 || len(w) != 6+int(count) {
		return nil, errors.New("invalid posting allowlist")
	}
	s.Criteria.MinimumTotalSupply = uint32(min)
	s.Criteria.MaximumTotalSupply = uint32(max)
	s.Criteria.MaximumSplitPercent = uint32(split)
	s.Criteria.AllowedAddresses = []string{}
	for _, v := range w[6:] {
		if v[:24] != strings.Repeat("0", 24) {
			return nil, errors.New("invalid allowed poster")
		}
		s.Criteria.AllowedAddresses = append(s.Criteria.AllowedAddresses, "0x"+strings.ToLower(v[24:]))
	}
	return s, nil
}
func PriceUnits(price string, decimals uint8) (string, error) {
	if !regexp.MustCompile(`^(0|[1-9][0-9]{0,31})(\.[0-9]{1,18})?$`).MatchString(price) {
		return "", errors.New("enter a valid minimum price")
	}
	parts := strings.Split(price, ".")
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > int(decimals) {
		return "", fmt.Errorf("this shop supports %d price decimals", decimals)
	}
	n, ok := new(big.Int).SetString(parts[0]+fraction+strings.Repeat("0", int(decimals)-len(fraction)), 10)
	if !ok || n.BitLen() > 104 {
		return "", errors.New("minimum price exceeds the contract limit")
	}
	return n.String(), nil
}
func criteriaData(c *Connection, p PostingCriteria) string {
	n, _ := new(big.Int).SetString(p.MinimumPrice, 10)
	words := []string{abiN(32), abiN(1), abiN(32), abiAddress(c.Hook), abiN(uint64(c.Category)), abiUint(n), abiN(uint64(p.MinimumTotalSupply)), abiN(uint64(p.MaximumTotalSupply)), abiN(uint64(p.MaximumSplitPercent)), abiN(224), abiN(uint64(len(p.AllowedAddresses)))}
	for _, a := range p.AllowedAddresses {
		words = append(words, abiAddress(a))
	}
	return abiCall("configurePostingCriteriaFor((address,uint24,uint104,uint32,uint32,uint32,address[])[])", words...)
}
func NextSetup(c *Connection, s *ConnectionState, from string) (*SetupTransaction, error) {
	if c.Expected == nil {
		return nil, errors.New("review the posting rules first")
	}
	if !s.PublisherGranted {
		if !s.CanGrant {
			if strings.EqualFold(s.Owner, RevnetOwner) {
				return nil, errors.New("this revnet did not grant Croptop posting permission at creation; its operator cannot add that permission through this setup")
			}
			return nil, errors.New("connect the shop owner or a wallet with permission to manage this project's permissions")
		}
		packed, _ := quantity(s.Permissions)
		packed.SetBit(packed, AdjustTiers, 1)
		ids := []string{}
		for i := 1; i < 256; i++ {
			if packed.Bit(i) == 1 {
				ids = append(ids, abiN(uint64(i)))
			}
		}
		pid, _ := new(big.Int).SetString(s.ProjectID, 10)
		words := []string{abiAddress(s.Owner), abiN(64), abiAddress(Publisher), abiUint(pid), abiN(96), abiN(uint64(len(ids)))}
		words = append(words, ids...)
		return &SetupTransaction{To: Permissions, Kind: "permission", Data: abiCall("setPermissionsFor(address,(address,uint64,uint8[]))", words...)}, nil
	}
	if !reflect.DeepEqual(s.Criteria, *c.Expected) {
		if !s.CanConfigure {
			return nil, errors.New("connect the shop owner or an operator allowed to manage its NFT tiers")
		}
		return &SetupTransaction{To: Publisher, Kind: "criteria", Data: criteriaData(c, *c.Expected)}, nil
	}
	return nil, nil
}
func PlanConnection(c *Connection, s *ConnectionState, from, price string) error {
	units, e := PriceUnits(price, s.Decimals)
	if e != nil {
		return e
	}
	if c.Expected != nil && c.Transaction != nil && c.Transaction.Attempt != nil && c.Transaction.Attempt.State != "confirmed" {
		return errors.New("check the saved transaction before changing posting rules")
	}
	expected := s.Criteria
	if expected.MinimumTotalSupply == 0 {
		expected = PostingCriteria{MinimumTotalSupply: 1, MaximumSplitPercent: 100000000, AllowedAddresses: []string{}}
	}
	expected.MinimumPrice = units
	if !s.CanConfigure && !reflect.DeepEqual(expected, s.Criteria) {
		return errors.New("this wallet cannot configure the shop's posting rules")
	}
	c.Price = price
	c.Expected = &expected
	c.State = s
	c.PlanState = s
	c.Transaction, e = NextSetup(c, s, from)
	return e
}

// VerifySetup confirms the exact saved transaction through a finalized chain receipt.
func VerifySetup(ctx context.Context, rpc RPC, c *Connection, hash string) (string, error) {
	t := c.Transaction
	if t == nil || t.Attempt == nil {
		return "", errors.New("record the wallet intent first")
	}
	return verifyTransaction(ctx, rpc, c.ChainID, t.To, t.Data, "0x0", t.Attempt, hash)
}
