package bulkhead_test

import (
	"context"
	"fmt"

	"github.com/harshwagh21/reliq/pkg/bulkhead"
	"github.com/harshwagh21/reliq/pkg/chain"
)

func ExampleNew() {
	b, err := bulkhead.New(2)
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
