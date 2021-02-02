package btc

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/ebiiim/btc-gateway/model"
)

type cliCmd string

const (
	cmdPing = "ping"
	// cmdGetNewAddress                = "getnewaddress"
	// cmdListUnspent                  = "listunspent"
	cmdGetBalance                   = "getbalance"
	cmdGetTransaction               = "gettransaction"
	cmdCreateRawTransaction         = "createrawtransaction"
	cmdSignRawTransactionWithWallet = "signrawtransactionwithwallet"
	cmdSendRawTransaction           = "sendrawtransaction"
)

type cliArg string

const (
	argChain       = "-chain="
	argRPCAddr     = "-rpcconnect="
	argRPCPort     = "-rpcport="
	argRPCUser     = "-rpcuser="
	argRPCPassword = "-rpcpassword="
)

type exitCode int

const (
	exitOK              = 0
	exitERR             = 1
	exitInvalidTXID     = 5
	exitWrongSizeTXID   = 8
	exitWalletNotLoaded = 18
)

// Errors
var (
	ErrUnexpectedExitCode   = errors.New("ErrUnexpectedExitCode")
	ErrExitCode1            = errors.New("ErrExitCode1")
	ErrDryRun               = errors.New("")
	ErrFailedToExec         = errors.New("ErrFailedToExec")
	ErrFailedToDecode       = errors.New("ErrFailedToDecode")
	ErrPingFailed           = errors.New("ErrPingFailed")
	ErrInvalidTransactionID = errors.New("ErrInvalidTransactionID")
	ErrWalletNotLoaded      = errors.New("ErrWalletNotLoaded")
	ErrInvalidFee           = errors.New("ErrInvalidFee")
	ErrFailedToSign         = errors.New("ErrFailedToSign")
)

// BitcoinCLI contains parameters for bitcoin-cli.
type BitcoinCLI struct {
	// Path
	binPath string
	// Connection
	btcNet      model.BTCNet
	rpcAddr     string
	rpcPort     string
	rpcUser     string
	rpcPassword string
}

// NewBitcoinCLI initializes a BitcoinCLI.
//
// Parameters:
//   - binPath sets path to bitcoin-cli. Both relative and absolute are acceptable.
//   - btcNet sets target Bitcoin network. A valid value must be set.
//   - rpcAddr sets IP address to which bitcoin-cli connects. If "" is set, default value will be used.
//   - rpcPort sets TCP port to which bitcoin-cli connects. If "" is set, default value will be used.
//   - rpcUser sets RPC username used by bitcoin-cli. If "" is set, default value will be used.
//   - rpcPassword sets RPC password used by bitcoin-cli. If "" is set, default value will be used.
//
// This function does NOT check the status of the target bitcoind.
// If necessary, call b.Ping after calling this function.
func NewBitcoinCLI(binPath string, btcNet model.BTCNet, rpcAddr, rpcPort, rpcUser, rpcPassword string) *BitcoinCLI {
	b := &BitcoinCLI{
		binPath:     binPath,
		btcNet:      btcNet,
		rpcAddr:     rpcAddr,
		rpcPort:     rpcPort,
		rpcUser:     rpcUser,
		rpcPassword: rpcPassword,
	}
	return b
}

func (b *BitcoinCLI) connArgs() []string {
	var s []string
	switch b.btcNet {
	case model.BTCMainnet:
		s = append(s, argChain+"main")
	case model.BTCTestnet3:
		s = append(s, argChain+"test")
	case model.BTCTestnet4:
		panic("not implemented")
	}
	if b.rpcAddr != "" {
		s = append(s, argRPCAddr+b.rpcAddr)
	}
	if b.rpcPort != "" {
		s = append(s, argRPCPort+b.rpcPort)
	}
	if b.rpcUser != "" {
		s = append(s, argRPCUser+b.rpcUser)
	}
	if b.rpcPassword != "" {
		s = append(s, argRPCPassword+b.rpcPassword)
	}
	return s
}

func removeSpecialChars(buf *bytes.Buffer, enable bool) *bytes.Buffer {
	if !enable {
		return buf
	}
	var buf2 bytes.Buffer
	bb := buf.Bytes()
	for _, b := range bb {
		switch b {
		case 0x000d, 0x000a: // CR, LF
			continue
		default:
			_ = buf2.WriteByte(b) // always nil
		}
	}
	return &buf2
}

// Test use only.
var dryRun = false

// run returns (stdout, stderr, error).
func (b *BitcoinCLI) run(ctx context.Context, args []string, noCRLF bool) (*bytes.Buffer, *bytes.Buffer, error) {
	args = append(b.connArgs(), args...)
	if dryRun {
		return nil, nil, fmt.Errorf("%w%s %s", ErrDryRun, b.binPath, strings.Join(args, " "))
	}
	cmd := exec.CommandContext(ctx, b.binPath, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, nil, fmt.Errorf("%w (%v)", ErrFailedToExec, err)
	}
	ec := cmd.ProcessState.ExitCode()
	so := removeSpecialChars(&stdout, noCRLF)
	se := removeSpecialChars(&stderr, noCRLF)
	switch ec {
	case exitOK:
		return so, se, nil
	case exitERR:
		return so, se, ErrExitCode1
	case exitInvalidTXID, exitWrongSizeTXID:
		return so, se, ErrInvalidTransactionID
	case exitWalletNotLoaded:
		return so, se, ErrWalletNotLoaded
	default:
		return so, se, fmt.Errorf("%w (%v)", ErrUnexpectedExitCode, ec)
	}
}

// Ping pings bitcoind and returns nil if successful.
//
// Possible errors: ErrExitCode1|ErrUnexpectedExitCode|ErrFailedToExec
func (b *BitcoinCLI) Ping(ctx context.Context) error {
	if stdout, stderr, err := b.run(ctx, []string{cmdPing}, true); err != nil {
		if errors.Is(err, ErrDryRun) {
			return err
		}
		return fmt.Errorf("%w (stdout=%s, stderr=%s)", err, stdout.String(), stderr.String())
	}
	return nil
}

// GetBalance returns balance of the default wallet.
//
// Possible errors: ErrWalletNotLoaded|ErrUnexpectedExitCode|ErrFailedToExec
func (b *BitcoinCLI) GetBalance(ctx context.Context) (string, error) {
	stdout, stderr, err := b.run(ctx, []string{cmdGetBalance}, true)
	if err != nil {
		if errors.Is(err, ErrDryRun) {
			return "", err
		}
		return "", fmt.Errorf("%w (stdout=%s, stderr=%s)", err, stdout.String(), stderr.String())
	}
	s := stdout.String()
	if len(s) == 0 {
		return "", fmt.Errorf("%w (%v)", ErrFailedToDecode, err)
	}
	return s, nil
}

// GetTransaction returns a transaction in JSON.
//
// Possible errors: ErrInvalidTransactionID|ErrWalletNotLoaded|ErrUnexpectedExitCode|ErrFailedToExec
func (b *BitcoinCLI) GetTransaction(ctx context.Context, txid []byte) (*bytes.Buffer, error) {
	stdout, stderr, err := b.run(ctx, []string{cmdGetTransaction, hex.EncodeToString(txid)}, false)
	if err != nil {
		if errors.Is(err, ErrDryRun) {
			return nil, err
		}
		return nil, fmt.Errorf("%w (stdout=%s, stderr=%s)", err, stdout.String(), stderr.String())
	}
	return stdout, nil
}

func calcFee(bal string, feeSatoshi uint) (string, error) {
	f64Bal, err := strconv.ParseFloat(bal, 64)
	if err != nil {
		return "", err
	}
	var rate float64 = 100_000_000
	f64Bal = float64(uint(f64Bal*rate)-feeSatoshi) / rate
	return fmt.Sprintf("%.8f", f64Bal), err
}

// ParseTransactionReceived returns received amount of the given Bitcoin address.
func (b *BitcoinCLI) ParseTransactionReceived(txJSON *bytes.Buffer, recvAddr string) (string, error) {
	var val map[string]interface{}
	if err := json.NewDecoder(txJSON).Decode(&val); err != nil {
		return "", fmt.Errorf("%w (%v)", ErrFailedToDecode, err)
	}
	// Parse { ..., details: [ { "address": "abc", "category": "receive", "amount": 0.123 } ] }
	details, ok := val["details"].([]map[string]interface{})
	if !ok {
		return "", fmt.Errorf("%w (root->details)", ErrFailedToDecode)
	}
	for _, d := range details {
		// category == receive ? pass : continue
		cat, ok := d["categoty"].(string)
		if !ok {
			return "", fmt.Errorf("%w (root->details->category)", ErrFailedToDecode)
		}
		if cat != "receive" {
			continue
		}
		// address == ${recvAddr} ? pass : continue
		addr, ok := d["address"].(string)
		if !ok {
			return "", fmt.Errorf("%w (root->details->address)", ErrFailedToDecode)
		}
		if addr != recvAddr {
			continue
		}
		// amount is float ? return : ErrFailedToDecode
		amo, ok := d["amount"].(float64)
		if !ok {
			return "", fmt.Errorf("%w (root->details->amount)", ErrFailedToDecode)
		}
		return fmt.Sprintf("%.8f", amo), nil
	}
	return "", fmt.Errorf("%w (not found)", ErrFailedToDecode)
}

// CreateRawTransactionForAnchor creates a raw transaction with one vout and one OP_RETURN.
//
// Parameters:
//   - fromTxid sets UTXO.
//   - balance sets balance of fromTxid.
//   - toAddr sets destination Bitcoin address.
//   - fee sets transaction fee in Satoshi.
//     - Info: BTC 0.0002 = Satoshi 20,000
//   - data sets OP_RETURN data. Up to 80 bytes.
//
// Possible errors: ErrInvalidFee|ErrExitCode1|ErrUnexpectedExitCode|ErrFailedToExec
func (b *BitcoinCLI) CreateRawTransactionForAnchor(ctx context.Context, fromTxid []byte, balance string, toAddr string, fee uint, data []byte) ([]byte, error) {
	sFee, err := calcFee(balance, fee)
	if err != nil {
		return nil, fmt.Errorf("%w (%v)", ErrInvalidFee, err)
	}
	argFmt0 := `[{"txid": "%s", "vout": 0}]`
	argFmt1 := `[{"%s": %s}, {"data": "%s"}]`
	arg0 := fmt.Sprintf(argFmt0, hex.EncodeToString(fromTxid))
	arg1 := fmt.Sprintf(argFmt1, toAddr, sFee, hex.EncodeToString(data))
	stdout, stderr, err := b.run(ctx, []string{cmdCreateRawTransaction, arg0, arg1}, true)
	if err != nil {
		if errors.Is(err, ErrDryRun) {
			return nil, err
		}
		return nil, fmt.Errorf("%w (stdout=%s, stderr=%s)", err, stdout.String(), stderr.String())
	}
	bs, err := hex.DecodeString(stdout.String())
	if err != nil || len(bs) == 0 {
		return nil, fmt.Errorf("%w (%v)", ErrFailedToDecode, err)
	}
	return bs, nil
}

// SignRawTransactionWithWallet signs the given transaction with the default wallet in bitcoin-cli and returns JSON.
//
// Possible errors: ErrWalletNotLoaded|ErrUnexpectedExitCode|ErrFailedToExec
func (b *BitcoinCLI) SignRawTransactionWithWallet(ctx context.Context, rawTx []byte) (*bytes.Buffer, error) {
	stdout, stderr, err := b.run(ctx, []string{cmdSignRawTransactionWithWallet, hex.EncodeToString(rawTx)}, false)
	if err != nil {
		if errors.Is(err, ErrDryRun) {
			return nil, err
		}
		return nil, fmt.Errorf("%w (stdout=%s, stderr=%s)", err, stdout.String(), stderr.String())
	}
	return stdout, nil
}

// ParseSignRawTransactionWithWallet parses the response from b.SignRawTransactionWithWallet.
func (b *BitcoinCLI) ParseSignRawTransactionWithWallet(stdout io.Reader) ([]byte, error) {
	var val map[string]interface{}
	if err := json.NewDecoder(stdout).Decode(&val); err != nil {
		return nil, fmt.Errorf("%w (%v)", ErrFailedToDecode, err)
	}
	// Parse { "hex": "12345", "complete": true, ... }
	// return complete ? hex : ErrFailedToSign
	completed, ok := val["complete"].(bool)
	if !ok {
		return nil, fmt.Errorf("%w (root->complete)", ErrFailedToDecode)
	}
	hexStr, ok := val["hex"].(string)
	if !ok {
		return nil, fmt.Errorf("%w (root->hex)", ErrFailedToDecode)
	}
	if !completed {
		return nil, fmt.Errorf("%w (%s)", ErrFailedToSign, hexStr)
	}
	bs, err := hex.DecodeString(hexStr)
	if err != nil || len(bs) == 0 {
		return nil, fmt.Errorf("%w (%v)", ErrFailedToDecode, err)
	}
	return bs, nil
}

// SendRawTransaction sends the given signed raw transaction and returns transaction ID.
//
// Possible errors: ErrUnexpectedExitCode|ErrFailedToExec
// TODO: Handle more errors.
func (b *BitcoinCLI) SendRawTransaction(ctx context.Context, signedRawTx []byte) ([]byte, error) {
	stdout, stderr, err := b.run(ctx, []string{cmdSendRawTransaction, hex.EncodeToString(signedRawTx)}, true)
	if err != nil {
		if errors.Is(err, ErrDryRun) {
			return nil, err
		}
		return nil, fmt.Errorf("%w (stdout=%s, stderr=%s)", err, stdout.String(), stderr.String())
	}
	bs, err := hex.DecodeString(stdout.String())
	if err != nil || len(bs) == 0 {
		return nil, fmt.Errorf("%w (%v)", ErrFailedToDecode, err)
	}
	return bs, nil
}

func (b *BitcoinCLI) PutAnchor(ctx context.Context, a *model.Anchor) ([]byte, error) {
	panic("not implemented")
}

func (b *BitcoinCLI) GetAnchor(ctx context.Context, btctx []byte) (*model.AnchorRecord, error) {
	panic("not implemented")
}