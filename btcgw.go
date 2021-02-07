package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/ebiiim/btc-gateway/api"
	"github.com/ebiiim/btc-gateway/btc"
	"github.com/ebiiim/btc-gateway/gw"
	"github.com/ebiiim/btc-gateway/model"
	"github.com/ebiiim/btc-gateway/store"
	"github.com/ebiiim/btc-gateway/util"

	oapimiddleware "github.com/deepmap/oapi-codegen/pkg/chi-middleware"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
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
)

const (
	dbName    = "btcgw"
	tableName = "anchors"
	key       = "cid"
)

func useMongoDBAtlas() string {
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
		log.Println(err)
		return ""
	}
	return fmt.Sprintf("mongo://%s/%s?id_field=%s", dbName, tableName, key)
}

var middlewareHealthz = func(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "" || r.URL.Path == "/healthz" || r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	var port = flag.Int("port", 8080, "HTTP port")
	var dev = flag.Bool("dev", false, "Use AnchorVersion 255 and prettify HTTP response body")
	flag.Parse()

	if *dev {
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

	// Setup GatewayService.
	var err error
	btcCLI := btc.NewBitcoinCLI(cliPath, btcNet, rpcAddr, rpcPort, rpcUser, rpcPW)
	conn := useMongoDBAtlas()
	docStore := store.NewDocstore(conn)
	if err = docStore.Open(); err != nil {
		log.Println(err)
		return
	}
	defer func() {
		if cErr := docStore.Close(); cErr != nil {
			log.Printf("%v (captured err: %v)", cErr, err)
		}
	}()
	gwImpl := gw.NewGatewayImpl(model.BTCTestnet3, btcCLI, docStore)
	gwService := api.NewGatewayService(gwImpl)

	// Setup Swagger.
	swagger, err := api.GetSwagger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not load swagger spec\n: %s", err)
		os.Exit(1)
	}
	// Skips validating server names.
	swagger.Servers = nil

	//	Setup validator.
	validatorOpts := &oapimiddleware.Options{}
	validatorOpts.Options.AuthenticationFunc = func(c context.Context, input *openapi3filter.AuthenticationInput) error {
		h := input.RequestValidationInput.Request.Header["X-Api-Key"]
		if h == nil {
			return errors.New("X-API-KEY not found")
		}
		if !api.AuthFn(h[0], input.RequestValidationInput.PathParams) {
			return errors.New("auth failed")
		}
		return nil
	}

	// Setup Chi.
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(httprate.LimitByIP(100, 1*time.Minute))
	r.Use(middlewareHealthz)
	r.Use(oapimiddleware.OapiRequestValidatorWithOptions(swagger, validatorOpts))
	api.HandlerFromMux(gwService, r)
	opts := api.ChiServerOptions{
		BaseURL:     "",
		BaseRouter:  r,
		Middlewares: nil,
	}
	api.HandlerWithOptions(gwService, opts)

	// Serve.
	addr := fmt.Sprintf("0.0.0.0:%d", *port)
	fmt.Printf("Listening on: http://%s\n", addr)
	s := &http.Server{
		Handler: r,
		Addr:    addr,
	}
	log.Fatal(s.ListenAndServe())
}