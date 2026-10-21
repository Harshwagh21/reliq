package chain_test

import (
	"context"
	"fmt"

	"github.com/harshwagh21/reliq/pkg/chain"
)

func ExampleDo() {
	err := chain.Do(context.Background(), func(context.Context) error {
		return nil
	})
	fmt.Println(err == nil)
	// Output:
	// true
}

func ExampleGet() {
	v, err := chain.Get(context.Background(), func(context.Context) (string, error) {
		return "ok", nil
	})
	fmt.Println(v, err == nil)
	// Output:
	// ok true
}
