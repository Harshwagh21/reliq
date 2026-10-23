package circuitbreaker_test

import (
	"context"
	"fmt"
	"time"

	"github.com/harshwagh21/reliq/pkg/chain"
	"github.com/harshwagh21/reliq/pkg/circuitbreaker"
)

func ExampleNew() {
	b, err := circuitbreaker.New(circuitbreaker.Config{
		FailureThreshold: 1,
		OpenTimeout:      time.Second,
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	err = chain.Do(context.Background(), func(context.Context) error {
		return nil
	}, b.Policy())
	fmt.Println(err == nil)
	// Output:
	// true
}
