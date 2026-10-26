// Package main opens a circuit on the first failure and rejects the next call.
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/harshwagh21/reliq/pkg/chain"
	"github.com/harshwagh21/reliq/pkg/circuitbreaker"
)

func main() {
	b, err := circuitbreaker.New(circuitbreaker.Config{
		FailureThreshold: 1,
		OpenTimeout:      time.Hour,
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	err = chain.Do(context.Background(), func(context.Context) error {
		return errors.New("fail")
	}, b.Policy())
	if err == nil {
		fmt.Println("first call succeeded")
		return
	}
	err = chain.Do(context.Background(), func(context.Context) error {
		return nil
	}, b.Policy())
	fmt.Println(errors.Is(err, chain.ErrCircuitOpen))
}
