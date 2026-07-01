package main

import (
"encoding/base64"
"encoding/hex"
"encoding/json"
"fmt"
"testing"
"time"

"github.com/canopy-network/go-plugin/tutorial/contract"
"github.com/canopy-network/go-plugin/tutorial/crypto"
"google.golang.org/protobuf/proto"
"google.golang.org/protobuf/types/known/anypb"
)

// TestLoreProtocol exercises the full Lore tx flow against a live local node:
// publish_thread -> tip_creator -> set_supporter_threshold -> claim_tips
func TestLoreProtocol(t *testing.T) {
queryRPCURL := "http://localhost:50002"
adminRPCURL := "http://localhost:50003"
networkID := uint64(1)
chainID := uint64(1)
testPassword := "testpassword123"
fee := uint64(10000)

suffix := randomSuffix()

// Validator account: pre-funded in genesis, used to fund test accounts via plain `send`
// (this chain does not register a `faucet` tx type, only send + the 4 Lore types).
validatorAddr := "133cd9a5d78de6e9ca2b9bd0ecf1f31f57c59c38"
validatorPassword := "test1234"
validatorKey, err := keystoreGetKey(adminRPCURL, validatorAddr, validatorPassword)
if err != nil {
t.Fatalf("Failed to get validator key: %v", err)
}

// Step 1: create creator and tipper accounts
t.Log("Step 1: Creating creator and tipper accounts...")
creatorAddr, err := keystoreNewKey(adminRPCURL, "lore_creator_"+suffix, testPassword)
if err != nil {
t.Fatalf("Failed to create creator account: %v", err)
}
t.Logf("Created creator: %s", creatorAddr)

tipperAddr, err := keystoreNewKey(adminRPCURL, "lore_tipper_"+suffix, testPassword)
if err != nil {
t.Fatalf("Failed to create tipper account: %v", err)
}
t.Logf("Created tipper: %s", tipperAddr)

creatorKey, err := keystoreGetKey(adminRPCURL, creatorAddr, testPassword)
if err != nil {
t.Fatalf("Failed to get creator key: %v", err)
}
tipperKey, err := keystoreGetKey(adminRPCURL, tipperAddr, testPassword)
if err != nil {
t.Fatalf("Failed to get tipper key: %v", err)
}

// Step 2: fund both via a plain send from the validator
t.Log("Step 2: Funding creator and tipper from validator via send...")
height, _ := getHeight(queryRPCURL)

creatorFundTx, err := sendSendTx(queryRPCURL, validatorKey, validatorAddr, creatorAddr, uint64(1000000000), fee, networkID, chainID, height)
if err != nil {
t.Fatalf("Failed to fund creator: %v", err)
}
if ok, err := waitForTxInclusion(queryRPCURL, validatorAddr, creatorFundTx, 60*time.Second); err != nil || !ok {
t.Fatalf("Creator funding tx not included: ok=%v err=%v", ok, err)
}
t.Log("Creator funded")

height, _ = getHeight(queryRPCURL)
tipperFundTx, err := sendSendTx(queryRPCURL, validatorKey, validatorAddr, tipperAddr, uint64(1000000000), fee, networkID, chainID, height)
if err != nil {
t.Fatalf("Failed to fund tipper: %v", err)
}
if ok, err := waitForTxInclusion(queryRPCURL, validatorAddr, tipperFundTx, 60*time.Second); err != nil || !ok {
t.Fatalf("Tipper funding tx not included: ok=%v err=%v", ok, err)
}
t.Log("Tipper funded")

// Step 3: publish_thread from creator
t.Log("Step 3: Publishing thread from creator...")
height, _ = getHeight(queryRPCURL)
contentHash := make([]byte, 32) // 32 zero bytes is fine for a structural test
for i := range contentHash {
contentHash[i] = byte(i)
}
publishMsg := map[string]interface{}{
"creator":     hexToBase64(creatorAddr),
"contentHash": base64Encode(contentHash),
"title":       "My First Lore Post",
}
publishTxHash, err := sendLoreTx(queryRPCURL, creatorKey, "publish_thread", publishMsg, fee, networkID, chainID, height)
if err != nil {
t.Fatalf("Failed to send publish_thread tx: %v", err)
}
t.Logf("publish_thread tx sent: %s", publishTxHash)
if ok, err := waitForTxInclusion(queryRPCURL, creatorAddr, publishTxHash, 60*time.Second); err != nil || !ok {
t.Fatalf("publish_thread tx not included: ok=%v err=%v", ok, err)
}
if failedCount, _ := checkTxNotFailed(queryRPCURL, creatorAddr); failedCount > 0 {
t.Fatalf("Creator has %d failed transactions after publish_thread", failedCount)
}
t.Log("publish_thread confirmed and not failed")

// Step 4: set_supporter_threshold from creator (low threshold so the tip below crosses it)
t.Log("Step 4: Setting supporter threshold...")
height, _ = getHeight(queryRPCURL)
thresholdMsg := map[string]interface{}{
"creator":        hexToBase64(creatorAddr),
"thresholdUcnpy": float64(50000000), // 50 tokens
}
thresholdTxHash, err := sendLoreTx(queryRPCURL, creatorKey, "set_supporter_threshold", thresholdMsg, fee, networkID, chainID, height)
if err != nil {
t.Fatalf("Failed to send set_supporter_threshold tx: %v", err)
}
if ok, err := waitForTxInclusion(queryRPCURL, creatorAddr, thresholdTxHash, 60*time.Second); err != nil || !ok {
t.Fatalf("set_supporter_threshold tx not included: ok=%v err=%v", ok, err)
}
if failedCount, _ := checkTxNotFailed(queryRPCURL, creatorAddr); failedCount > 0 {
t.Fatalf("Creator has %d failed transactions after set_supporter_threshold", failedCount)
}
t.Log("set_supporter_threshold confirmed and not failed")

// Step 5: tip_creator from tipper, amount above the threshold
t.Log("Step 5: Tipping creator...")
tipperBalBefore, _ := getAccountBalance(queryRPCURL, tipperAddr)
t.Logf("Tipper balance before tip: %d", tipperBalBefore)

height, _ = getHeight(queryRPCURL)
tipAmount := uint64(100000000) // 100 tokens, above the 50-token threshold
tipMsg := map[string]interface{}{
"tipper":  hexToBase64(tipperAddr),
"creator": hexToBase64(creatorAddr),
"amount":  float64(tipAmount),
}
tipTxHash, err := sendLoreTx(queryRPCURL, tipperKey, "tip_creator", tipMsg, fee, networkID, chainID, height)
if err != nil {
t.Fatalf("Failed to send tip_creator tx: %v", err)
}
if ok, err := waitForTxInclusion(queryRPCURL, tipperAddr, tipTxHash, 60*time.Second); err != nil || !ok {
t.Fatalf("tip_creator tx not included: ok=%v err=%v", ok, err)
}
if failedCount, _ := checkTxNotFailed(queryRPCURL, tipperAddr); failedCount > 0 {
t.Fatalf("Tipper has %d failed transactions after tip_creator", failedCount)
}
t.Log("tip_creator confirmed and not failed")

tipperBalAfter, _ := getAccountBalance(queryRPCURL, tipperAddr)
t.Logf("Tipper balance after tip: %d (expected drop of ~%d)", tipperBalAfter, tipAmount+fee)
if tipperBalBefore-tipperBalAfter != tipAmount+fee {
t.Errorf("Tipper balance delta = %d, want %d (tip+fee)", tipperBalBefore-tipperBalAfter, tipAmount+fee)
}

// Step 6: claim_tips from creator, should receive the pending tip balance minus claim fee
t.Log("Step 6: Creator claiming tips...")
creatorBalBefore, _ := getAccountBalance(queryRPCURL, creatorAddr)
t.Logf("Creator balance before claim: %d", creatorBalBefore)

height, _ = getHeight(queryRPCURL)
claimMsg := map[string]interface{}{
"creator": hexToBase64(creatorAddr),
}
claimTxHash, err := sendLoreTx(queryRPCURL, creatorKey, "claim_tips", claimMsg, fee, networkID, chainID, height)
if err != nil {
t.Fatalf("Failed to send claim_tips tx: %v", err)
}
if ok, err := waitForTxInclusion(queryRPCURL, creatorAddr, claimTxHash, 60*time.Second); err != nil || !ok {
t.Fatalf("claim_tips tx not included: ok=%v err=%v", ok, err)
}
if failedCount, _ := checkTxNotFailed(queryRPCURL, creatorAddr); failedCount > 0 {
t.Fatalf("Creator has %d failed transactions after claim_tips", failedCount)
}
t.Log("claim_tips confirmed and not failed")

creatorBalAfter, _ := getAccountBalance(queryRPCURL, creatorAddr)
t.Logf("Creator balance after claim: %d (expected increase of ~%d)", creatorBalAfter, tipAmount-fee)
if creatorBalAfter-creatorBalBefore != tipAmount-fee {
t.Errorf("Creator balance delta = %d, want %d (tip - claim fee)", creatorBalAfter-creatorBalBefore, tipAmount-fee)
}

t.Log("All Lore transactions confirmed successfully: publish -> threshold -> tip -> claim")
}

// base64Encode converts raw bytes to base64 string (protojson wire format for bytes fields).
func base64Encode(b []byte) string {
return hexToBase64(bytesToHex(b))
}

func bytesToHex(b []byte) string {
const hexdigits = "0123456789abcdef"
out := make([]byte, len(b)*2)
for i, v := range b {
out[i*2] = hexdigits[v>>4]
out[i*2+1] = hexdigits[v&0x0f]
}
return string(out)
}

// sendLoreTx is a Lore-specific tx builder that extends buildSignAndSendTx with
// the four Lore message types (publish_thread, tip_creator, claim_tips, set_supporter_threshold).
// It mirrors the plugin-only branch in buildSignAndSendTx exactly.
func sendLoreTx(rpcURL string, signerKey *keyGroup, msgType string, msgJSON map[string]interface{}, fee, networkID, chainID, height uint64) (string, error) {
typeURLs := map[string]string{
"publish_thread":          "type.googleapis.com/types.MsgPublishThread",
"tip_creator":             "type.googleapis.com/types.MsgTipCreator",
"claim_tips":              "type.googleapis.com/types.MsgClaimTips",
"set_supporter_threshold": "type.googleapis.com/types.MsgSetSupporterThreshold",
}
typeURL, ok := typeURLs[msgType]
if !ok {
return "", fmt.Errorf("unknown lore message type: %s", msgType)
}

// Build proto message for signing
var msgProto proto.Message
switch msgType {
case "publish_thread":
creator, _ := base64.StdEncoding.DecodeString(msgJSON["creator"].(string))
contentHash, _ := base64.StdEncoding.DecodeString(msgJSON["contentHash"].(string))
msgProto = &contract.MsgPublishThread{
Creator:     creator,
ContentHash: contentHash,
Title:       msgJSON["title"].(string),
}
case "tip_creator":
tipper, _ := base64.StdEncoding.DecodeString(msgJSON["tipper"].(string))
creator, _ := base64.StdEncoding.DecodeString(msgJSON["creator"].(string))
msgProto = &contract.MsgTipCreator{
Tipper:  tipper,
Creator: creator,
Amount:  uint64(msgJSON["amount"].(float64)),
}
case "claim_tips":
creator, _ := base64.StdEncoding.DecodeString(msgJSON["creator"].(string))
msgProto = &contract.MsgClaimTips{
Creator: creator,
}
case "set_supporter_threshold":
creator, _ := base64.StdEncoding.DecodeString(msgJSON["creator"].(string))
msgProto = &contract.MsgSetSupporterThreshold{
Creator:        creator,
ThresholdUcnpy: uint64(msgJSON["thresholdUcnpy"].(float64)),
}
}

msgBytes, err := proto.Marshal(msgProto)
if err != nil {
return "", fmt.Errorf("failed to marshal message: %v", err)
}

msgAny := &anypb.Any{TypeUrl: typeURL, Value: msgBytes}
txTime := uint64(time.Now().UnixMicro())

signBytes, err := crypto.GetSignBytes(msgType, msgAny, txTime, height, fee, "", networkID, chainID)
if err != nil {
return "", fmt.Errorf("failed to get sign bytes: %v", err)
}

privKey, err := crypto.StringToBLS12381PrivateKey(signerKey.PrivateKey)
if err != nil {
return "", fmt.Errorf("failed to parse private key: %v", err)
}
signature := privKey.Sign(signBytes)

pubKeyBytes, err := hex.DecodeString(signerKey.PublicKey)
if err != nil {
return "", fmt.Errorf("failed to decode public key: %v", err)
}

tx := map[string]interface{}{
"type":       msgType,
"msgTypeUrl": typeURL,
"msgBytes":   hex.EncodeToString(msgBytes),
"signature": map[string]string{
"publicKey": hex.EncodeToString(pubKeyBytes),
"signature": hex.EncodeToString(signature),
},
"time":          txTime,
"createdHeight": height,
"fee":           fee,
"memo":          "",
"networkID":     networkID,
"chainID":       chainID,
}

txJSONBytes, err := json.MarshalIndent(tx, "", "  ")
if err != nil {
return "", fmt.Errorf("failed to marshal transaction: %v", err)
}

respBody, err := postRawJSON(rpcURL+"/v1/tx", string(txJSONBytes))
if err != nil {
return "", fmt.Errorf("failed to send transaction: %v", err)
}

var txHash string
if err := json.Unmarshal(respBody, &txHash); err != nil {
return "", fmt.Errorf("failed to parse response: %v, body: %s", err, string(respBody))
}
return txHash, nil
}
