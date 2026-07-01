package contract

import (
	"bytes"
	"encoding/binary"
	"log"
	"encoding/json"
	"fmt"
	"math/rand"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
)

/* This file contains the base contract implementation that overrides the basic 'transfer' functionality */

// PluginConfig: the configuration of the contract
var ContractConfig = &PluginConfig{
	Name:                  "go_plugin_contract",
	Id:                    1,
	Version:               1,
	SupportedTransactions: []string{
		"send",
		"publish_thread",
		"tip_creator",
		"claim_tips",
		"set_supporter_threshold",
	},
	TransactionTypeUrls: []string{
		"type.googleapis.com/types.MessageSend",
		"type.googleapis.com/types.MsgPublishThread",
		"type.googleapis.com/types.MsgTipCreator",
		"type.googleapis.com/types.MsgClaimTips",
		"type.googleapis.com/types.MsgSetSupporterThreshold",
	},
	EventTypeUrls: nil,
}

// init sets FileDescriptorProtos after ensuring .pb.go files are initialized
func init() {
	// Explicitly initialize the proto files first to ensure File_*_proto are set
	file_account_proto_init()
	file_event_proto_init()
	file_plugin_proto_init()
	file_tx_proto_init()

	var fds [][]byte
	// Include google/protobuf/any.proto first as it's a dependency of event.proto and tx.proto
	for _, file := range []protoreflect.FileDescriptor{
		anypb.File_google_protobuf_any_proto,
		File_account_proto, File_event_proto, File_plugin_proto, File_tx_proto,
	} {
		fd, _ := proto.Marshal(protodesc.ToFileDescriptorProto(file))
		fds = append(fds, fd)
	}
	ContractConfig.FileDescriptorProtos = fds
}

// Contract() defines the smart contract that implements the extended logic of the nested chain
type Contract struct {
	Config        Config
	FSMConfig     *PluginFSMConfig // fsm configuration
	plugin        *Plugin          // plugin connection
	fsmId         uint64           // the id of the requesting fsm
	currentHeight uint64           // captured in BeginBlock, used by DeliverTx handlers
}

// Genesis() implements logic to import a json file to create the state at height 0 and export the state at any height
func (c *Contract) Genesis(_ *PluginGenesisRequest) *PluginGenesisResponse {
	return &PluginGenesisResponse{} // TODO map out original token holders
}

// BeginBlock() is code that is executed at the start of `applying` the block
func (c *Contract) BeginBlock(request *PluginBeginRequest) *PluginBeginResponse {
	c.currentHeight = request.Height
	return &PluginBeginResponse{}
}

// CheckTx() is code that is executed to statelessly validate a transaction
func (c *Contract) CheckTx(request *PluginCheckRequest) *PluginCheckResponse {
	// validate fee
	resp, err := c.plugin.StateRead(c, &PluginStateReadRequest{
		Keys: []*PluginKeyRead{
			{QueryId: rand.Uint64(), Key: KeyForFeeParams()},
		}})
	if err == nil {
		err = resp.Error
	}
	// handle error
	if err != nil {
		return &PluginCheckResponse{Error: err}
	}
	// convert bytes into fee parameters
	minFees := new(FeeParams)
	if err = Unmarshal(resp.Results[0].Entries[0].Value, minFees); err != nil {
		return &PluginCheckResponse{Error: err}
	}
	// check for the minimum fee
	if request.Tx.Fee < minFees.SendFee {
		return &PluginCheckResponse{Error: ErrTxFeeBelowStateLimit()}
	}
	// get the message
	msg, err := msgFromAny(request.Tx.Msg)
	if err != nil {
		return &PluginCheckResponse{Error: err}
	}
	// handle the message
	switch x := msg.(type) {
	case *MessageSend:
		return c.CheckMessageSend(x)
	case *MsgPublishThread:
		return c.CheckMsgPublishThread(x)
	case *MsgTipCreator:
		return c.CheckMsgTipCreator(x)
	case *MsgClaimTips:
		return c.CheckMsgClaimTips(x)
	case *MsgSetSupporterThreshold:
		return c.CheckMsgSetSupporterThreshold(x)
	default:
		return &PluginCheckResponse{Error: ErrInvalidMessageCast()}
	}
}

// DeliverTx() is code that is executed to apply a transaction
func (c *Contract) DeliverTx(request *PluginDeliverRequest) *PluginDeliverResponse {
	// get the message
	msg, err := msgFromAny(request.Tx.Msg)
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	// handle the message
	switch x := msg.(type) {
	case *MessageSend:
		return c.DeliverMessageSend(x, request.Tx.Fee)
	case *MsgPublishThread:
		return c.DeliverMsgPublishThread(x, request.Tx.Fee, c.currentHeight)
	case *MsgTipCreator:
		return c.DeliverMsgTipCreator(x, request.Tx.Fee, c.currentHeight)
	case *MsgClaimTips:
		return c.DeliverMsgClaimTips(x, request.Tx.Fee)
	case *MsgSetSupporterThreshold:
		return c.DeliverMsgSetSupporterThreshold(x, request.Tx.Fee)
	default:
		return &PluginDeliverResponse{Error: ErrInvalidMessageCast()}
	}
}

// EndBlock() is code that is executed at the end of 'applying' a block
func (c *Contract) EndBlock(_ *PluginEndRequest) *PluginEndResponse {
	return &PluginEndResponse{}
}

// CheckMessageSend() statelessly validates a 'send' message
func (c *Contract) CheckMessageSend(msg *MessageSend) *PluginCheckResponse {
	// check sender address
	if len(msg.FromAddress) != 20 {
		return &PluginCheckResponse{Error: ErrInvalidAddress()}
	}
	// check recipient address
	if len(msg.ToAddress) != 20 {
		return &PluginCheckResponse{Error: ErrInvalidAddress()}
	}
	// check amount
	if msg.Amount == 0 {
		return &PluginCheckResponse{Error: ErrInvalidAmount()}
	}
	// return the authorized signers
	return &PluginCheckResponse{Recipient: msg.ToAddress, AuthorizedSigners: [][]byte{msg.FromAddress}}
}

// DeliverMessageSend() handles a 'send' message
func (c *Contract) DeliverMessageSend(msg *MessageSend, fee uint64) *PluginDeliverResponse {
	log.Printf("DeliverMessageSend called: from=%x to=%x amount=%d fee=%d", msg.FromAddress, msg.ToAddress, msg.Amount, fee)
	var (
		fromKey, toKey, feePoolKey         []byte
		fromBytes, toBytes, feePoolBytes   []byte
		fromQueryId, toQueryId, feeQueryId = rand.Uint64(), rand.Uint64(), rand.Uint64()
		from, to, feePool                  = new(Account), new(Account), new(Pool)
	)
	// calculate the from key and to key
	fromKey, toKey, feePoolKey = KeyForAccount(msg.FromAddress), KeyForAccount(msg.ToAddress), KeyForFeePool(c.Config.ChainId)
	log.Printf("Keys: fromKey=%x toKey=%x feePoolKey=%x", fromKey, toKey, feePoolKey)
	// get the from and to account
	response, err := c.plugin.StateRead(c, &PluginStateReadRequest{
		Keys: []*PluginKeyRead{
			{QueryId: feeQueryId, Key: feePoolKey},
			{QueryId: fromQueryId, Key: fromKey},
			{QueryId: toQueryId, Key: toKey},
		}})
	// check for internal error
	if err != nil {
		log.Printf("StateRead error: %v", err)
		return &PluginDeliverResponse{Error: err}
	}
	// ensure no error fsm error
	if response.Error != nil {
		log.Printf("StateRead FSM error: %v", response.Error)
		return &PluginDeliverResponse{Error: response.Error}
	}
	log.Printf("StateRead returned %d results", len(response.Results))
	// get the from bytes and to bytes
	for _, resp := range response.Results {
		log.Printf("Result QueryId=%d Entries=%d", resp.QueryId, len(resp.Entries))
		if len(resp.Entries) == 0 {
			log.Printf("WARNING: No entries for QueryId=%d", resp.QueryId)
			continue
		}
		switch resp.QueryId {
		case fromQueryId:
			fromBytes = resp.Entries[0].Value
			log.Printf("fromBytes len=%d", len(fromBytes))
		case toQueryId:
			toBytes = resp.Entries[0].Value
			log.Printf("toBytes len=%d", len(toBytes))
		case feeQueryId:
			feePoolBytes = resp.Entries[0].Value
			log.Printf("feePoolBytes len=%d", len(feePoolBytes))
		}
	}
	// add fee to 'amount to deduct'
	amountToDeduct := msg.Amount + fee
	// convert the bytes to account structures
	if err = Unmarshal(fromBytes, from); err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if err = Unmarshal(toBytes, to); err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if err = Unmarshal(feePoolBytes, feePool); err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	log.Printf("from.Amount=%d to.Amount=%d feePool.Amount=%d", from.Amount, to.Amount, feePool.Amount)
	// if the account amount is less than the amount to subtract; return insufficient funds
	if from.Amount < amountToDeduct {
		log.Printf("ERROR: Insufficient funds: from.Amount=%d amountToDeduct=%d", from.Amount, amountToDeduct)
		return &PluginDeliverResponse{Error: ErrInsufficientFunds()}
	}
	// for self-transfer, use same account data
	if bytes.Equal(fromKey, toKey) {
		to = from
	}
	// subtract from sender
	from.Amount -= amountToDeduct
	// add the fee to the 'fee pool'
	feePool.Amount += fee
	// add to recipient
	to.Amount += msg.Amount
	log.Printf("AFTER: from.Amount=%d to.Amount=%d feePool.Amount=%d", from.Amount, to.Amount, feePool.Amount)
	// convert the accounts to bytes
	fromBytes, err = Marshal(from)
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	toBytes, err = Marshal(to)
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	feePoolBytes, err = Marshal(feePool)
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	// execute writes to the database
	var resp *PluginStateWriteResponse
	// if the from account is drained - delete the from account
	if from.Amount == 0 {
		resp, err = c.plugin.StateWrite(c, &PluginStateWriteRequest{
			Sets: []*PluginSetOp{
				{Key: feePoolKey, Value: feePoolBytes},
				{Key: toKey, Value: toBytes},
			},
			Deletes: []*PluginDeleteOp{{Key: fromKey}},
		})
	} else {
		resp, err = c.plugin.StateWrite(c, &PluginStateWriteRequest{
			Sets: []*PluginSetOp{
				{Key: feePoolKey, Value: feePoolBytes},
				{Key: toKey, Value: toBytes},
				{Key: fromKey, Value: fromBytes},
			},
		})
	}
	if err != nil {
		log.Printf("StateWrite internal error: %v", err)
		return &PluginDeliverResponse{Error: err}
	}
	if resp.Error != nil {
		log.Printf("StateWrite FSM error: %v", resp.Error)
		return &PluginDeliverResponse{Error: resp.Error}
	}
	log.Printf("StateWrite SUCCESS!")
	return &PluginDeliverResponse{}
}

var (
	accountPrefix = []byte{1} // store key prefix for accounts
	poolPrefix    = []byte{2} // store key prefix for pools
	paramsPrefix  = []byte{7} // store key prefix for governance parameters
)

// KeyForAccount() returns the state database key for an account
func KeyForAccount(addr []byte) []byte {
	return JoinLenPrefix(accountPrefix, addr)
}

// KeyForFeeParams() returns the state database key for governance controlled 'fee parameters'
func KeyForFeeParams() []byte {
	return JoinLenPrefix(paramsPrefix, []byte("/f/"))
}

// KeyForFeeParams() returns the state database key for governance controlled 'fee parameters'
func KeyForFeePool(chainId uint64) []byte {
	return JoinLenPrefix(poolPrefix, formatUint64(chainId))
}

func formatUint64(u uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, u)
	return b
}

// --- Lore state key prefixes (must start at 0x10+ per plugin rules) ---
var (
creatorPrefix   = []byte{0x10}
supporterPrefix = []byte{0x11}
)

// KeyForCreator returns the state key for a creator address
func KeyForCreator(addr []byte) []byte {
return JoinLenPrefix(creatorPrefix, addr)
}

// KeyForSupporter returns the state key for a tipper/creator relationship
func KeyForSupporter(creator, tipper []byte) []byte {
return JoinLenPrefix(supporterPrefix, JoinLenPrefix(creator, tipper))
}

// --- Lore state types ---
type CreatorState struct {
Address           []byte `json:"address"`
PendingBalance    uint64 `json:"pending_balance"`
TotalEarned       uint64 `json:"total_earned"`
SupporterThreshold uint64 `json:"supporter_threshold"`
WeekTips          uint64 `json:"week_tips"`
WeekStartBlock    uint64 `json:"week_start_block"`
ThreadCount       uint32 `json:"thread_count"`
}

type SupporterRecord struct {
Creator        []byte `json:"creator"`
Tipper         []byte `json:"tipper"`
CumulativeTips uint64 `json:"cumulative_tips"`
IsSupporter    bool   `json:"is_supporter"`
}

const weekBlocks = uint64(50400)

// --- CheckTx handlers ---

func (c *Contract) CheckMsgPublishThread(msg *MsgPublishThread) *PluginCheckResponse {
if len(msg.Creator) != 20 {
return &PluginCheckResponse{Error: ErrInvalidAddress()}
}
if len(msg.ContentHash) != 32 {
return &PluginCheckResponse{Error: ErrInvalidAmount()}
}
return &PluginCheckResponse{AuthorizedSigners: [][]byte{msg.Creator}}
}

func (c *Contract) CheckMsgTipCreator(msg *MsgTipCreator) *PluginCheckResponse {
if len(msg.Tipper) != 20 {
return &PluginCheckResponse{Error: ErrInvalidAddress()}
}
if len(msg.Creator) != 20 {
return &PluginCheckResponse{Error: ErrInvalidAddress()}
}
if msg.Amount == 0 {
return &PluginCheckResponse{Error: ErrInvalidAmount()}
}
if bytes.Equal(msg.Tipper, msg.Creator) {
return &PluginCheckResponse{Error: ErrInvalidAddress()}
}
return &PluginCheckResponse{AuthorizedSigners: [][]byte{msg.Tipper}}
}

func (c *Contract) CheckMsgClaimTips(msg *MsgClaimTips) *PluginCheckResponse {
if len(msg.Creator) != 20 {
return &PluginCheckResponse{Error: ErrInvalidAddress()}
}
return &PluginCheckResponse{AuthorizedSigners: [][]byte{msg.Creator}}
}

func (c *Contract) CheckMsgSetSupporterThreshold(msg *MsgSetSupporterThreshold) *PluginCheckResponse {
if len(msg.Creator) != 20 {
return &PluginCheckResponse{Error: ErrInvalidAddress()}
}
return &PluginCheckResponse{AuthorizedSigners: [][]byte{msg.Creator}}
}

// --- DeliverTx handlers ---

func (c *Contract) DeliverMsgPublishThread(msg *MsgPublishThread, fee uint64, height uint64) *PluginDeliverResponse {
qid, feeQid := rand.Uint64(), rand.Uint64()
resp, err := c.plugin.StateRead(c, &PluginStateReadRequest{
Keys: []*PluginKeyRead{
{QueryId: qid, Key: KeyForCreator(msg.Creator)},
{QueryId: feeQid, Key: KeyForFeePool(c.Config.ChainId)},
},
})
if err != nil {
return &PluginDeliverResponse{Error: err}
}
if resp.Error != nil {
return &PluginDeliverResponse{Error: resp.Error}
}
cs := &CreatorState{Address: msg.Creator, WeekStartBlock: height}
feePool := new(Pool)
for _, r := range resp.Results {
if len(r.Entries) == 0 || len(r.Entries[0].Value) == 0 {
continue
}
switch r.QueryId {
case qid:
if e := UnmarshalJSON(r.Entries[0].Value, cs); e != nil {
return &PluginDeliverResponse{Error: e}
}
case feeQid:
if e := Unmarshal(r.Entries[0].Value, feePool); e != nil {
return &PluginDeliverResponse{Error: e}
}
}
}
cs.ThreadCount++
feePool.Amount += fee
bz, e := MarshalJSON(cs)
if e != nil {
return &PluginDeliverResponse{Error: e}
}
feePoolBz, e := Marshal(feePool)
if e != nil {
return &PluginDeliverResponse{Error: e}
}
writeResp, err := c.plugin.StateWrite(c, &PluginStateWriteRequest{
Sets: []*PluginSetOp{
{Key: KeyForCreator(msg.Creator), Value: bz},
{Key: KeyForFeePool(c.Config.ChainId), Value: feePoolBz},
},
})
if err != nil {
return &PluginDeliverResponse{Error: err}
}
if writeResp.Error != nil {
return &PluginDeliverResponse{Error: writeResp.Error}
}
return &PluginDeliverResponse{}
}

func (c *Contract) DeliverMsgTipCreator(msg *MsgTipCreator, fee uint64, height uint64) *PluginDeliverResponse {
tipperQid, creatorQid, supQid, feeQid := rand.Uint64(), rand.Uint64(), rand.Uint64(), rand.Uint64()
resp, err := c.plugin.StateRead(c, &PluginStateReadRequest{
Keys: []*PluginKeyRead{
{QueryId: tipperQid, Key: KeyForAccount(msg.Tipper)},
{QueryId: creatorQid, Key: KeyForCreator(msg.Creator)},
{QueryId: supQid, Key: KeyForSupporter(msg.Creator, msg.Tipper)},
{QueryId: feeQid, Key: KeyForFeePool(c.Config.ChainId)},
},
})
if err != nil {
return &PluginDeliverResponse{Error: err}
}
if resp.Error != nil {
return &PluginDeliverResponse{Error: resp.Error}
}
tipper := new(Account)
cs := &CreatorState{Address: msg.Creator, WeekStartBlock: height}
sr := &SupporterRecord{Creator: msg.Creator, Tipper: msg.Tipper}
feePool := new(Pool)
for _, r := range resp.Results {
if len(r.Entries) == 0 || len(r.Entries[0].Value) == 0 {
continue
}
switch r.QueryId {
case tipperQid:
Unmarshal(r.Entries[0].Value, tipper)
case creatorQid:
UnmarshalJSON(r.Entries[0].Value, cs)
case supQid:
UnmarshalJSON(r.Entries[0].Value, sr)
case feeQid:
Unmarshal(r.Entries[0].Value, feePool)
}
}
total := msg.Amount + fee
if tipper.Amount < total {
return &PluginDeliverResponse{Error: ErrInsufficientFunds()}
}
tipper.Amount -= total
feePool.Amount += fee
cs.PendingBalance += msg.Amount
cs.TotalEarned += msg.Amount
cs.WeekTips += msg.Amount
sr.CumulativeTips += msg.Amount
if cs.SupporterThreshold > 0 && sr.CumulativeTips >= cs.SupporterThreshold {
sr.IsSupporter = true
}
tipperBz, e := Marshal(tipper)
if e != nil {
return &PluginDeliverResponse{Error: e}
}
csBz, e := MarshalJSON(cs)
if e != nil {
return &PluginDeliverResponse{Error: e}
}
srBz, e := MarshalJSON(sr)
if e != nil {
return &PluginDeliverResponse{Error: e}
}
feePoolBz, e := Marshal(feePool)
if e != nil {
return &PluginDeliverResponse{Error: e}
}
writeResp, err := c.plugin.StateWrite(c, &PluginStateWriteRequest{
Sets: []*PluginSetOp{
{Key: KeyForAccount(msg.Tipper), Value: tipperBz},
{Key: KeyForCreator(msg.Creator), Value: csBz},
{Key: KeyForSupporter(msg.Creator, msg.Tipper), Value: srBz},
{Key: KeyForFeePool(c.Config.ChainId), Value: feePoolBz},
},
})
if err != nil {
return &PluginDeliverResponse{Error: err}
}
if writeResp.Error != nil {
return &PluginDeliverResponse{Error: writeResp.Error}
}
return &PluginDeliverResponse{}
}

func (c *Contract) DeliverMsgClaimTips(msg *MsgClaimTips, fee uint64) *PluginDeliverResponse {
csQid, accQid, feeQid := rand.Uint64(), rand.Uint64(), rand.Uint64()
resp, err := c.plugin.StateRead(c, &PluginStateReadRequest{
Keys: []*PluginKeyRead{
{QueryId: csQid, Key: KeyForCreator(msg.Creator)},
{QueryId: accQid, Key: KeyForAccount(msg.Creator)},
{QueryId: feeQid, Key: KeyForFeePool(c.Config.ChainId)},
},
})
if err != nil {
return &PluginDeliverResponse{Error: err}
}
if resp.Error != nil {
return &PluginDeliverResponse{Error: resp.Error}
}
cs := &CreatorState{Address: msg.Creator}
acc := new(Account)
feePool := new(Pool)
for _, r := range resp.Results {
if len(r.Entries) == 0 || len(r.Entries[0].Value) == 0 {
continue
}
switch r.QueryId {
case csQid:
if e := UnmarshalJSON(r.Entries[0].Value, cs); e != nil {
return &PluginDeliverResponse{Error: e}
}
case accQid:
Unmarshal(r.Entries[0].Value, acc)
case feeQid:
Unmarshal(r.Entries[0].Value, feePool)
}
}
if cs.PendingBalance == 0 {
return &PluginDeliverResponse{Error: ErrInvalidAmount()}
}
if acc.Amount < fee {
return &PluginDeliverResponse{Error: ErrInsufficientFunds()}
}
acc.Amount += cs.PendingBalance
acc.Amount -= fee
feePool.Amount += fee
cs.PendingBalance = 0
accBz, e := Marshal(acc)
if e != nil {
return &PluginDeliverResponse{Error: e}
}
csBz, e := MarshalJSON(cs)
if e != nil {
return &PluginDeliverResponse{Error: e}
}
feePoolBz, e := Marshal(feePool)
if e != nil {
return &PluginDeliverResponse{Error: e}
}
writeResp, err := c.plugin.StateWrite(c, &PluginStateWriteRequest{
Sets: []*PluginSetOp{
{Key: KeyForAccount(msg.Creator), Value: accBz},
{Key: KeyForCreator(msg.Creator), Value: csBz},
{Key: KeyForFeePool(c.Config.ChainId), Value: feePoolBz},
},
})
if err != nil {
return &PluginDeliverResponse{Error: err}
}
if writeResp.Error != nil {
return &PluginDeliverResponse{Error: writeResp.Error}
}
return &PluginDeliverResponse{}
}

func (c *Contract) DeliverMsgSetSupporterThreshold(msg *MsgSetSupporterThreshold, fee uint64) *PluginDeliverResponse {
qid, feeQid := rand.Uint64(), rand.Uint64()
resp, err := c.plugin.StateRead(c, &PluginStateReadRequest{
Keys: []*PluginKeyRead{
{QueryId: qid, Key: KeyForCreator(msg.Creator)},
{QueryId: feeQid, Key: KeyForFeePool(c.Config.ChainId)},
},
})
if err != nil {
return &PluginDeliverResponse{Error: err}
}
if resp.Error != nil {
return &PluginDeliverResponse{Error: resp.Error}
}
cs := &CreatorState{Address: msg.Creator}
feePool := new(Pool)
for _, r := range resp.Results {
if len(r.Entries) == 0 || len(r.Entries[0].Value) == 0 {
continue
}
switch r.QueryId {
case qid:
if e := UnmarshalJSON(r.Entries[0].Value, cs); e != nil {
return &PluginDeliverResponse{Error: e}
}
case feeQid:
Unmarshal(r.Entries[0].Value, feePool)
}
}
cs.SupporterThreshold = msg.ThresholdUcnpy
feePool.Amount += fee
bz, e := MarshalJSON(cs)
if e != nil {
return &PluginDeliverResponse{Error: e}
}
feePoolBz, e := Marshal(feePool)
if e != nil {
return &PluginDeliverResponse{Error: e}
}
writeResp, err := c.plugin.StateWrite(c, &PluginStateWriteRequest{
Sets: []*PluginSetOp{
{Key: KeyForCreator(msg.Creator), Value: bz},
{Key: KeyForFeePool(c.Config.ChainId), Value: feePoolBz},
},
})
if err != nil {
return &PluginDeliverResponse{Error: err}
}
if writeResp.Error != nil {
return &PluginDeliverResponse{Error: writeResp.Error}
}
return &PluginDeliverResponse{}
}

// MarshalJSON serializes a plain Go struct to JSON bytes
func MarshalJSON(v any) ([]byte, *PluginError) {
bz, err := json.Marshal(v)
if err != nil {
return nil, ErrMarshal(err)
}
return bz, nil
}

// UnmarshalJSON deserializes JSON bytes into a plain Go struct
func UnmarshalJSON(bz []byte, v any) *PluginError {
if err := json.Unmarshal(bz, v); err != nil {
return ErrUnmarshal(err)
}
return nil
}

// msgFromAny() manually resolves plugin message types without the global proto registry
func msgFromAny(a *anypb.Any) (proto.Message, *PluginError) {
if a == nil {
return nil, ErrFromAny(fmt.Errorf("nil any"))
}
switch a.TypeUrl {
case "type.googleapis.com/types.MessageSend":
var msg MessageSend
if err := proto.Unmarshal(a.Value, &msg); err != nil {
return nil, ErrFromAny(err)
}
return &msg, nil
case "type.googleapis.com/types.MsgPublishThread":
var msg MsgPublishThread
if err := proto.Unmarshal(a.Value, &msg); err != nil {
return nil, ErrFromAny(err)
}
return &msg, nil
case "type.googleapis.com/types.MsgTipCreator":
var msg MsgTipCreator
if err := proto.Unmarshal(a.Value, &msg); err != nil {
return nil, ErrFromAny(err)
}
return &msg, nil
case "type.googleapis.com/types.MsgClaimTips":
var msg MsgClaimTips
if err := proto.Unmarshal(a.Value, &msg); err != nil {
return nil, ErrFromAny(err)
}
return &msg, nil
case "type.googleapis.com/types.MsgSetSupporterThreshold":
var msg MsgSetSupporterThreshold
if err := proto.Unmarshal(a.Value, &msg); err != nil {
return nil, ErrFromAny(err)
}
return &msg, nil
default:
return nil, ErrFromAny(fmt.Errorf("unknown type url: %s", a.TypeUrl))
}
}
