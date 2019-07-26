package goarbitrum

import (
	"context"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/offchainlabs/arb-validator/coordinator"
	"log"
	"math"
	"sync"
	"time"
)

///////////////////////////////////////////////////////////////////////////////
// Methods of ContractFilterer

// FilterLogs executes a log filter operation, blocking during execution and
// returning all the results in one batch.
//
// TODO(karalabe): Deprecate when the subscription one can return past data too.
func (conn *ArbConnection) FilterLogs(ctx context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
	return nil, _nyiError("FilterLogs")
}

// SubscribeFilterLogs creates a background log filtering operation, returning
// a subscription immediately, which can be used to stream the found events.
func (conn *ArbConnection) SubscribeFilterLogs(
	ctx   context.Context,
	query ethereum.FilterQuery,
	ch    chan<- types.Log,
) (ethereum.Subscription, error) {
	return newSubscription(conn, query, ch), nil
}

const subscriptionPollingInterval = 1*time.Second

type subscription struct {
	proxy            ValidatorProxy
	firstBlockUnseen uint64
	active           bool
	logChan          chan<- types.Log
	errChan          chan error
	address          common.Address
	topics           [][32]byte
	unsubOnce        *sync.Once
}

func _extractAddrTopics(query ethereum.FilterQuery) (addr common.Address, topics [][32]byte) {
	if len(query.Addresses) > 1 {
		panic("GoArbitrum: subscription can't handle more than one contract address")
	}
	addr = query.Addresses[0]

	topics = make([][32]byte, len(query.Topics))
	for i,sl := range query.Topics {
		if len(sl) > 1 {
			panic("GoArbitrum: subscription can't handle ORs of topics")
		}
		copy(topics[i][:], sl[0][:])
	}
	return
}

func _decodeLogInfo(ins *coordinator.LogInfo) (*types.Log, error) {
	outs := &types.Log{}
	addr, err := hexutil.Decode(ins.Address)
	if err != nil {
		log.Println("_decodeLogInfo error 1:", err)
		return nil, err
	}
	copy(outs.Address[:], addr)
	outs.Topics = make([]common.Hash, len(ins.Topics))
	for i,top := range ins.Topics {
		decodedTopic, err := hexutil.Decode(top)
		if err != nil {
			log.Println("_decodeLogInfo error 2:", err)
			return nil, err
		}
		copy(outs.Topics[i][:], decodedTopic)
	}
	outs.Data, err = hexutil.Decode(ins.Data)
	if err != nil {
		log.Println("_decodeLogInfo error 3:", err)
		return nil, err
	}
	outs.BlockNumber, err = hexutil.DecodeUint64(ins.BlockNumber)
	if err != nil {
		log.Println("_decodeLogInfo error 4:", err)
		return nil, err
	}
	hh, err := hexutil.Decode(ins.Address)
	if err != nil {
		log.Println("_decodeLogInfo error 5:", err)
		return nil, err
	}
	copy(outs.TxHash[:], hh)
	txi64, err := hexutil.DecodeUint64(ins.TransactionIndex)
	if err != nil {
		log.Println("_decodeLogInfo error 6:", err)
		log.Println("value was", ins.TransactionIndex)
		return nil, err
	}
	outs.TxIndex = uint(txi64)
	hh, err = hexutil.Decode(ins.BlockHash)
	if err != nil {
		log.Println("_decodeLogInfo error 7:", err)
		return nil, err
	}
	copy(outs.BlockHash[:], hh)
	iui, err := hexutil.DecodeUint64(ins.LogIndex)
	if err != nil {
		log.Println("_decodeLogInfo error 8:", err)
		return nil, err
	}
	outs.Index = uint(iui)
	outs.Removed = false
	return outs, nil
}

func newSubscription(conn *ArbConnection, query ethereum.FilterQuery, ch chan<- types.Log) *subscription {
	address, topics := _extractAddrTopics(query)
	sub := &subscription{
		conn.proxy,
		0,
		true,
		ch,
		make(chan error, 1),
		address,
		topics,
		&sync.Once{},
	}
	go func() {
		defer sub.Unsubscribe()
		for {
			time.Sleep(subscriptionPollingInterval)
			if !sub.active { return }
			logInfos, err := sub.proxy.FindLogs(int64(sub.firstBlockUnseen), math.MaxInt32, sub.address[:], sub.topics)
			if err != nil {
				sub.errChan <- err
				return
			}
			for _, logInfo := range logInfos {
				outs, err := _decodeLogInfo(&logInfo)
				if err != nil {
					sub.errChan <- err
					return
				}
				ok := true
				for i,targetTopic := range topics {
					if targetTopic != outs.Topics[i] {
						ok = false
					}
				}
				if outs.BlockNumber < sub.firstBlockUnseen {
					ok = false
				}
				if ok {
					sub.logChan <- *outs
					if sub.firstBlockUnseen <= outs.BlockNumber {
						sub.firstBlockUnseen = outs.BlockNumber+1
					}
				}
			}
		}
	}()
	return sub
}

// Unsubscribe cancels the sending of events to the data channel
// and closes the error channel.
func (sub *subscription) Unsubscribe() {
	sub.unsubOnce.Do(func () {
		sub.active = false
		close(sub.errChan)
	})
}

// Err returns the subscription error channel. The error channel receives
// a value if there is an issue with the subscription (e.g. the network connection
// delivering the events has been closed). Only one value will ever be sent.
// The error channel is closed by Unsubscribe.
func (sub *subscription) Err() <-chan error {
	return sub.errChan
}