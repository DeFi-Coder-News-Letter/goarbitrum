package goarbitrum

import (
	"context"
	"errors"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto/secp256k1"
	solsha3 "github.com/miguelmota/go-solidity-sha3"
	"github.com/offchainlabs/arb-util/evm"
	"github.com/offchainlabs/arb-util/value"
	"log"
	"math/big"
	"time"
)

type ArbConnection struct {
	proxy      ValidatorProxy
	myAddress  common.Address
	vmId       []byte
	privateKey []byte
	hexPubkey  string
}

func Dial(url string, myAddress common.Address, privateKey []byte, hexPubkey string) (*ArbConnection, error) {
	proxy := NewValidatorProxyImpl(url)
	vmIdStr, err := proxy.GetVMInfo()
	if err != nil {
		return nil, err
	}
	vmId, err := hexutil.Decode(vmIdStr)
	if err != nil {
		return nil, err
	}
	return &ArbConnection{ proxy, myAddress, vmId, append([]byte{}, privateKey...), hexPubkey }, nil
}

func _nyiError(funcname string) error {
	return errors.New("goarbitrum error: "+funcname+" not yet implemented")
}

func _extendTo32Bytes(arr []byte) []byte {
	if len(arr) > 32 {
		panic("invalid call to _extendTo32Bytes")
	}
	ret := make([]byte, 32)
	//copy(ret[32-len(arr):], arr)
	copy(ret[:], arr)
	return ret
}

///////////////////////////////////////////////////////////////////////////////
// Methods of ContractCaller

// CodeAt returns the code of the given account. This is needed to differentiate
// between contract internal errors and the local chain being out of sync.
func (conn *ArbConnection) CodeAt(
	ctx         context.Context,
	contract    common.Address,
	blockNumber *big.Int,
) ([]byte, error) {
	return nil, _nyiError("CodeAt")
}

// CallContract executes an Ethereum contract call with the specified data as the
// input.
func (conn *ArbConnection) CallContract(
	ctx         context.Context,
	call        ethereum.CallMsg,
	blockNumber *big.Int,
) ([]byte, error) {
	dataValue, err := evm.BytesToSizedByteArray(call.Data)
	if err != nil {
		return nil, err
	}
	destAddrValue := value.NewIntValue(new(big.Int).SetBytes(call.To[:]))
	seqNumValue := value.NewIntValue(new(big.Int).Sub(new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil), big.NewInt(2)))
	arbCallValue, err := value.NewTupleFromSlice([]value.Value{ dataValue, destAddrValue, seqNumValue })
	if err != nil {
		panic("Unexpected error building arbCallValue")
	}
	retValue, succeeded, err := conn.proxy.CallMessage(arbCallValue, call.From)
	if err != nil {
		return nil, err
	}
	if succeeded {
		return evm.SizedByteArrayToHex(retValue)
	} else {
		return nil, errors.New("call reverted")
	}
}

///////////////////////////////////////////////////////////////////////////////
// Methods of ContractTransactor

// PendingCodeAt returns the code of the given account in the pending state.
func (conn *ArbConnection) PendingCodeAt(
	ctx     context.Context,
	account common.Address,
) ([]byte, error) {
	return nil, _nyiError("PendingCodeAt")
}

// PendingNonceAt retrieves the current pending nonce associated with an account.
func (conn *ArbConnection) PendingNonceAt(
	ctx     context.Context,
	account common.Address,
) (uint64, error) {
	return 0, nil
}

// SuggestGasPrice retrieves the currently suggested gas price to allow a timely
// execution of a transaction.
func (conn *ArbConnection) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	return big.NewInt(0), nil
}

// EstimateGas tries to estimate the gas needed to execute a specific
// transaction based on the current pending state of the backend blockchain.
// There is no guarantee that this is the true gas limit requirement as other
// transactions may be added or removed by miners, but it should provide a basis
// for setting a reasonable default.

// EstimateGas tries to estimate the gas needed to execute a specific
// transaction based on the current pending state of the backend blockchain.
// There is no guarantee that this is the true gas limit requirement as other
// transactions may be added or removed by miners, but it should provide a basis
// for setting a reasonable default.
func (conn *ArbConnection) EstimateGas(
	ctx context.Context,
	call ethereum.CallMsg,
) (gas uint64, err error) {
	return 100000, nil
}

// SendTransaction injects the transaction into the pending pool for execution.
func (conn *ArbConnection) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	dataValue, err := evm.BytesToSizedByteArray(tx.Data())
	if err != nil {
		log.Println("Error converting to SizedByteArray")
		return err
	}
	destAddrValue := value.NewIntValue(new(big.Int).SetBytes(tx.To()[:]))
	seqNumValue := value.NewIntValue(new(big.Int).Sub(new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil), big.NewInt(2)))

	arbCallValue, err := value.NewTupleFromSlice([]value.Value{ dataValue, destAddrValue, seqNumValue })
	if err != nil {
		panic("Unexpected error building arbCallValue")
	}

	tokenType := [21]byte{}
	messageHash := solsha3.SoliditySHA3(
		solsha3.Bytes32(conn.vmId),
		solsha3.Bytes32(arbCallValue.Hash()),
		solsha3.Uint256(big.NewInt(0)),  // amount
		tokenType[:],
	)
	signedMsg := solsha3.SoliditySHA3WithPrefix(solsha3.Bytes32(messageHash))
	sig, err := secp256k1.Sign(signedMsg, conn.privateKey)
	if err != nil {
		return err
	}

	txHash, err := conn.proxy.SendMessage(arbCallValue, conn.hexPubkey, sig)
	if err != nil {
		log.Println("SendTransaction: error returned from proxy.SendMessage:", err)
		return err
	}

	return func() error {
		for {
			resultVal, ok, err := conn.proxy.GetMessageResult(txHash)
			if err != nil {
				log.Println("GetMessageResult error:", err)
				return err
			}
			if !ok {
				time.Sleep(2*time.Second)
			} else {
				result, err := evm.ProcessLog(resultVal)
				if err != nil {
					log.Println("GetMessageResultLog error:", err)
					return err
				}
				switch res := result.(type) {
				case evm.Revert:
					log.Println("call reverted:", string(res.ReturnVal))
				default:
					// do nothing
				}
				return nil
			}
		}
	}()
}
