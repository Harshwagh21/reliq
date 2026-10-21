package timeout_test

import (
	"context"
	"fmt"
	"time"

	"github.com/harshwagh21/reliq/pkg/chain"
	"github.com/harshwagh21/reliq/pkg/timeout"
)

func ExampleNew() {
	tm, err := timeout.New(time.Second)
	if err != nil {
		fmt.Println(err)
		return
	}
	err = chain.Do(context.Background(), func(context.Context) error {
		return nil
	}, tm.Policy())
	fmt.Println(err == nil)
	// Output:
	// true
}
