// btcgw is an API Server that handles anchor CRUD for BBc-1 Ledger Subsystem.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ebiiim/btcgw/api"
	"github.com/ebiiim/btcgw/auth"
	"github.com/ebiiim/btcgw/btc"
	"github.com/ebiiim/btcgw/gw"
	"github.com/ebiiim/btcgw/model"
	"github.com/ebiiim/btcgw/store"
	"github.com/ebiiim/btcgw/util"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/go-chi/cors"
	"github.com/go-chi/httprate"
	_ "gocloud.dev/docstore/mongodocstore"
)

var (
	cliPath = util.GetEnvOr("BITCOIN_CLI_PATH", "./bitcoin-cli")
	btcNet  = model.BTCNet(uint8(util.MustAtoi(util.GetEnvOr("BITCOIN_NETWORK", "3")))) // model.BTCTestnet3
	rpcAddr = util.GetEnvOr("BITCOIND_ADDR", "")
	rpcPort = util.GetEnvOr("BITCOIND_PORT", "")
	rpcUser = util.GetEnvOr("BITCOIND_RPC_USER", "")
	rpcPW   = util.GetEnvOr("BITCOIND_RPC_PASSWORD", "")

	cmdprxEnabled = util.GetEnvBoolOr("CMDPROXY_ENABLED", false)
	cmdprxURL     = util.GetEnvOr("CMDPROXY_URL", "")
	cmdprxSecret  = util.GetEnvOr("CMDPROXY_SECRET", "")

	dev        = util.GetEnvBoolOr("DEV", false)
	port       = util.GetEnvIntOr("PORT", 8080)
	walletAddr = util.GetEnvOr("BITCOIN_WALLET_ADDR", "")
)

const (
	dbName      = "btcgw"
	anchorTable = "anchors"
	anchorKey   = "cid"
	authTable   = "apikeys"
	authKey     = "key"
	utxoTable   = "utxos"
	utxoKey     = "addr"
)

func useMongoDBAtlas() {
	// Please set environment variables first.
	// e.g. `set -a; source .env; set +a;`
	var (
		mongoUser = os.Getenv("MONGO_USER")
		mongoPW   = os.Getenv("MONGO_PASSWORD")
		mongoHost = os.Getenv("MONGO_HOSTNAME")
	)
	const (
		mongoEnv      = "MONGO_SERVER_URL"
		mongoAtlasFmt = "mongodb+srv://%s:%s@%s"
	)
	mongoAtlas := fmt.Sprintf(mongoAtlasFmt, mongoUser, mongoPW, mongoHost)
	if err := os.Setenv(mongoEnv, mongoAtlas); err != nil {
		panic(err)
	}
}

func mongoStore() string {
	return fmt.Sprintf("mongo://%s/%s?id_field=%s", dbName, anchorTable, anchorKey)
}

func mongoAuthenticator() string {
	return fmt.Sprintf("mongo://%s/%s?id_field=%s", dbName, authTable, authKey)
}

func mongoWallet() string {
	return fmt.Sprintf("mongo://%s/%s?id_field=%s", dbName, utxoTable, utxoKey)
}

func main() {
	// flag.IntVar(&port, "port", 8080, "HTTP port")
	// flag.BoolVar(&dev, "dev", false, "Use AnchorVersion 255 and prettify HTTP response body")
	// flag.StringVar(&walletAddr, "wallet", "", "Bitcoin address for sending transactions")
	// flag.Parse()

	// use environment variables

	if walletAddr == "" {
		log.Println("please set wallet")
		return
	}
	if dev {
		fmt.Println("Development Environment")
		// Set AnchorVersion to test.
		model.XAnchorVersion(255)
		api.PrettifyResponseJSON = true
	} else {
		fmt.Println("")
		fmt.Println("==================================")
		fmt.Println("===== Production Environment =====")
		fmt.Println("==================================")
		fmt.Println("")
	}

	useMongoDBAtlas()
	// Setup Gateway.
	var err error
	var btcCLI *btc.BitcoinCLI
	if cmdprxEnabled {
		btcCLI = btc.MustNewBitcoinCLIWithCmdProxy(cliPath, btcNet, rpcAddr, rpcPort, rpcUser, rpcPW, cmdprxURL, cmdprxSecret)
	} else {
		btcCLI = btc.NewBitcoinCLI(cliPath, btcNet, rpcAddr, rpcPort, rpcUser, rpcPW)
	}
	docStore := store.NewDocstore(mongoStore())
	if err = docStore.Open(); err != nil {
		log.Println(err)
		return
	}
	wallet := btc.MustNewDocstoreWallet(mongoWallet(), walletAddr)
	gwImpl := gw.NewGatewayImpl(model.BTCTestnet3, btcCLI, wallet, docStore)

	// Setup Authenticator.
	var a auth.Authenticator
	a = auth.MustNewDocstoreAuth(mongoAuthenticator())
	// a = &auth.SpecialAuth{}

	// Setup GatewayService.
	gwService := api.NewGatewayService(gwImpl, a)
	defer func() {
		if cErr := gwService.Close(); cErr != nil {
			log.Printf("%v (captured err: %v)", cErr, err)
		}
	}()

	// Setup Chi.
	r := chi.NewRouter()
	r.Use(middleware.RealIP)                     // use this only if you have a trusted reverse proxy
	r.Use(httprate.LimitByIP(60, 1*time.Minute)) // returns 429
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Heartbeat("/healthz"))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "PATCH"}, // browsers do not POST
		AllowedHeaders:   []string{"*"},
		ExposedHeaders:   []string{"*"},
		AllowCredentials: false,
		MaxAge:           300,
	}))
	r.Use(gwService.OAPIValidator())
	api.AnchorHandlerFromMux(gwService, r)

	// Serve.
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	fmt.Printf("Listening on: http://%s\n", addr)
	s := &http.Server{
		Handler: r,
		Addr:    addr,
	}

	sigCh := make(chan os.Signal)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		log.Println(s.ListenAndServe())
		sigCh <- syscall.SIGTERM
	}()
	sig := <-sigCh
	fmt.Printf("Signal <%s> received. Shutting down...\n", sig)
	ctx, cancelFunc := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelFunc()
	if err := s.Shutdown(ctx); err != nil {
		log.Printf("Graceful shutdown failed: %v\n", err)
	}
}
