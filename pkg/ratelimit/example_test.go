package ratelimit_test

import (
	"context"
	"fmt"

	"github.com/harshwagh21/reliq/pkg/chain"
	"github.com/harshwagh21/reliq/pkg/ratelimit"
)

func ExampleNew() {
	l, err := ratelimit.New(10, 1)
	if err != nil {
		fmt.Println(err)
		return
	}
	err = chain.Do(context.Background(), func(context.Context) error {
		return nil
	}, l.Policy())
	fmt.Println(err == nil)
	// Output:
	// true
}
