package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ebiiim/btcgw/btc"
	"github.com/ebiiim/btcgw/model"
	"github.com/ebiiim/btcgw/util"
)

func init() {
	// Set AnchorVersion to test.
	model.XAnchorVersion(255)
}

var (
	cliPath = util.GetEnvOr("BITCOIN_CLI_PATH", "../../bitcoin-cli")
	btcNet  = model.BTCNet(uint8(util.MustAtoi(util.GetEnvOr("BITCOIN_NETWORK", "3")))) // model.BTCTestnet3
	rpcAddr = util.GetEnvOr("BITCOIND_ADDR", "")
	rpcPort = util.GetEnvOr("BITCOIND_PORT", "")
	rpcUser = util.GetEnvOr("BITCOIND_RPC_USER", "")
	rpcPW   = util.GetEnvOr("BITCOIND_RPC_PASSWORD", "")
)

func main() {
	var b btc.BTC
	xCLI := btc.NewBitcoinCLI(cliPath, btcNet, rpcAddr, rpcPort, rpcUser, rpcPW)
	b = xCLI

	// Get the Anchor specified by given Bitcoin transaction ID.
	btctx := util.MustDecodeHexString("6928e1c6478d1f55ed1a5d86e1ab24669a14f777b879bbb25c746543810bf916")
	ctx, cancelFunc := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelFunc()
	ar, err := b.GetAnchor(ctx, btctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s\n", ar)
}
